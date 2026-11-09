package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
)

// A download's own speed limit (ported from MDM's limiter.rs) holds the
// whole transfer to its rate, across all its connections, and is saved.
func TestPerDownloadSpeedLimit(t *testing.T) {
	data := payload(300<<10, 41)
	fs := newFileServer(t, data, nil)
	e := newEnv(t, nil)
	start := time.Now()
	d, err := e.svc.Add(context.Background(), domain.CreateRequest{
		URL: fs.url("/files/slow.bin"), Directory: e.dir, Connections: 4, SpeedLimit: 200 << 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := e.waitCompleted(d.ID)
	e.assertFile(done, data)
	// 300 KB at 200 KB/s with an empty bucket at the start: about 1.5 s.
	if took := time.Since(start); took < time.Second {
		t.Fatalf("finished in %v: the limit was not applied", took)
	}
	if got := e.persisted(d.ID).SpeedLimit; got != 200<<10 {
		t.Fatalf("persisted limit %d", got)
	}
	if err = e.mgr.SetDownloadSpeedLimit(context.Background(), d.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := e.persisted(d.ID).SpeedLimit; got != 0 {
		t.Fatalf("limit not cleared: %d", got)
	}
}

// A checksum the user gives (ported from MDM's verify.rs) is checked when
// the download finishes, and saved with it.
func TestUserChecksum(t *testing.T) {
	data := payload(128<<10, 42)
	fs := newFileServer(t, data, nil)
	e := newEnv(t, nil)
	sum := sha256.Sum256(data)
	right, err := e.svc.Add(context.Background(), domain.CreateRequest{
		URL: fs.url("/files/ok.bin"), Directory: e.dir, Checksum: "SHA-256: " + hex.EncodeToString(sum[:]) + "  *ok.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.assertFile(e.waitCompleted(right.ID), data)
	if got := e.persisted(right.ID).ExpectedChecksum; got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("persisted checksum %q", got)
	}

	wrong, err := e.svc.Add(context.Background(), domain.CreateRequest{
		URL: fs.url("/files/bad.bin"), Directory: e.dir, Checksum: "md5:00000000000000000000000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := e.waitFailed(wrong.ID); !strings.Contains(d.Error, "checksum mismatch") {
		t.Fatalf("error %q", d.Error)
	}

	_, err = e.svc.Add(context.Background(), domain.CreateRequest{URL: fs.url("/files/x.bin"), Directory: e.dir, Checksum: "md5:abc"})
	if !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("an unusable checksum must be refused: %v", err)
	}
}

// A per-server connection limit learned from refusals (ported from MDM's
// hosts.rs) is the most connections seen working, is saved, and survives a
// restart, so the next download starts at it instead of being refused
// again.
func TestLearnedHostLimitSurvivesRestart(t *testing.T) {
	data := payload(2<<20, 43)
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.maxActive = 2
		fs.chunk, fs.delay = 16<<10, 2*time.Millisecond
	})
	e := newEnv(t, func(c *engine.Config) { c.Connections = 8 })
	e.assertFile(e.waitCompleted(e.add(fs.url("/files/a.bin"), "a.bin", 8)), data)
	if fs.throttled.Load() == 0 {
		t.Fatal("server limit was never hit")
	}
	u, _ := url.Parse(fs.url("/"))
	saved, err := e.store.LoadHostLimits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var learned domain.HostLimit
	for _, l := range saved {
		if l.Host == u.Host {
			learned = l
		}
	}
	if learned.MaxConnections < 1 || learned.MaxConnections > 2 {
		t.Fatalf("learned %+v from a server that allows 2 (saved %+v)", learned, saved)
	}
	t.Logf("learned %d connections after %d refusals", learned.MaxConnections, learned.Seen)
	if learned.Seen < 2 {
		t.Skip("refused only once: the limit does not apply yet")
	}

	e.restart()
	found := false
	for _, l := range e.mgr.HostLimits() {
		found = found || (l.Host == u.Host && l.MaxConnections == learned.MaxConnections)
	}
	if !found {
		t.Fatalf("limit not loaded after restart: %+v", e.mgr.HostLimits())
	}
	before := fs.throttled.Load()
	e.assertFile(e.waitCompleted(e.add(fs.url("/files/b.bin"), "b.bin", 8)), data)
	if learned.MaxConnections == 2 {
		if n := fs.throttled.Load() - before; n != 0 {
			t.Fatalf("started at the learned limit, yet refused %d times", n)
		}
	}
}
