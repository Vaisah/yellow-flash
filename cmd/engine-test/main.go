// Command engine-test drives the download engine from a terminal, without
// the desktop UI.
//
//	engine-test [flags] URL          download a URL
//	engine-test -db s.db -resume ID  resume a paused or failed download
//	engine-test -db s.db -list       list recorded downloads
//
// Ctrl+C pauses the transfer and persists progress (resumable with -db).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	appdownload "github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/application/events"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/memory"
	"github.com/vaisah/internal/infrastructure/persistence/sqlite"
)

type headerFlags map[string]string

func (h headerFlags) String() string { return fmt.Sprintf("%d header(s)", len(h)) }

func (h headerFlags) Set(v string) error {
	name, value, ok := strings.Cut(v, ":")
	if !ok {
		return errors.New(`header must be "Name: value"`)
	}
	h[strings.TrimSpace(name)] = strings.TrimSpace(value)
	return nil
}

func main() { os.Exit(run()) }

func run() int {
	headers := headerFlags{}
	dir := flag.String("dir", "downloads", "destination directory")
	name := flag.String("name", "", "output filename (default: from server or URL)")
	conns := flag.Int("c", 8, "connections per download")
	dbPath := flag.String("db", "", "SQLite state file (enables resume across runs)")
	resume := flag.String("resume", "", "resume the download with this ID")
	list := flag.Bool("list", false, "list recorded downloads and exit")
	retries := flag.Int("retries", 5, "maximum retries per request")
	allowPrivate := flag.Bool("allow-private", true, "allow loopback/private network destinations")
	verbose := flag.Bool("v", false, "debug logging")
	flag.Var(headers, "header", `extra request header "Name: value" (repeatable)`)
	flag.Parse()

	if (*resume == "" && !*list && flag.NArg() != 1) || ((*resume != "" || *list) && *dbPath == "") {
		fmt.Fprintln(os.Stderr, "usage: engine-test [flags] URL | -db FILE -resume ID | -db FILE -list")
		flag.PrintDefaults()
		return 2
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	var store domain.Store = memory.New()
	if *dbPath != "" {
		s, err := sqlite.Open(*dbPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open state:", err)
			return 1
		}
		defer s.Close()
		store = s
	}

	printer := newPrinter(os.Stderr)
	policy := engine.DefaultRetryPolicy()
	policy.MaxRetries = *retries
	mgr, err := engine.New(store, engine.Config{
		Connections: *conns, Retry: &policy, AllowPrivateNetworks: *allowPrivate,
		Publisher: printer, Logger: logger, DefaultDirectory: *dir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	svc := appdownload.New(mgr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// -list is read-only: it must not run recovery, which would mark
	// downloads another running process owns as interrupted.
	if *list {
		return listDownloads(ctx, svc)
	}
	if err = svc.Recover(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "recover:", err)
		return 1
	}

	id := *resume
	if id == "" {
		d, err := svc.Create(ctx, domain.CreateRequest{URL: flag.Arg(0), Directory: *dir, Filename: *name, Connections: *conns, Headers: headers})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		id = d.ID
		fmt.Fprintf(os.Stderr, "download %s -> %s\n", id, filepath.Join(d.Destination, d.Filename))
	}
	printer.watch(id)
	if err = svc.Start(ctx, id); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		return 1
	}

	select {
	case <-printer.done:
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "\ninterrupted: pausing and saving progress...")
		pauseCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := svc.Pause(pauseCtx, id); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
			fmt.Fprintln(os.Stderr, "pause:", err)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = svc.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown:", err)
	}
	d, err := svc.Get(context.Background(), id)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return report(d, *dbPath)
}

func report(d *domain.Download, dbPath string) int {
	fmt.Fprintln(os.Stderr)
	switch d.Status {
	case domain.Completed:
		path := filepath.Join(d.Destination, d.Filename)
		sum, err := fileSHA256(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "hash:", err)
			return 1
		}
		fmt.Printf("completed %s (%d bytes, %d retries)\nsha256 %s\n", path, d.Size, d.Retries, sum)
		return 0
	case domain.Paused:
		if dbPath != "" {
			fmt.Printf("paused at %d bytes; resume with: engine-test -db %s -resume %s\n", d.Downloaded, dbPath, d.ID)
		} else {
			fmt.Println("paused (no -db given, so progress is not resumable across runs)")
		}
		return 130
	default:
		fmt.Printf("%s: %s\n", strings.ToLower(string(d.Status)), d.Error)
		return 1
	}
}

func listDownloads(ctx context.Context, svc *appdownload.Service) int {
	items, err := svc.List(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, d := range items {
		fmt.Printf("%s  %-11s %10s / %-10s  %s\n    url: %s\n", d.ID, d.Status, humanBytes(d.Downloaded), humanBytes(d.Size), filepath.Join(d.Destination, d.Filename), d.URL)
		if d.Error != "" {
			fmt.Printf("    error: %s\n", d.Error)
		}
	}
	return 0
}

// printer renders throttled progress for one download.
type printer struct {
	mu        sync.Mutex
	out       io.Writer
	id        string
	done      chan struct{}
	closeOnce sync.Once
	lastPrint time.Time
}

func newPrinter(out io.Writer) *printer { return &printer{out: out, done: make(chan struct{})} }

func (p *printer) watch(id string) {
	p.mu.Lock()
	p.id = id
	p.mu.Unlock()
}

func (p *printer) Publish(e events.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.Download.ID != p.id {
		return
	}
	switch e.Type {
	case events.ProgressUpdated:
		if time.Since(p.lastPrint) < 500*time.Millisecond {
			return
		}
		p.lastPrint = time.Now()
		pr := e.Progress
		pct, eta := "  ?  ", "?"
		if pr.Percent >= 0 {
			pct = fmt.Sprintf("%5.1f", pr.Percent)
		}
		if pr.ETASeconds >= 0 {
			eta = (time.Duration(pr.ETASeconds) * time.Second).String()
		}
		fmt.Fprintf(p.out, "\r[%s%%] %s / %s  %s/s  avg %s/s  ETA %s  conns %d   ",
			pct, humanBytes(pr.Downloaded), humanBytes(pr.Total), humanBytes(int64(pr.Speed)), humanBytes(int64(pr.AverageSpeed)), eta, pr.ActiveConnections)
	case events.DownloadChanged:
		switch e.Download.Status {
		case domain.Completed, domain.Failed, domain.Cancelled, domain.Paused:
			p.closeOnce.Do(func() { close(p.done) })
		}
	}
}

func humanBytes(n int64) string {
	if n < 0 {
		return "?"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
