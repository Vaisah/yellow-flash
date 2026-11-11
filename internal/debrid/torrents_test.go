// Ported from Mister Download Manager, crates/debrid/tests/torrents.rs and src/torrents.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// Torrents through each service, end to end against debridtest's fakes: a
// magnet link sent to the account, its files downloaded over HTTP by the
// engine into the torrent's folder, byte for byte, and the torrent removed
// from the account's list afterwards. Nothing connects to a peer. Also: a
// torrent that dies at the service, and one whose file names climb out of
// the folder (refused, nothing written outside it).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// engineFetcher downloads each file through a fresh engine with d's
// accounts, as the app does with its own.
func engineFetcher(d *Debrid) Fetcher {
	return func(ctx context.Context, link, dir, name string) (string, int64, error) {
		mgr, err := engine.New(memory.New(), engine.Config{AllowPrivateNetworks: true, Unlockers: d,
			Retry: &engine.RetryPolicy{MaxRetries: 1, BaseDelay: 20 * time.Millisecond}})
		if err != nil {
			return "", 0, err
		}
		svc := appdownload.New(mgr)
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = svc.Shutdown(sctx)
		}()
		dl, err := svc.Add(ctx, domain.CreateRequest{URL: link, Directory: dir, Filename: name})
		if err != nil {
			return "", 0, err
		}
		for {
			got, err := svc.Get(context.Background(), dl.ID)
			if err != nil {
				return "", 0, err
			}
			switch got.Status {
			case domain.Completed:
				return filepath.Join(got.Destination, got.Filename), got.Size, nil
			case domain.Failed:
				return "", 0, errors.New(got.Error)
			}
			select {
			case <-ctx.Done():
				return "", 0, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}

type notes struct {
	mu    sync.Mutex
	lines []string
}

func (n *notes) add(s string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lines = append(n.lines, s)
}

func (n *notes) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.lines)
}

func sameAs(t *testing.T, path, linkPath string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || len(got) == 0 || !bytes.Equal(got, debridtest.Content(linkPath)) {
		t.Errorf("%s differs from %s (%v)", path, linkPath, err)
	}
}

func runTorrent(t *testing.T, d *Debrid, hash, folder string) (Result, string, *notes, error) {
	t.Helper()
	acct, _, ok := d.TorrentAccount()
	if !ok {
		t.Fatal("an account")
	}
	var sent string
	n := &notes{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := d.RunTorrent(ctx, Order{Account: acct, Magnet: "magnet:?xt=urn:btih:" + hash + "&dn=Lab%20Show",
		Folder: folder, Poll: 20 * time.Millisecond}, engineFetcher(d),
		Hooks{Sent: func(id string) { sent = id }, Note: n.add})
	return res, sent, n, err
}

func TestAMagnetThroughEachServiceComesDownAsFiles(t *testing.T) {
	for _, kind := range Kinds {
		r := newRig(t)
		d := r.debrid(t, Account{ID: "7", Service: kind, Credential: Token("good")})
		dir := t.TempDir()
		res, sent, n, err := runTorrent(t, d, "1234abcd", dir)
		if err != nil {
			t.Fatalf("%s: %v %v", kind, err, n.all())
		}
		show := filepath.Join(dir, "Lab Show")
		if res.Path != show || res.Size != (2<<20)+(64<<10) {
			t.Errorf("%s: %+v", kind, res)
		}
		sameAs(t, filepath.Join(show, "video.mkv"), "/file/2M/video.mkv")
		sameAs(t, filepath.Join(show, "extras", "notes.txt"), "/file/64K/notes.txt")
		if sent == "" || slices.Contains(r.fake.Held(), sent) {
			t.Errorf("%s: removed from the account's list afterwards (%q, %v)", kind, sent, r.fake.Held())
		}
		if !slices.ContainsFunc(n.all(), func(l string) bool { return strings.Contains(l, "is downloading the torrent") }) {
			t.Errorf("%s: %v", kind, n.all())
		}
		d.mu.RLock()
		left := len(d.pinned)
		d.mu.RUnlock()
		if left != 0 {
			t.Errorf("%s: %d links still pinned", kind, left)
		}
	}
}

func TestATorrentTheServiceCantGetFailsWithItsReason(t *testing.T) {
	for _, kind := range Kinds {
		r := newRig(t)
		d := r.debrid(t, Account{ID: "7", Service: kind, Credential: Token("good")})
		dir := t.TempDir()
		_, _, _, err := runTorrent(t, d, "dead0001", dir)
		var se *engine.ServiceError
		if !errors.As(err, &se) || (se.Fault != engine.FaultTorrentUnavailable && se.Fault != engine.FaultOther) ||
			se.Service != kind.Name() {
			t.Errorf("%s: %v", kind, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: files made", kind)
		}
	}
}

func TestFileNamesThatClimbOutOfTheFolderAreRefused(t *testing.T) {
	for _, kind := range []Kind{RealDebrid, AllDebrid} {
		r := newRig(t)
		d := r.debrid(t, Account{ID: "7", Service: kind, Credential: Token("good")})
		root := t.TempDir()
		dir := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := runTorrent(t, d, "bad00001", dir)
		if err == nil || !strings.Contains(err.Error(), "can't be written safely") {
			t.Errorf("%s: %v", kind, err)
		}
		for _, p := range []string{filepath.Join(root, "escape.bin"), filepath.Join(root, "a", "escape.bin")} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s: %s written", kind, p)
			}
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: nothing is written when one name is unsafe", kind)
		}
	}
}

// Stopped while the service works: started again with the transfer's id,
// nothing is sent twice and the files come down.
func TestAStoppedTorrentContinuesFromItsTransfer(t *testing.T) {
	r := newRig(t)
	d := r.debrid(t, Account{ID: "7", Service: RealDebrid, Credential: Token("good")})
	dir := t.TempDir()
	order := Order{Account: "7", Magnet: "magnet:?xt=urn:btih:5678&dn=Again", Folder: dir, Poll: 300 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	sent := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := d.RunTorrent(ctx, order, engineFetcher(d), Hooks{Sent: func(id string) { sent <- id }})
		done <- err
	}()
	id := <-sent
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !slices.Contains(r.fake.Held(), id) {
		t.Fatal("kept at the service")
	}
	order.Transfer, order.Poll = id, 20*time.Millisecond
	again := false
	res, err := d.RunTorrent(context.Background(), order, engineFetcher(d), Hooks{Sent: func(string) { again = true }})
	if err != nil || again {
		t.Fatal(err, again)
	}
	sameAs(t, filepath.Join(dir, "Again", "video.mkv"), "/file/2M/video.mkv")
	if res.Path != filepath.Join(dir, "Again") || slices.Contains(r.fake.Held(), id) {
		t.Error(res, r.fake.Held())
	}
}

func TestPathsFromTheServiceStayInTheTorrentsFolder(t *testing.T) {
	ok := map[string]string{
		"Show/S01/e1.mkv": filepath.Join("Show", "S01", "e1.mkv"),
		"/Show/e1.mkv":    filepath.Join("Show", "e1.mkv"),
		`a\b.txt`:         filepath.Join("a", "b.txt"),
	}
	for in, want := range ok {
		if got, good := SafePath(in); !good || got != want {
			t.Errorf("%q: %q %v", in, got, good)
		}
	}
	for _, bad := range []string{"../evil.exe", "ok/../../evil", "", "///"} {
		if got, good := SafePath(bad); good {
			t.Errorf("%q: %q", bad, got)
		}
	}
	// Each part a name Yellow Flash writes: no drive, no reserved characters.
	if p, good := SafePath("C:/x<y>.txt"); !good || filepath.IsAbs(p) || strings.ContainsAny(p, "<>:") {
		t.Error(p)
	}
}

func TestRealDebridsDocumentedTorrentStates(t *testing.T) {
	var ready rdInfo
	_ = json.Unmarshal([]byte(`{"id":"T","filename":"Show","hash":"h","bytes":3,"progress":100,"status":"downloaded",
		"files":[{"id":1,"path":"/Show/a.mkv","bytes":2,"selected":1},{"id":2,"path":"/Show/x.nfo","bytes":1,"selected":0},
		{"id":3,"path":"/Show/b.mkv","bytes":1,"selected":1}],
		"links":["https://real-debrid.com/d/A","https://real-debrid.com/d/B"]}`), &ready)
	st := rdStatusOf(ready)
	if !st.ready || st.name != "Show" || len(st.files) != 2 || st.files[0].Path != "/Show/a.mkv" ||
		st.files[0].Link != "https://real-debrid.com/d/A" || st.files[1].Path != "/Show/b.mkv" {
		t.Errorf("%+v", st)
	}
	busy := rdStatusOf(rdInfo{Status: "downloading", Progress: 42.5})
	if busy.ready || busy.failed != nil || busy.progress != 0.425 || busy.what != "downloading" {
		t.Errorf("%+v", busy)
	}
	if f := faultOf(t, rdStatusOf(rdInfo{Status: "dead"}).failed); f != engine.FaultTorrentUnavailable {
		t.Error(f)
	}
}

func TestAllDebridsDocumentedCodesAndTree(t *testing.T) {
	m := func(code int, status string) adMagnet {
		return adMagnet{Filename: "x", Size: 100, Status: status, StatusCode: code, Downloaded: 25}
	}
	if st, done := adStatusOf(m(1, "Downloading")); !done || st.progress != 0.25 || st.what != "downloading" {
		t.Errorf("%+v", st)
	}
	if _, done := adStatusOf(m(4, "Ready")); done {
		t.Error("ready is completed by its files")
	}
	if st, _ := adStatusOf(m(15, "File not available - no peer")); faultOf(t, st.failed) != engine.FaultTorrentUnavailable {
		t.Error(st)
	}
	var tree []adFileNode
	_ = json.Unmarshal([]byte(`[{"n":"Show","e":[{"n":"a.mkv","s":2,"l":"https://alldebrid.com/f/A"},
		{"n":"Sub","e":[{"n":"b.srt","s":1,"l":"https://alldebrid.com/f/B"}]}]}]`), &tree)
	var files []RemoteFile
	adFlatten(tree, "", &files)
	if len(files) != 2 || files[0].Path != "Show/a.mkv" || files[0].Size != 2 || files[1].Path != "Show/Sub/b.srt" {
		t.Errorf("%+v", files)
	}
}

// A torrent's file link goes through its account while it downloads,
// whatever its host.
func TestAPinnedLinkGoesThroughItsAccount(t *testing.T) {
	d := newRig(t).debrid(t, Account{ID: "1", Service: RealDebrid, Credential: Token("good")})
	link := "https://cdn.example/d/ABC"
	if d.UnlockerFor(link) != nil {
		t.Fatal("not on any host list")
	}
	// The service's own links are its account's, pinned or not.
	if u := d.UnlockerFor("https://real-debrid.com/d/ABC"); u == nil || u.Name() != "Real-Debrid" {
		t.Fatal("real-debrid.com is Real-Debrid's")
	}
	if d.UnlockerFor("https://alldebrid.com/f/X") != nil {
		t.Fatal("alldebrid.com isn't Real-Debrid's")
	}
	release := d.pin(link, d.session("1"))
	if u := d.UnlockerFor(link); u == nil || u.Name() != "Real-Debrid" {
		t.Fatal(u)
	}
	release()
	if d.UnlockerFor(link) != nil {
		t.Fatal("taken back")
	}
}

// Links asked for while the host lists are still being read wait for them:
// one the account takes goes through it, any other directly.
func TestLinksWaitForTheHostListsBeingRead(t *testing.T) {
	r := newRig(t)
	d := &Debrid{}
	d.SetAccounts([]Account{{ID: "1", Service: AllDebrid, Credential: Token("good")}}, r.client)
	host, other := d.UnlockerFor("http://hoster.test/file/1M/w.bin"), d.UnlockerFor("http://elsewhere.test/a")
	if host == nil || other == nil {
		t.Fatal("both wait for the lists")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = d.RefreshHosts(context.Background())
	}()
	u, err := host.Unlock(context.Background(), "http://hoster.test/file/1M/w.bin", func(string) {})
	if err != nil || u == nil || !strings.Contains(u.URL, "/gen/1M/w.bin") {
		t.Fatal(u, err)
	}
	if u, err := other.Unlock(context.Background(), "http://elsewhere.test/a", func(string) {}); u != nil || err != nil {
		t.Fatal("not the account's: directly", u, err)
	}
	if d.UnlockerFor("http://elsewhere.test/a") != nil {
		t.Fatal("once the lists are read, other links don't wait")
	}
	// A refusal while waiting names its service.
	gone, _ := (&waitingUnlocker{d: d, wait: closedChan()}).Unlock(context.Background(), "http://hoster.test/file/1M/g.bin?gone=1", func(string) {})
	if gone != nil {
		t.Fatal(gone)
	}
	_, err = (&waitingUnlocker{d: d, wait: closedChan()}).Unlock(context.Background(), "http://hoster.test/file/1M/g.bin?gone=1", func(string) {})
	var se *engine.ServiceError
	if !errors.As(err, &se) || se.Service != "AllDebrid" || se.Fault != engine.FaultFileUnavailable {
		t.Fatal(err)
	}
}

func closedChan() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
