// Ported from Mister Download Manager, crates/cli/src/lib.rs (get, resume)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Downloads with the engine, without the app: get, resume, list. They are
// kept in <data>/cli/downloads.json, held by one command line at a time
// (never the app's list). This replaces cmd/engine-test.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appdownload "github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/application/events"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
)

// session is the engine on the command line's own list.
type session struct {
	svc     *appdownload.Service
	printer *printer
	close   func()
}

// openSession opens the command line's list (one command line at a time)
// with an engine of conns connections.
func openSession(conns int) (*session, error) {
	dir, err := cliDir()
	if err != nil {
		return nil, err
	}
	lock, err := ipc.Lock(dir)
	if err != nil {
		return nil, fmt.Errorf("another %s download is running (%v)", name, err)
	}
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	p := newPrinter()
	mgr, err := engine.New(store, engine.Config{Connections: conns, AllowPrivateNetworks: true, Publisher: p})
	if err != nil {
		store.Close()
		lock.Close()
		return nil, err
	}
	return &session{svc: appdownload.New(mgr), printer: p, close: func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
		store.Close()
		lock.Close()
	}}, nil
}

func cmdGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	out := fs.String("o", ".", "folder to save into")
	fs.StringVar(out, "output", ".", "folder to save into")
	fileName := fs.String("name", "", "file name (default: from the server or the address)")
	conns := fs.Int("c", 8, "connections (1 to 16)")
	fs.IntVar(conns, "connections", 8, "connections")
	checksum := fs.String("checksum", "", "the finished file's hash (hex MD5, SHA-1, SHA-256, SHA-512, CRC32; or sha256:…)")
	var headers listFlag
	fs.Var(&headers, "H", `extra request header "Name: value" (repeatable)`)
	fs.Var(&headers, "header", "extra request header")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErr("one address")
	}
	if *conns < 1 || *conns > 16 {
		return usageErr("connections must be between 1 and 16")
	}
	h := map[string]string{}
	for _, raw := range headers {
		n, v, ok := strings.Cut(raw, ":")
		if !ok || strings.TrimSpace(n) == "" {
			return usageErr(`a header is "Name: value"`)
		}
		h[strings.TrimSpace(n)] = strings.TrimSpace(v)
	}
	dir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	s, err := openSession(*conns)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.svc.Recover(ctx); err != nil {
		return err
	}
	d, err := s.svc.Create(ctx, domain.CreateRequest{URL: rest[0], Directory: dir, Filename: *fileName,
		Connections: *conns, Headers: h, Checksum: *checksum})
	if err != nil {
		return err
	}
	if !jsonOut {
		fmt.Fprintf(stderr, "download %s -> %s\n", d.ID, filepath.Join(d.Destination, d.Filename))
	}
	return s.follow(ctx, d.ID)
}

func cmdResume(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	conns := fs.Int("c", 8, "connections (1 to 16)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErr("one id (list shows them)")
	}
	s, err := openSession(*conns)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.svc.Recover(ctx); err != nil {
		return err
	}
	d, err := s.svc.Get(ctx, rest[0])
	if err != nil {
		return err
	}
	if d.Status == domain.Completed {
		return errors.New("that download is complete")
	}
	return s.follow(ctx, d.ID)
}

// follow starts download id and shows its progress until it ends, or
// pauses it on Ctrl+C.
func (s *session) follow(ctx context.Context, id string) error {
	s.printer.watch(id)
	d, err := s.svc.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.Status == domain.Failed {
		err = s.svc.Retry(ctx, id)
	} else {
		err = s.svc.Start(ctx, id)
	}
	if err != nil {
		return err
	}
	select {
	case <-s.printer.done:
	case <-ctx.Done():
		if !jsonOut {
			fmt.Fprintln(stderr, "\ninterrupted: pausing and saving progress...")
		}
		pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.svc.Pause(pctx, id); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
			return err
		}
		<-s.printer.done
	}
	d, err = s.svc.Get(context.Background(), id)
	if err != nil {
		return err
	}
	return report(d)
}

func report(d *domain.Download) error {
	path := filepath.Join(d.Destination, d.Filename)
	switch d.Status {
	case domain.Completed:
		sum, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if jsonOut {
			emit(map[string]any{"event": "done", "id": d.ID, "path": path, "size": d.Size, "sha256": sum})
		} else {
			fmt.Fprintf(stderr, "\n")
			fmt.Fprintf(stdout, "completed %s (%d bytes)\nsha256 %s\n", path, d.Size, sum)
		}
		return nil
	case domain.Paused:
		if jsonOut {
			emit(map[string]any{"event": "paused", "id": d.ID, "downloaded": d.Downloaded})
		} else {
			fmt.Fprintf(stdout, "\npaused at %d bytes; continue with: %s resume %s\n", d.Downloaded, name, d.ID)
		}
		return errReported
	}
	if jsonOut {
		emit(map[string]any{"event": "failed", "id": d.ID, "error": d.Error})
		return errReported
	}
	return fmt.Errorf("%s: %s (continue with: %s resume %s)", strings.ToLower(string(d.Status)), d.Error, name, d.ID)
}

func cmdList(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return usageErr("no arguments")
	}
	dir, err := cliDir()
	if err != nil {
		return err
	}
	// Read only: no recovery (a running get owns its downloads).
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		return err
	}
	defer store.Close()
	items, err := store.List(ctx)
	if err != nil {
		return err
	}
	for _, d := range items {
		if jsonOut {
			emit(map[string]any{"id": d.ID, "status": d.Status, "file": filepath.Join(d.Destination, d.Filename),
				"url": d.URL, "size": d.Size, "downloaded": d.Downloaded, "error": d.Error})
			continue
		}
		fmt.Fprintf(stdout, "%s  %-11s %10s / %-10s  %s\n    url: %s\n", d.ID, d.Status, humanBytes(d.Downloaded),
			humanBytes(d.Size), filepath.Join(d.Destination, d.Filename), d.URL)
		if d.Error != "" {
			fmt.Fprintf(stdout, "    error: %s\n", d.Error)
		}
	}
	return nil
}

// printer shows one download's progress (a line for people, JSON events
// for programs).
type printer struct {
	mu        sync.Mutex
	id        string
	done      chan struct{}
	closeOnce sync.Once
	last      time.Time
}

func newPrinter() *printer { return &printer{done: make(chan struct{})} }

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
		if time.Since(p.last) < 500*time.Millisecond {
			return
		}
		p.last = time.Now()
		pr := e.Progress
		if jsonOut {
			emit(map[string]any{"event": "progress", "downloaded": pr.Downloaded, "total": pr.Total,
				"speed": int64(pr.Speed), "connections": pr.ActiveConnections})
			return
		}
		pct, eta := "  ?  ", "?"
		if pr.Percent >= 0 {
			pct = fmt.Sprintf("%5.1f", pr.Percent)
		}
		if pr.ETASeconds >= 0 {
			eta = (time.Duration(pr.ETASeconds) * time.Second).String()
		}
		fmt.Fprintf(stderr, "\r[%s%%] %s / %s  %s/s  ETA %s  conns %d   ", pct, humanBytes(pr.Downloaded),
			humanBytes(pr.Total), humanBytes(int64(pr.Speed)), eta, pr.ActiveConnections)
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
