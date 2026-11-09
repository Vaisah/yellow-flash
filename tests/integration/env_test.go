// Package integration exercises the service, engine and SQLite adapter
// together against misbehaving HTTP servers.
package integration

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	appdownload "github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/application/events"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/infrastructure/persistence/sqlite"
)

// The engine must satisfy the application port.
var _ appdownload.DownloadPort = (*engine.Manager)(nil)

// recorder captures events for assertions.
type recorder struct {
	mu        sync.Mutex
	progress  int
	changed   int
	maxActive int
	leaked    bool
}

func (r *recorder) Publish(e events.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e.Type {
	case events.ProgressUpdated:
		r.progress++
		r.maxActive = max(r.maxActive, e.Progress.ActiveConnections)
	case events.DownloadChanged:
		r.changed++
	}
	if len(e.Download.Headers) != 0 {
		r.leaked = true
	}
}

func (r *recorder) stats() (progress, maxActive int, leaked bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.progress, r.maxActive, r.leaked
}

type env struct {
	t      *testing.T
	dir    string
	dbPath string
	store  *sqlite.Store
	mgr    *engine.Manager
	svc    *appdownload.Service
	events *recorder
	client *http.Client
	cfg    engine.Config
}

func fastRetry(maxRetries int) *engine.RetryPolicy {
	return &engine.RetryPolicy{MaxRetries: maxRetries, BaseDelay: 5 * time.Millisecond, MaxDelay: 50 * time.Millisecond, Multiplier: 2, Jitter: 0.1, MaxRetryAfter: 5 * time.Second}
}

func newEnv(t *testing.T, tweak func(*engine.Config)) *env {
	t.Helper()
	e := &env{t: t, dir: filepath.Join(t.TempDir(), "downloads"), dbPath: filepath.Join(t.TempDir(), "state.db"), events: &recorder{}}
	e.client = network.NewClient(network.Options{AllowPrivate: true})
	e.cfg = engine.Config{
		Connections: 4, MinSegmentSize: 16 << 10, Retry: fastRetry(4), Client: e.client, Publisher: e.events,
		ProgressInterval: 20 * time.Millisecond, PersistInterval: 50 * time.Millisecond, ReadIdleTimeout: 5 * time.Second,
	}
	if tweak != nil {
		tweak(&e.cfg)
	}
	e.open()
	t.Cleanup(e.close)
	return e
}

func (e *env) open() {
	e.t.Helper()
	store, err := sqlite.Open(e.dbPath)
	if err != nil {
		e.t.Fatal(err)
	}
	cfg := e.cfg
	cfg.HostLimits = store // as the app does: learned limits survive restarts
	mgr, err := engine.New(store, cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.store, e.mgr, e.svc = store, mgr, appdownload.New(mgr)
	if err = e.svc.Recover(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) close() {
	if e.mgr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.svc.Shutdown(ctx); err != nil {
		e.t.Errorf("shutdown: %v", err)
	}
	_ = e.store.Close()
	e.client.CloseIdleConnections()
	e.mgr = nil
}

// restart simulates an orderly application restart.
func (e *env) restart() {
	e.t.Helper()
	e.close()
	e.open()
}

func (e *env) add(url, name string, conns int) string {
	e.t.Helper()
	d, err := e.svc.Add(context.Background(), domain.CreateRequest{URL: url, Directory: e.dir, Filename: name, Connections: conns})
	if err != nil {
		e.t.Fatalf("add %s: %v", url, err)
	}
	return d.ID
}

func (e *env) get(id string) *domain.Download {
	e.t.Helper()
	d, err := e.svc.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

// waitFor polls until cond holds.
func (e *env) waitFor(id string, what string, cond func(*domain.Download) bool) *domain.Download {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if d := e.get(id); cond(d) {
			return d
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s: %+v", what, e.get(id))
	return nil
}

// waitRest waits for a resting state and returns it.
func (e *env) waitRest(id string) *domain.Download {
	e.t.Helper()
	return e.waitFor(id, "resting state", func(d *domain.Download) bool {
		return d.Status == domain.Completed || d.Status == domain.Failed || d.Status == domain.Paused || d.Status == domain.Cancelled
	})
}

func (e *env) waitCompleted(id string) *domain.Download {
	e.t.Helper()
	d := e.waitRest(id)
	if d.Status != domain.Completed {
		e.t.Fatalf("download %s ended %s: %s", id, d.Status, d.Error)
	}
	return d
}

func (e *env) waitFailed(id string) *domain.Download {
	e.t.Helper()
	d := e.waitRest(id)
	if d.Status != domain.Failed {
		e.t.Fatalf("download %s ended %s, want FAILED", id, d.Status)
	}
	return d
}

// assertFile checks the published file byte-for-byte and its size.
func (e *env) assertFile(d *domain.Download, want []byte) {
	e.t.Helper()
	got, err := os.ReadFile(filepath.Join(d.Destination, d.Filename))
	if err != nil {
		e.t.Fatal(err)
	}
	if int64(len(got)) != int64(len(want)) || d.Size != int64(len(want)) || d.Downloaded != d.Size {
		e.t.Fatalf("size: file=%d want=%d record size=%d downloaded=%d", len(got), len(want), d.Size, d.Downloaded)
	}
	if !bytes.Equal(got, want) {
		e.t.Fatalf("content mismatch: sha256 %s, want %s", digest(got), digest(want))
	}
	if _, err := os.Stat(d.TempPath); !os.IsNotExist(err) {
		e.t.Fatalf("temporary file %s left behind", d.TempPath)
	}
}

func (e *env) persisted(id string) *domain.Download {
	e.t.Helper()
	d, err := e.store.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}
