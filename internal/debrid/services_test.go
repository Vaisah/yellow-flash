// Ported from Mister Download Manager, crates/debrid/tests/{realdebrid,services,no_secret_in_logs}.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// Each service against debridtest's fakes, end to end through the engine:
// the account, the host list, the unlock, the download, the sign-in, and
// the services' refusals. The fake host hoster.test resolves nowhere: a
// download that completes came through the service's direct link.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	appdownload "github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/debrid/debridtest"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/memory"
)

// syncBuffer is a log written by several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type rig struct {
	fake   *debridtest.Server
	client *Client
	log    *syncBuffer
}

func newRig(t *testing.T) *rig {
	fake := debridtest.Start(t)
	c := NewClient(http.DefaultClient, ServicesAt(fake.URL()))
	c.minPoll = 10 * time.Millisecond
	return &rig{fake: fake, client: c, log: &syncBuffer{}}
}

func (r *rig) debrid(t *testing.T, accounts ...Account) *Debrid {
	t.Helper()
	d := &Debrid{}
	d.SetAccounts(accounts, r.client)
	if err := d.RefreshHosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}

// download runs link through the engine with d's accounts, to its end.
func (r *rig) download(t *testing.T, d *Debrid, link string) *domain.Download {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(r.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mgr, err := engine.New(memory.New(), engine.Config{AllowPrivateNetworks: true, Unlockers: d, Logger: logger,
		Retry: &engine.RetryPolicy{MaxRetries: 1, BaseDelay: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	svc := appdownload.New(mgr)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	}()
	dl, err := svc.Add(context.Background(), domain.CreateRequest{URL: link, Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Get(context.Background(), dl.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == domain.Completed || got.Status == domain.Failed {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not end", link)
	return nil
}

func (r *rig) expectFile(t *testing.T, d *domain.Download, linkPath string) {
	t.Helper()
	if d.Status != domain.Completed {
		t.Fatalf("%s: %s", d.Status, d.Error)
	}
	got, err := os.ReadFile(filepath.Join(d.Destination, d.Filename))
	if err != nil || !bytes.Equal(got, debridtest.Content(linkPath)) {
		t.Fatalf("%s: the file differs (%v)", linkPath, err)
	}
	// The token went to the API only, never to the direct link's server.
	for _, req := range r.fake.Log() {
		if strings.HasPrefix(req.Path, "/gen/") && req.Authorization != "" {
			t.Fatal("the token went to the file's server")
		}
	}
}

func expectRefusal(t *testing.T, d *domain.Download, service string, f engine.Fault) {
	t.Helper()
	want := service + ": " + f.Describe("")
	if d.Status != domain.Failed || !strings.Contains(d.Error, want) {
		t.Errorf("%s %q, want %q", d.Status, d.Error, want)
	}
}

func TestRealDebridLinksComeThroughTheAccount(t *testing.T) {
	r := newRig(t)
	info, err := Check(context.Background(), r.client, RealDebrid, Token("good"))
	if err != nil || info.User != "labuser" || !info.Premium || info.PremiumLeft <= 0 {
		t.Fatal(info, err)
	}
	d := r.debrid(t, Account{ID: "7", Service: RealDebrid, Credential: Token("good")})
	link := "http://hoster.test/file/2M/movie.mkv?chunks=4"
	if id, kind, ok := d.AccountFor(link); !ok || id != "7" || kind != RealDebrid {
		t.Error(id, kind, ok)
	}
	got := r.download(t, d, link)
	r.expectFile(t, got, "/file/2M/movie.mkv")
	if got.Filename != "movie.mkv" || got.URL != link || got.FinalURL != link {
		t.Errorf("%+v", got)
	}
	for _, other := range []string{"http://example.com/a.zip", "http://nothoster.test/file/1M/a", "ftp://hoster.test/a"} {
		if d.UnlockerFor(other) != nil {
			t.Error(other)
		}
	}
	if !d.HostsKnown() {
		t.Error("the host lists were read")
	}
}

func TestRealDebridsRefusalsSayWhy(t *testing.T) {
	r := newRig(t)
	premium := r.debrid(t, Account{ID: "1", Service: RealDebrid, Credential: Token("good")})
	expectRefusal(t, r.download(t, premium, "http://hoster.test/file/1M/a.bin?gone=1"), "Real-Debrid", engine.FaultFileUnavailable)
	expectRefusal(t, r.download(t, premium, "http://hoster.test/file/1M/a.bin?down=1"), "Real-Debrid", engine.FaultHostDown)
	free := r.debrid(t, Account{ID: "2", Service: RealDebrid, Credential: Token("free")})
	expectRefusal(t, r.download(t, free, "http://hoster.test/file/1M/a.bin"), "Real-Debrid", engine.FaultNotPremium)
	wrong := r.debrid(t, Account{ID: "3", Service: RealDebrid, Credential: Token("wrong")})
	expectRefusal(t, r.download(t, wrong, "http://hoster.test/file/1M/a.bin"), "Real-Debrid", engine.FaultSignIn)
	if _, err := Check(context.Background(), r.client, RealDebrid, Token("wrong")); err == nil ||
		err.Error() != "Real-Debrid: sign in again" {
		t.Error(err)
	}
}

func TestRealDebridSignsInByCodeThenDownloads(t *testing.T) {
	r := newRig(t)
	code, err := StartSignIn(context.Background(), r.client, RealDebrid)
	if err != nil || code.Shown != "LABCODE1" || !strings.HasPrefix(code.Page, "https://real-debrid.com/") {
		t.Fatal(code, err)
	}
	var cr *Credential
	for range 10 {
		if cr, err = PollSignIn(context.Background(), r.client, code); err != nil {
			t.Fatal(err)
		}
		if cr != nil {
			break
		}
	}
	if cr == nil || cr.Kind != "oauth" || cr.RefreshToken == "" {
		t.Fatal("allowed after a few polls", cr)
	}
	if info, err := Check(context.Background(), r.client, RealDebrid, *cr); err != nil || !info.Premium {
		t.Fatal(info, err)
	}
	d := r.debrid(t, Account{ID: "1", Service: RealDebrid, Credential: *cr})
	r.expectFile(t, r.download(t, d, "http://hoster.test/file/1M/signed.bin"), "/file/1M/signed.bin")
}

func TestAllDebridLinksComeThroughTheAccountDelayedOnesToo(t *testing.T) {
	r := newRig(t)
	info, err := Check(context.Background(), r.client, AllDebrid, Token("good"))
	if err != nil || !info.Premium || info.PremiumLeft <= 0 {
		t.Fatal(info, err)
	}
	d := r.debrid(t, Account{ID: "3", Service: AllDebrid, Credential: Token("good")})
	r.expectFile(t, r.download(t, d, "http://hoster.test/file/1M/a.bin"), "/file/1M/a.bin")
	// A link the service needs time for: asked again until it is ready.
	r.expectFile(t, r.download(t, d, "http://hoster.test/file/1M/slow.bin?delayed=2"), "/file/1M/slow.bin")
	expectRefusal(t, r.download(t, d, "http://hoster.test/file/1M/g.bin?gone=1"), "AllDebrid", engine.FaultFileUnavailable)
	expectRefusal(t, r.download(t, d, "http://hoster.test/file/1M/d.bin?down=1"), "AllDebrid", engine.FaultHostDown)
	free := r.debrid(t, Account{ID: "4", Service: AllDebrid, Credential: Token("free")})
	expectRefusal(t, r.download(t, free, "http://hoster.test/file/1M/f.bin"), "AllDebrid", engine.FaultNotPremium)
	if d.UnlockerFor("http://elsewhere.test/a") != nil {
		t.Error("not a host it takes")
	}
}

func TestAllDebridSignsInByPIN(t *testing.T) {
	r := newRig(t)
	code, err := StartSignIn(context.Background(), r.client, AllDebrid)
	if err != nil || code.Shown != "LABP" || !strings.HasPrefix(code.Page, "https://alldebrid.com/pin/") {
		t.Fatal(code, err)
	}
	var cr *Credential
	for range 5 {
		if cr, err = PollSignIn(context.Background(), r.client, code); err != nil {
			t.Fatal(err)
		}
		if cr != nil {
			break
		}
	}
	if cr == nil || *cr != Token("good") {
		t.Fatal(cr)
	}
}

func TestPremiumizeLinksComeThroughTheAccount(t *testing.T) {
	r := newRig(t)
	info, err := Check(context.Background(), r.client, Premiumize, Token("good"))
	if err != nil || info.User != "1234567" || !info.Premium {
		t.Fatal(info, err)
	}
	d := r.debrid(t, Account{ID: "5", Service: Premiumize, Credential: Token("good")})
	r.expectFile(t, r.download(t, d, "http://hoster.test/file/2M/b.bin"), "/file/2M/b.bin")
	expectRefusal(t, r.download(t, d, "http://hoster.test/file/1M/g.bin?gone=1"), "Premiumize", engine.FaultFileUnavailable)
	expectRefusal(t, r.download(t, d, "http://hoster.test/file/1M/d.bin?down=1"), "Premiumize", engine.FaultHostDown)
	if _, err := Check(context.Background(), r.client, Premiumize, Token("bad")); faultOf(t, err) != engine.FaultSignIn {
		t.Error(err)
	}
	// Premiumize signs in by its API key only.
	if _, err := StartSignIn(context.Background(), r.client, Premiumize); err == nil {
		t.Error("no sign-in by code")
	}
}

func TestAccountsAreAskedInTheirOrder(t *testing.T) {
	r := newRig(t)
	d := r.debrid(t,
		Account{ID: "1", Service: Premiumize, Credential: Token("good")},
		Account{ID: "2", Service: AllDebrid, Credential: Token("good")})
	// Both take hoster.test; the first in the list wins. Only AllDebrid takes hoster2.test.
	if id, kind, _ := d.AccountFor("http://hoster.test/x"); id != "1" || kind != Premiumize {
		t.Error(id, kind)
	}
	if id, kind, _ := d.AccountFor("http://hoster2.test/x"); id != "2" || kind != AllDebrid {
		t.Error(id, kind)
	}
	// An account taken away takes its host list with it.
	d.SetAccounts([]Account{{ID: "2", Service: AllDebrid, Credential: Token("good")}}, r.client)
	if id, _, _ := d.AccountFor("http://hoster.test/x"); id != "2" {
		t.Error(id)
	}
	if info, err := d.AccountInfo(context.Background(), "2"); err != nil || !info.Premium {
		t.Error(info, err)
	}
	if _, err := d.AccountInfo(context.Background(), "1"); err == nil {
		t.Error("a removed account")
	}
}

// A debrid account's token never reaches a log: the engine's at its most
// detailed, the downloads' errors, and any dump, while each service signs
// in, lists its hosts and unlocks a link downloaded end to end.
func TestNoTokenInAnyLog(t *testing.T) {
	const token = "good-9f3c1a7e-NEVER-LOGGED"
	r := newRig(t)
	var seen strings.Builder
	for i, kind := range Kinds {
		cr := Token(token)
		info, err := Check(context.Background(), r.client, kind, cr)
		if err != nil || !info.Premium {
			t.Fatal(kind, info, err)
		}
		fmt.Fprintf(&seen, "%v %+v %#v", info, cr, cr)
		d := r.debrid(t, Account{ID: fmt.Sprint(i), Service: kind, Credential: cr})
		fmt.Fprintf(&seen, "%v %+v", d, d)
		for _, link := range []string{"http://hoster.test/file/1M/a.bin", "http://hoster.test/file/1M/gone.bin?gone=1"} {
			fmt.Fprintf(&seen, "%v", d.UnlockerFor(link))
			got := r.download(t, d, link)
			fmt.Fprintf(&seen, "%+v", got)
		}
	}
	logged := r.log.String()
	if strings.Count(logged, "unlocked through a service") != len(Kinds) {
		t.Fatalf("the log was captured: %s", logged)
	}
	if strings.Contains(logged, token) {
		t.Error("the token is in the log")
	}
	if strings.Contains(seen.String(), token) {
		t.Error("the token is in an error or a dump")
	}
	if strings.Contains(logged, "seed=") || strings.Contains(seen.String(), "seed=") {
		t.Error("a direct link is in the log or a record")
	}
}
