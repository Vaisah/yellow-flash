// Ported from Mister Download Manager, crates/engine/tests/unlock.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package integration

// Downloads through a service account, with a fake unlocker standing for a
// debrid service. The host's link (hoster.test) resolves nowhere: a
// download that completes came through the unlocker's direct link (the
// test's file server).

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
)

const hostLink = "http://hoster.test/file/abc/movie.bin"

// fakeService unlocks links; answer decides each unlock (1-based).
type fakeService struct {
	calls  atomic.Int32
	answer func(call int) (*engine.Unlocked, error)
}

func (f *fakeService) Name() string { return "Fake-Debrid" }

func (f *fakeService) Unlock(_ context.Context, _ string, note func(string)) (*engine.Unlocked, error) {
	note("Fake-Debrid is preparing the link")
	return f.answer(int(f.calls.Add(1)))
}

// hosterOnly gives hoster.test links to the service.
type hosterOnly struct{ f *fakeService }

func (h hosterOnly) UnlockerFor(rawURL string) engine.Unlocker {
	if u, err := url.Parse(rawURL); err == nil && u.Hostname() == "hoster.test" {
		return h.f
	}
	return nil
}

// switchable gives every link to the service while on is set.
type switchable struct {
	f  *fakeService
	on *atomic.Bool
}

func (s switchable) UnlockerFor(string) engine.Unlocker {
	if s.on.Load() {
		return s.f
	}
	return nil
}

func direct(link string, chunks int) func(int) (*engine.Unlocked, error) {
	return func(int) (*engine.Unlocked, error) {
		return &engine.Unlocked{URL: link, FileName: "from-the-service.bin", MaxConnections: chunks}, nil
	}
}

func withService(f *fakeService) func(*engine.Config) {
	return func(c *engine.Config) { c.Unlockers = hosterOnly{f}; c.Connections = 8 }
}

// A host link comes through the service: at most its connection cap, none of
// the host's cookies at the service's server, the service's name for the
// file, and the direct link stored nowhere.
func TestAHostLinkComesThroughTheService(t *testing.T) {
	data := payload(4<<20, 44)
	var cookies atomic.Int32
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.maxActive = 2 // a third transfer at once would get 429
		fs.fail = func(r *http.Request, _ int64) (int, string) {
			if r.Header.Get("Cookie") != "" {
				cookies.Add(1)
			}
			return 0, ""
		}
	})
	svc := &fakeService{answer: direct(fs.url("/gen/d?sig=SECRETSIG"), 2)}
	e := newEnv(t, withService(svc))
	d, err := e.svc.Add(context.Background(), domain.CreateRequest{URL: hostLink, Directory: e.dir,
		Headers: map[string]string{"Cookie": "host_session=secret"}})
	if err != nil {
		t.Fatal(err)
	}
	got := e.waitCompleted(d.ID)
	e.assertFile(got, data)
	if got.Filename != "from-the-service.bin" {
		t.Errorf("name %q", got.Filename)
	}
	if svc.calls.Load() != 1 {
		t.Errorf("unlocked %d times", svc.calls.Load())
	}
	if fs.throttled.Load() != 0 {
		t.Errorf("the service's cap of 2 connections was passed (%d refused)", fs.throttled.Load())
	}
	if cookies.Load() != 0 {
		t.Error("the host's cookie went to the service's link")
	}
	if p := e.persisted(d.ID); p.FinalURL != hostLink || p.URL != hostLink {
		t.Errorf("stored addresses %q %q", p.URL, p.FinalURL)
	}
	if b, _ := os.ReadFile(e.dbPath); strings.Contains(string(b), "SECRETSIG") {
		t.Error("the direct link is in the saved list")
	}
}

// A direct link that dies mid-download is unlocked again, and the download
// continues from the bytes it has.
func TestAnExpiredDirectLinkIsUnlockedAgain(t *testing.T) {
	data := payload(2<<20, 45)
	var expired atomic.Bool
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.fail = func(r *http.Request, _ int64) (int, string) {
			if r.URL.Query().Get("gen") == "1" && expired.Load() {
				return http.StatusForbidden, "" // the first link has died
			}
			return 0, ""
		}
		fs.resetAt = func(r *http.Request, _ int64) int64 {
			if r.URL.Query().Get("gen") == "1" {
				expired.Store(true) // its connections drop as it dies
				return 300 << 10
			}
			return -1
		}
	})
	svc := &fakeService{answer: func(n int) (*engine.Unlocked, error) {
		return &engine.Unlocked{URL: fs.url("/gen/e?gen=" + strconv.Itoa(n))}, nil
	}}
	e := newEnv(t, withService(svc))
	got := e.waitCompleted(e.add(hostLink, "e.bin", 2))
	e.assertFile(got, data)
	if svc.calls.Load() < 2 {
		t.Errorf("unlocked %d times", svc.calls.Load())
	}
}

// A host the service declines is downloaded directly.
func TestAHostTheServiceDeclinesIsDownloadedDirectly(t *testing.T) {
	data := payload(1<<20, 46)
	fs := newFileServer(t, data, nil)
	svc := &fakeService{answer: func(int) (*engine.Unlocked, error) { return nil, nil }}
	on := &atomic.Bool{}
	on.Store(true)
	e := newEnv(t, func(c *engine.Config) { c.Unlockers = switchable{svc, on} })
	e.assertFile(e.waitCompleted(e.add(fs.url("/files/plain.bin"), "", 4)), data)
	if svc.calls.Load() != 1 {
		t.Errorf("asked %d times", svc.calls.Load())
	}
}

// A refusal stops the download before any file; one a wait may cure is
// asked again after that wait.
func TestAServiceRefusalStopsTheDownloadBeforeAnyFile(t *testing.T) {
	data := payload(1<<20, 47)
	fs := newFileServer(t, data, nil)
	svc := &fakeService{answer: func(int) (*engine.Unlocked, error) {
		return nil, &engine.ServiceError{Fault: engine.FaultFileUnavailable}
	}}
	e := newEnv(t, withService(svc))
	d := e.waitFailed(e.add(hostLink, "", 4))
	if d.Error != "metadata: Fake-Debrid: the file is gone or unavailable on the file host" {
		t.Errorf("error %q", d.Error)
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
		t.Errorf("files made: %v", entries)
	}
	if fs.requests.Load() != 0 {
		t.Error("a request went out")
	}

	slow := &fakeService{answer: func(n int) (*engine.Unlocked, error) {
		if n == 1 {
			return nil, &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: 50 * time.Millisecond}
		}
		return &engine.Unlocked{URL: fs.url("/gen/f")}, nil
	}}
	e2 := newEnv(t, withService(slow))
	e2.assertFile(e2.waitCompleted(e2.add(hostLink, "f.bin", 4)), data)
	if slow.calls.Load() != 2 {
		t.Errorf("asked %d times", slow.calls.Load())
	}
}

// The Add URL preview names and sizes the file through the service, and
// shows the host's link.
func TestThePreviewNamesAndSizesTheFileThroughTheService(t *testing.T) {
	data := payload(2<<20, 48)
	fs := newFileServer(t, data, nil)
	e := newEnv(t, withService(&fakeService{answer: direct(fs.url("/gen/d?sig=SECRETSIG"), 0)}))
	info, err := e.svc.Inspect(context.Background(), hostLink, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(data)) || info.Filename != "from-the-service.bin" || info.FinalURL != hostLink {
		t.Errorf("%+v", info)
	}
}

// A download begun directly continues through the service once an account
// takes its host: the service is asked at once, and the bytes stay.
func TestADownloadBegunDirectlyContinuesThroughTheService(t *testing.T) {
	data := payload(2<<20, 49)
	var slow atomic.Bool
	slow.Store(true)
	fs := newFileServer(t, data, func(fs *fileServer) {
		fs.gate = func(*http.Request, int64) {
			if slow.Load() {
				time.Sleep(5 * time.Millisecond)
			}
		}
	})
	svc := &fakeService{answer: direct(fs.url("/gen/d"), 0)}
	on := &atomic.Bool{}
	e := newEnv(t, func(c *engine.Config) { c.Unlockers = switchable{svc, on} })
	id := e.add(fs.url("/files/d.bin"), "d.bin", 2)
	e.waitFor(id, "some bytes", func(d *domain.Download) bool { return d.Downloaded > 256<<10 })
	if err := e.svc.Pause(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if d := e.waitRest(id); d.Status != domain.Paused {
		t.Fatalf("%s: %s", d.Status, d.Error)
	}
	slow.Store(false)
	on.Store(true)
	if err := e.svc.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	e.assertFile(e.waitCompleted(id), data)
	if svc.calls.Load() != 1 {
		t.Errorf("asked %d times", svc.calls.Load())
	}
}
