package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaisah/internal/application/queues"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/platform"
)

func TestTheWindowsDialogsWork(t *testing.T) {
	body := bytes.Repeat([]byte("yellow"), 50_000)
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private.bin" {
			if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "s3cret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			auth = r.Header.Get("Authorization")
		}
		http.ServeContent(w, r, "f.bin", time.Unix(1_700_000_000, 0), bytes.NewReader(body))
	}))
	defer srv.Close()
	a, _, dir := extractApp(t)
	a.debrid.secrets = platform.NewMemorySecretStore() // not this computer's keychain
	s := a.GetSettings()
	s.DownloadDirectory = dir
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}

	// Checksum of a finished file.
	d := fetch(t, a, srv.URL+"/public.bin", dir)
	sum := sha256.Sum256(body)
	if h, err := a.FileChecksum(d.ID, "sha256"); err != nil || h != hex.EncodeToString(sum[:]) {
		t.Fatal(h, err)
	}
	if _, err := a.FileChecksum(d.ID, "crc99"); err == nil {
		t.Fatal("an unknown algorithm")
	}

	// A site's saved login goes with its downloads; the password isn't in the list.
	if _, err := a.SaveSiteLogin("http://127.0.0.1/whatever", "me", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if l := a.ListSiteLogins(); len(l) != 1 || l[0].Site != "127.0.0.1" || l[0].User != "me" {
		t.Fatal(l)
	}
	got, err := a.AddDownload(AddRequest{URL: srv.URL + "/private.bin", Directory: dir, Start: true})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the private download", func() bool {
		x, _ := a.GetDownload(got.ID)
		return x.Status == domain.Completed || x.Status == domain.Failed
	})
	if x, _ := a.GetDownload(got.ID); x.Status != domain.Completed || auth == "" {
		t.Fatal(x.Status, x.Error)
	}
	if b, _ := os.ReadFile(filepath.Join(a.dataDir, "downloads.json")); bytes.Contains(b, []byte("s3cret")) {
		t.Fatal("the password is in the list")
	}
	if l, err := a.RemoveSiteLogin("127.0.0.1"); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	if a.loginFor(srv.URL+"/private.bin") != "" {
		t.Fatal("removed")
	}
	// Typed in Add URL instead.
	typed, err := a.AddDownload(AddRequest{URL: srv.URL + "/private.bin", Directory: dir, Filename: "typed.bin", Start: true,
		Username: "me", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the typed login", func() bool { x, _ := a.GetDownload(typed.ID); return x.Status == domain.Completed })

	// The disk; the cleaner touches only what is ours to clean.
	if info := a.DiskSpace(""); info.Total == 0 || info.UsedPercent < 0 || info.UsedPercent > 100 {
		t.Fatalf("%+v", info)
	}
	foreign := filepath.Join(dir, "browser-download.zip.part")
	_ = os.WriteFile(foreign, []byte("not ours"), 0o644)
	staging := filepath.Join(dir, ".yf-extract-1-1")
	_ = os.MkdirAll(staging, 0o755)
	_ = os.WriteFile(filepath.Join(staging, "x"), []byte("12345"), 0o644)
	res, err := a.CleanDisk()
	if err != nil || res.Files != 1 || res.Bytes != 5 {
		t.Fatal(res, err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("another program's .part file was removed")
	}

	// The speed limiter.
	if l, err := a.SetSpeedLimit(SpeedLimit{Enabled: true, KBps: 512}); err != nil || !l.Enabled || l.KBps != 512 ||
		a.GetNetworkSettings().SpeedLimitKBps != 512 {
		t.Fatal(l, err)
	}
	if l, _ := a.SetSpeedLimit(SpeedLimit{Enabled: false, KBps: 512}); l.Enabled || l.KBps != 512 || a.GetNetworkSettings().SpeedLimitKBps != 0 {
		t.Fatal("off, keeping the last rate", l)
	}

	// Queues tell the window whether they run.
	runner, err := queues.New(context.Background(), a.manager, a.store.Queues(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.queues.Store(runner)
	qs, err := a.ListQueues()
	if err != nil || len(qs) < 2 || qs[0].ID != "main" || !qs[0].Builtin {
		t.Fatal(qs, err)
	}

	// The Download Complete window's switch.
	o := a.GetOptions()
	o.ShowComplete = false
	if o, err = a.SaveOptions(o); err != nil || o.ShowComplete {
		t.Fatal(o, err)
	}

	// The browser setup window: a list (never null), and once done it is
	// not offered at start again.
	bs := a.GetBrowserSetup()
	if bs.Browsers == nil {
		t.Fatal("null browser list")
	}
	if err := a.FinishBrowserSetup(); err != nil || a.GetBrowserSetup().Offer {
		t.Fatal("offered after done", err)
	}
	if _, err := a.AddExtensionTo("Netscape"); err == nil {
		t.Fatal("a browser that isn't installed")
	}
}
