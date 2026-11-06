package integration

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"sync/atomic"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/infrastructure/persistence/sqlite"
)

// 1. Successful normal download (single connection) with integrity.
func TestNormalDownload(t *testing.T) {
	data := payload(300<<10, 1)
	fs := newFileServer(t, data, nil)
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/normal.bin"), "normal.bin", 1))
	e.assertFile(d, data)
	if !d.RangeSupported || d.CompletedAt.IsZero() || d.StartedAt.IsZero() || d.Error != "" {
		t.Fatalf("record: %+v", d)
	}
	if p := e.persisted(d.ID); p.Status != domain.Completed || p.Downloaded != int64(len(data)) {
		t.Fatalf("persisted: %+v", p)
	}
}

// 2. Segmented download over several concurrent connections, with throttled
// progress events and live connection counts.
func TestSegmentedDownload(t *testing.T) {
	data := payload(3<<20, 2)
	fs := newFileServer(t, data, func(fs *fileServer) { fs.chunk, fs.delay = 16<<10, 2*time.Millisecond })
	e := newEnv(t, func(c *engine.Config) { c.Connections = 8 })
	d := e.waitCompleted(e.add(fs.url("/files/seg.bin"), "seg.bin", 8))
	e.assertFile(d, data)
	if got := fs.rangeRequests.Load(); got < 9 {
		t.Fatalf("expected probe + 8 range requests, got %d", got)
	}
	progress, maxActive, leaked := e.events.stats()
	chunks := len(data) / (16 << 10)
	if progress == 0 || progress >= chunks || maxActive < 2 || leaked {
		t.Fatalf("progress events=%d (chunks %d) maxActive=%d leaked=%v", progress, chunks, maxActive, leaked)
	}
}

// 3. Server without range support falls back to one stream.
func TestServerWithoutRangeSupport(t *testing.T) {
	data := payload(500<<10, 3)
	fs := newFileServer(t, data, func(fs *fileServer) { fs.noRange = true })
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/norange.bin"), "norange.bin", 8))
	e.assertFile(d, data)
	if d.RangeSupported {
		t.Fatal("range support must not be inferred")
	}
}

// 4. HEAD unsupported: the GET probe still discovers everything.
func TestHEADUnsupported(t *testing.T) {
	data := payload(400<<10, 4)
	fs := newFileServer(t, data, func(fs *fileServer) { fs.noHEAD = true })
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/nohead.bin"), "nohead.bin", 4))
	e.assertFile(d, data)
	if !d.RangeSupported {
		t.Fatal("range support should be verified by GET")
	}
}

// 5. Invalid Content-Range on segment responses fails without retry loops.
func TestInvalidContentRange(t *testing.T) {
	fs := newFileServer(t, payload(200<<10, 5), func(fs *fileServer) { fs.badContentRange = true })
	e := newEnv(t, nil)
	d := e.waitFailed(e.add(fs.url("/files/badrange.bin"), "badrange.bin", 4))
	if !strings.Contains(d.Error, "Content-Range") {
		t.Fatalf("error: %s", d.Error)
	}
	if _, err := os.Stat(filepath.Join(d.Destination, d.Filename)); !os.IsNotExist(err) {
		t.Fatal("failed download must not be published")
	}
}

// 6. Server answering 200 to segment range requests (after a valid probe)
// is detected and the download completes as a single stream.
func TestRangeRequestAnsweredWith200(t *testing.T) {
	data := payload(600<<10, 6)
	fs := newFileServer(t, data, func(fs *fileServer) { fs.probeRangeOnly = true })
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/ignore.bin"), "ignore.bin", 4))
	e.assertFile(d, data)
	if d.RangeSupported {
		t.Fatal("download should have fallen back to a single stream")
	}
}

// 7. HTTP 416 is permanent; an empty resource still completes.
func TestHTTP416(t *testing.T) {
	fs := newFileServer(t, payload(1000, 7), func(fs *fileServer) {
		fs.fail = func(r *http.Request, _ int64) (int, string) {
			if r.Method == http.MethodGet {
				return http.StatusRequestedRangeNotSatisfiable, ""
			}
			return 0, ""
		}
	})
	e := newEnv(t, nil)
	d := e.waitFailed(e.add(fs.url("/files/416.bin"), "416.bin", 4))
	if !strings.Contains(d.Error, "416") || d.Retries != 0 {
		t.Fatalf("error=%q retries=%d", d.Error, d.Retries)
	}
	empty := newFileServer(t, []byte{}, nil)
	d = e.waitCompleted(e.add(empty.url("/files/empty.bin"), "empty.bin", 4))
	e.assertFile(d, []byte{})
}

// 8. HTTP 404 fails immediately without retries.
func TestHTTP404(t *testing.T) {
	fs := newFileServer(t, nil, func(fs *fileServer) {
		fs.fail = func(*http.Request, int64) (int, string) { return http.StatusNotFound, "" }
	})
	e := newEnv(t, nil)
	d := e.waitFailed(e.add(fs.url("/files/missing.bin"), "missing.bin", 4))
	if !strings.Contains(d.Error, "404") || d.Retries != 0 {
		t.Fatalf("error=%q retries=%d", d.Error, d.Retries)
	}
	if fs.requests.Load() != 2 {
		t.Fatalf("404 retried: %d requests", fs.requests.Load())
	}
}

// 9. HTTP 429 with Retry-After is honoured before retrying.
func TestHTTP429RetryAfter(t *testing.T) {
	data := payload(200<<10, 9)
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.fail = func(_ *http.Request, n int64) (int, string) {
			if n <= 2 {
				return http.StatusTooManyRequests, "1"
			}
			return 0, ""
		}
	})
	e := newEnv(t, nil)
	start := time.Now()
	d := e.waitCompleted(e.add(fs.url("/files/429.bin"), "429.bin", 2))
	e.assertFile(d, data)
	if elapsed := time.Since(start); elapsed < time.Second || d.Retries < 1 {
		t.Fatalf("Retry-After not honoured: elapsed=%v retries=%d", elapsed, d.Retries)
	}
}

// 10. HTTP 503: transient failures recover, persistent ones exhaust retries.
func TestHTTP503(t *testing.T) {
	data := payload(300<<10, 10)
	var failures atomic.Int32
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.fail = func(r *http.Request, _ int64) (int, string) {
			if rangeStart(r) > 0 && failures.Add(1) <= 3 {
				return http.StatusServiceUnavailable, ""
			}
			return 0, ""
		}
	})
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/503.bin"), "503.bin", 4))
	e.assertFile(d, data)
	if d.Retries < 3 {
		t.Fatalf("retries=%d", d.Retries)
	}
	down := newFileServer(t, nil, func(fs *fileServer) {
		fs.fail = func(*http.Request, int64) (int, string) { return http.StatusServiceUnavailable, "" }
	})
	d = e.waitFailed(e.add(down.url("/files/down.bin"), "down.bin", 4))
	if !strings.Contains(d.Error, "503") || !strings.Contains(d.Error, "gave up after 4 retries") || d.Retries != 4 {
		t.Fatalf("error=%q retries=%d", d.Error, d.Retries)
	}
}

// 11. Connection resets (real TCP RST) are retried, resuming segments from
// their written offset; a non-range stream restarts from zero.
func TestConnectionReset(t *testing.T) {
	data := payload(1<<20, 11)
	var resets atomic.Int32
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.chunk = 8 << 10
		// Reset the first request of each of the four segments mid-body.
		fs.resetAt = func(r *http.Request, _ int64) int64 {
			if rh := r.Header.Get("Range"); rh != "" && rh != "bytes=0-0" && resets.Add(1) <= 4 {
				return 100 << 10
			}
			return -1
		}
	})
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/reset.bin"), "reset.bin", 4))
	e.assertFile(d, data)
	// Each reset forces a re-request: 1 probe + 4 segments + 4 re-requests.
	// Most are engine retries; a reset that lands before the response
	// headers are read is re-sent transparently by net/http instead.
	if got := fs.rangeRequests.Load(); got < 9 || d.Retries < 1 || resets.Load() < 4 {
		t.Fatalf("range requests=%d engine retries=%d resets=%d", got, d.Retries, resets.Load())
	}
	if served := fs.bytesServed.Load(); served > int64(len(data))*3/2 {
		t.Fatalf("segments restarted from scratch: served %d for %d", served, len(data))
	}

	var streamReset atomic.Bool
	stream := newFileServer(t, data, func(fs *fileServer) {
		fs.noRange = true
		fs.resetAt = func(r *http.Request, _ int64) int64 {
			if r.Header.Get("Range") == "" && streamReset.CompareAndSwap(false, true) {
				return 300 << 10
			}
			return -1
		}
	})
	d = e.waitCompleted(e.add(stream.url("/files/reset-stream.bin"), "reset-stream.bin", 1))
	e.assertFile(d, data)
	if d.Retries < 1 {
		t.Fatal("stream reset was not retried")
	}
}

// 11b. A connection that stalls mid-body is abandoned after the read idle
// timeout and retried from the bytes already written.
func TestStalledConnectionTimesOut(t *testing.T) {
	data := payload(256<<10, 111)
	var stalled atomic.Bool
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.chunk = 16 << 10
		var chunks sync.Map
		fs.gate = func(r *http.Request, n int64) {
			if r.Header.Get("Range") == "bytes=0-0" {
				return
			}
			c, _ := chunks.LoadOrStore(n, new(atomic.Int32))
			if c.(*atomic.Int32).Add(1) == 2 && stalled.CompareAndSwap(false, true) {
				<-r.Context().Done() // never send another byte
			}
		}
	})
	e := newEnv(t, func(c *engine.Config) { c.ReadIdleTimeout = 300 * time.Millisecond })
	start := time.Now()
	d := e.waitCompleted(e.add(fs.url("/files/stall.bin"), "stall.bin", 1))
	e.assertFile(d, data)
	if !stalled.Load() || d.Retries < 1 || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("stall=%v retries=%d elapsed=%v", stalled.Load(), d.Retries, time.Since(start))
	}
	if served := fs.bytesServed.Load(); served > int64(len(data))+32<<10 {
		t.Fatalf("stalled request restarted from zero: served %d", served)
	}
}

// 12. Redirect chains (301/302/307/308) are followed and recorded; unsafe
// redirect targets are refused.
func TestRedirects(t *testing.T) {
	data := payload(100<<10, 12)
	fs := newFileServer(t, data, nil)
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/hop/4"), "hopped.bin", 2))
	e.assertFile(d, data)
	if d.FinalURL != fs.url("/files/hopped.bin") {
		t.Fatalf("final URL %q", d.FinalURL)
	}
	d = e.waitFailed(e.add(fs.url("/to-ftp"), "ftp.bin", 2))
	if !strings.Contains(d.Error, "redirect rejected") {
		t.Fatalf("error: %s", d.Error)
	}
}

// 13. Unknown Content-Length streams to EOF and records the real size.
func TestUnknownContentLength(t *testing.T) {
	data := payload(700<<10, 13)
	fs := newFileServer(t, data, func(fs *fileServer) { fs.unknownLength = true })
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/chunked.bin"), "chunked.bin", 4))
	e.assertFile(d, data)
}

func slowServer(t *testing.T, data []byte, configure func(*fileServer)) *fileServer {
	return newFileServer(t, data, func(fs *fileServer) {
		fs.chunk, fs.delay = 16<<10, 4*time.Millisecond
		if configure != nil {
			configure(fs)
		}
	})
}

func (e *env) waitProgress(id string, size int) {
	e.t.Helper()
	e.waitFor(id, "progress", func(d *domain.Download) bool {
		return d.Downloaded > int64(size)/4 || d.Status.IsTerminal() || d.Status == domain.Failed
	})
}

// 14. Pause persists durable progress; resume continues instead of restarting.
func TestPauseAndResume(t *testing.T) {
	data := payload(4<<20, 14)
	fs := slowServer(t, data, nil)
	e := newEnv(t, nil)
	ctx := context.Background()
	id := e.add(fs.url("/files/pause.bin"), "pause.bin", 4)
	e.waitProgress(id, len(data))
	if err := e.svc.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	d := e.get(id)
	p := e.persisted(id)
	if d.Status != domain.Paused || p.Status != domain.Paused || p.Downloaded == 0 || p.Downloaded >= int64(len(data)) {
		t.Fatalf("paused state: live=%+v persisted=%+v", d, p)
	}
	segs, err := e.store.ListSegments(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, s := range segs {
		sum += s.Downloaded
	}
	if sum != p.Downloaded {
		t.Fatalf("segments %d != downloaded %d", sum, p.Downloaded)
	}
	if err = e.svc.Pause(ctx, id); err != nil {
		t.Fatalf("pause must be idempotent: %v", err)
	}
	if err = e.svc.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	d = e.waitCompleted(id)
	e.assertFile(d, data)
	if served := fs.bytesServed.Load(); served > int64(len(data))*3/2 {
		t.Fatalf("resume re-downloaded too much: %d of %d", served, len(data))
	}
}

// 15. A changed ETag on resume restarts from zero instead of mixing bytes.
func TestRemoteETagChange(t *testing.T) {
	oldData, newData := payload(2<<20, 15), payload(2<<20, 151)
	fs := slowServer(t, oldData, nil)
	e := newEnv(t, nil)
	ctx := context.Background()
	id := e.add(fs.url("/files/etag.bin"), "etag.bin", 4)
	e.waitProgress(id, len(oldData))
	if err := e.svc.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	fs.swap(newData)
	if err := e.svc.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	d := e.waitCompleted(id)
	e.assertFile(d, newData)
}

// 16. A failing worker is retried without disturbing the others.
func TestWorkerFailureAndRetry(t *testing.T) {
	data := payload(1<<20, 16)
	var failures atomic.Int32
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.fail = func(r *http.Request, _ int64) (int, string) {
			if rangeStart(r) == 256<<10 && failures.Add(1) <= 2 {
				return http.StatusInternalServerError, ""
			}
			return 0, ""
		}
	})
	e := newEnv(t, nil)
	d := e.waitCompleted(e.add(fs.url("/files/worker.bin"), "worker.bin", 4))
	e.assertFile(d, data)
	if d.Retries < 2 || failures.Load() < 3 {
		t.Fatalf("retries=%d failures=%d", d.Retries, failures.Load())
	}
}

// 17. Many downloads run concurrently without interfering.
func TestConcurrentDownloads(t *testing.T) {
	e := newEnv(t, nil)
	type job struct {
		id   string
		data []byte
	}
	var jobs []job
	for i := range 6 {
		data := payload((200+i*50)<<10, uint64(170+i))
		fs := newFileServer(t, data, func(fs *fileServer) { fs.delay = time.Millisecond })
		jobs = append(jobs, job{e.add(fs.url("/files/c.bin"), "same-name.bin", 3), data})
	}
	names := map[string]bool{}
	for _, j := range jobs {
		d := e.waitCompleted(j.id)
		e.assertFile(d, j.data)
		if names[d.Filename] {
			t.Fatalf("duplicate filename %s", d.Filename)
		}
		names[d.Filename] = true
	}
}

// 18. Shutdown during a transfer persists progress; after restart the
// download resumes and completes.
func TestShutdownDuringDownload(t *testing.T) {
	data := payload(3<<20, 18)
	fs := slowServer(t, data, nil)
	e := newEnv(t, nil)
	id := e.add(fs.url("/files/shutdown.bin"), "shutdown.bin", 4)
	e.waitProgress(id, len(data))
	e.restart()
	d := e.get(id)
	if d.Status != domain.Paused || d.Downloaded == 0 {
		t.Fatalf("after restart: %+v", d)
	}
	if _, err := e.svc.Create(context.Background(), domain.CreateRequest{URL: fs.url("/x")}); err != nil {
		t.Fatalf("restarted manager must accept work: %v", err)
	}
	if err := e.svc.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.assertFile(e.waitCompleted(id), data)
	if served := fs.bytesServed.Load(); served > int64(len(data))*3/2 {
		t.Fatalf("restart re-downloaded too much: %d of %d", served, len(data))
	}
}

// 19a. Crash recovery: the first process loses its database mid-transfer
// and never shuts down. A second process recovers the DOWNLOADING record,
// reconciles it with the temporary file and finishes without starting over.
func TestCrashRecovery(t *testing.T) {
	data := payload(3<<20, 19)
	var gate freezeGate
	fs := slowServer(t, data, func(fs *fileServer) {
		fs.gate = func(r *http.Request, n int64) { gate.wait(r.Context(), n) }
	})
	crashed := newEnv(t, func(c *engine.Config) { c.Retry = fastRetry(0) })
	id := crashed.add(fs.url("/files/crash.bin"), "crash.bin", 4)
	crashed.waitFor(id, "durable progress", func(*domain.Download) bool {
		return crashed.persisted(id).Downloaded > int64(len(data))/4
	})
	gate.freezeUpTo(fs.requests.Load()) // in-flight transfers stall forever
	_ = crashed.store.Close()           // the process can no longer persist anything
	before := fs.bytesServed.Load()

	store, err := sqlite.Open(crashed.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.Get(context.Background(), id)
	if err != nil || raw.Status != domain.Downloading {
		t.Fatalf("expected an interrupted DOWNLOADING record: %+v %v", raw, err)
	}
	cfg := crashed.cfg
	cfg.Retry = fastRetry(4)
	mgr, err := engine.New(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
		_ = store.Close()
	})
	if err = mgr.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, _ := mgr.Get(context.Background(), id)
	if rec.Status != domain.Paused || rec.Downloaded == 0 || rec.Downloaded > raw.Downloaded {
		t.Fatalf("recovered: %+v (persisted %d)", rec, raw.Downloaded)
	}
	if err = mgr.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		d, _ := mgr.Get(context.Background(), id)
		if d.Status == domain.Completed {
			crashed.assertFile(d, data)
			break
		}
		if d.Status == domain.Failed || time.Now().After(deadline) {
			t.Fatalf("recovery run ended %s: %s", d.Status, d.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if after := fs.bytesServed.Load() - before; after >= int64(len(data)) {
		t.Fatalf("recovery re-downloaded everything: %d", after)
	}
}

// 19b. Recovery clamps segment records that claim more than the temporary
// file holds (e.g. a crash before the OS flushed data).
func TestCrashRecoveryClampsToFile(t *testing.T) {
	data := payload(1<<20, 191)
	fs := newFileServer(t, data, nil)
	e := newEnv(t, nil)
	ctx := context.Background()
	created, err := e.svc.Create(ctx, domain.CreateRequest{URL: fs.url("/files/clamp.bin"), Directory: e.dir, Filename: "clamp.bin", Connections: 2})
	if err != nil {
		t.Fatal(err)
	}
	e.close()
	half := int64(len(data) / 2)
	onDisk := half + half/4
	if err = os.MkdirAll(e.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(created.TempPath, data[:onDisk], 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created.Status, created.Size, created.RangeSupported = domain.Downloading, int64(len(data)), true
	created.ETag, created.Downloaded = fs.etag, int64(len(data))
	if err = store.Save(ctx, created); err != nil {
		t.Fatal(err)
	}
	err = store.SaveSegments(ctx, created.ID, []domain.Segment{
		{ID: created.ID + "-0", DownloadID: created.ID, Index: 0, Start: 0, End: half - 1, Downloaded: half, Status: domain.SegmentCompleted},
		{ID: created.ID + "-1", DownloadID: created.ID, Index: 1, Start: half, End: int64(len(data)) - 1, Downloaded: half, Status: domain.SegmentCompleted},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	e.open()
	if d := e.get(created.ID); d.Status != domain.Paused || d.Downloaded != onDisk {
		t.Fatalf("recovered %+v, want downloaded %d", d, onDisk)
	}
	if err = e.svc.Resume(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	e.assertFile(e.waitCompleted(created.ID), data)
	if served := fs.bytesServed.Load(); served > int64(len(data))-onDisk+1 {
		t.Fatalf("fetched %d bytes, only %d were missing", served, int64(len(data))-onDisk)
	}
}

// 20. Integrity: advertised digests are verified; corrupt data is never
// published and is discarded.
func TestIntegrityVerification(t *testing.T) {
	data := payload(512<<10, 20)
	good := newFileServer(t, data, func(fs *fileServer) { fs.reprDigest = reprDigest(data) })
	e := newEnv(t, nil)
	e.assertFile(e.waitCompleted(e.add(good.url("/files/good.bin"), "good.bin", 4)), data)

	bad := newFileServer(t, data, func(fs *fileServer) { fs.reprDigest = reprDigest([]byte("other")) })
	d := e.waitFailed(e.add(bad.url("/files/bad.bin"), "bad.bin", 4))
	if !strings.Contains(d.Error, "integrity") {
		t.Fatalf("error: %s", d.Error)
	}
	for _, p := range []string{filepath.Join(d.Destination, d.Filename), d.TempPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s must not exist after an integrity failure", p)
		}
	}
}

func TestDestinationConflictAutoRenames(t *testing.T) {
	data := payload(600<<10, 21)
	fs := slowServer(t, data, nil)
	e := newEnv(t, nil)
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(e.dir, "report.bin")
	if err := os.WriteFile(existing, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := e.waitCompleted(e.add(fs.url("/files/report.bin"), "report.bin", 2))
	if d.Filename != "report (1).bin" {
		t.Fatalf("filename %q", d.Filename)
	}
	e.assertFile(d, data)
	// A file appearing while downloading is not overwritten either.
	id := e.add(fs.url("/files/late.bin"), "late.bin", 2)
	if err := os.WriteFile(filepath.Join(e.dir, "late.bin"), []byte("appeared"), 0o644); err != nil {
		t.Fatal(err)
	}
	d = e.waitCompleted(id)
	if d.Filename != "late (1).bin" {
		t.Fatalf("late filename %q", d.Filename)
	}
	for path, want := range map[string]string{existing: "user data", filepath.Join(e.dir, "late.bin"): "appeared"} {
		if b, _ := os.ReadFile(path); string(b) != want {
			t.Fatalf("%s was overwritten", path)
		}
	}
}

func TestCancelDiscardsPartialData(t *testing.T) {
	data := payload(3<<20, 22)
	fs := slowServer(t, data, nil)
	e := newEnv(t, nil)
	ctx := context.Background()
	id := e.add(fs.url("/files/cancel.bin"), "cancel.bin", 4)
	e.waitProgress(id, len(data))
	if err := e.svc.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	d := e.get(id)
	if d.Status != domain.Cancelled || e.persisted(id).Status != domain.Cancelled {
		t.Fatalf("status %s", d.Status)
	}
	if _, err := os.Stat(d.TempPath); !os.IsNotExist(err) {
		t.Fatal("temporary file kept after cancel")
	}
	if segs, _ := e.store.ListSegments(ctx, id); len(segs) != 0 {
		t.Fatal("segments kept after cancel")
	}
	if err := e.svc.Resume(ctx, id); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("cancelled download resumed: %v", err)
	}
}

func TestRetryAndDeleteKeepCompletedFile(t *testing.T) {
	data := payload(100<<10, 23)
	var broken atomic.Bool
	broken.Store(true)
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.fail = func(*http.Request, int64) (int, string) {
			if broken.Load() {
				return http.StatusForbidden, ""
			}
			return 0, ""
		}
	})
	e := newEnv(t, nil)
	ctx := context.Background()
	id := e.add(fs.url("/files/retry.bin"), "retry.bin", 2)
	e.waitFailed(id)
	broken.Store(false)
	if err := e.svc.Retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	d := e.waitCompleted(id)
	e.assertFile(d, data)
	if err := e.svc.Retry(ctx, id); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("completed download retried: %v", err)
	}
	if err := e.svc.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("record survived delete: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(d.Destination, d.Filename)); err != nil || len(b) != len(data) {
		t.Fatal("delete removed the completed file")
	}
}

func TestContentDispositionAndCredentials(t *testing.T) {
	data := payload(64<<10, 24)
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.disposition = `attachment; filename="../quarterly report.pdf"`
		fs.requireAuth = "Bearer t0k3n"
	})
	e := newEnv(t, nil)
	ctx := context.Background()
	d, err := e.svc.Add(ctx, domain.CreateRequest{URL: fs.url("/download?id=7"), Directory: e.dir,
		Headers: map[string]string{"Authorization": "Bearer t0k3n", "Referer": "https://example.test/"}})
	if err != nil {
		t.Fatal(err)
	}
	d = e.waitCompleted(d.ID)
	if d.Filename != "quarterly report.pdf" || filepath.Dir(filepath.Join(d.Destination, d.Filename)) != d.Destination {
		t.Fatalf("filename %q", d.Filename)
	}
	e.assertFile(d, data)
	p := e.persisted(d.ID)
	if _, ok := p.Headers["Authorization"]; ok || p.Headers["Referer"] == "" {
		t.Fatalf("persisted headers: %v", p.Headers)
	}
	if _, _, leaked := e.events.stats(); leaked {
		t.Fatal("headers leaked into events")
	}
}

func TestPrivateAddressesBlockedByDefault(t *testing.T) {
	fs := newFileServer(t, payload(10, 25), nil)
	e := newEnv(t, func(c *engine.Config) { c.Client, c.AllowPrivateNetworks = nil, false })
	d := e.waitFailed(e.add(fs.url("/files/ssrf.bin"), "ssrf.bin", 1))
	if !strings.Contains(d.Error, "not allowed") || fs.requests.Load() != 0 {
		t.Fatalf("error=%q requests=%d", d.Error, fs.requests.Load())
	}
}

func TestCreateRejectsUnsafeInput(t *testing.T) {
	e := newEnv(t, nil)
	for _, req := range []domain.CreateRequest{
		{URL: "file:///etc/passwd"},
		{URL: "https://user:pw@example.com/x"},
		{URL: "https://example.com/x", Filename: ".."},
		{URL: "https://example.com/x", Connections: 1000},
		{URL: "https://example.com/x", Headers: map[string]string{"Range": "bytes=0-"}},
	} {
		if _, err := e.svc.Create(context.Background(), req); !errors.Is(err, domain.ErrInvalidRequest) {
			t.Errorf("%+v accepted: %v", req, err)
		}
	}
	d, err := e.svc.Create(context.Background(), domain.CreateRequest{URL: "https://example.com/a/..%2F..%2Fevil.sh", Directory: e.dir})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(d.Filename, `/\`) || filepath.Dir(d.TempPath) != d.Destination || !strings.HasSuffix(d.TempPath, filesystem.TempSuffix) {
		t.Fatalf("unsafe derived path: %+v", d)
	}
}

func TestNoGoroutineLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	data := payload(1<<20, 26)
	fs := slowServer(t, data, nil)
	e := newEnv(t, nil)
	ctx := context.Background()
	id := e.add(fs.url("/files/leak.bin"), "leak.bin", 4)
	e.waitProgress(id, len(data))
	if err := e.svc.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	e.waitCompleted(id)
	e.close()
	fs.srv.Close()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+2 {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: before=%d after=%d\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
