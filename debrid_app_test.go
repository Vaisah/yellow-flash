package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/debrid"
	"github.com/vaisah/internal/debrid/debridtest"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
)

// token is a premium token at the fake services, easy to find if it leaks.
const token = "good-7d1e-NEVER-SAVED"

// debridApp is an app whose debrid accounts ask the fake services, its
// sign-ins kept in memory (not this computer's keychain).
func debridApp(t *testing.T, fake *debridtest.Server, store *jsonstore.Store, dir string, secrets platform.SecretStore) *App {
	t.Helper()
	a := &App{store: store, dataDir: dir, commands: make(chan ipc.Incoming, commandBacklog), stop: make(chan struct{})}
	services := debrid.ServicesAt(fake.URL())
	a.debrid.services, a.debrid.httpClient, a.debrid.poll = &services, http.DefaultClient, 20*time.Millisecond
	a.debrid.secrets = secrets
	mgr, err := engine.New(store, engine.Config{AllowPrivateNetworks: true, Unlockers: a.debridAccounts(),
		Retry: &engine.RetryPolicy{MaxRetries: 1, BaseDelay: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	a.manager, a.service = mgr, download.New(mgr)
	s := a.GetSettings()
	s.DownloadDirectory = filepath.Join(dir, "files")
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	a.startDebrid()
	t.Cleanup(func() {
		a.stopDebrid()
		_ = mgr.Shutdown(context.Background())
	})
	return a
}

func newDebridApp(t *testing.T) (*App, *debridtest.Server, platform.SecretStore) {
	t.Helper()
	fake := debridtest.Start(t)
	dir := t.TempDir()
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secrets := platform.NewMemorySecretStore()
	return debridApp(t, fake, store, dir, secrets), fake, secrets
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// noSecretIn checks the app's files hold no sign-in.
func noSecretIn(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(token)) {
				t.Errorf("the sign-in is in %s", p)
			}
		}
		return nil
	})
}

func TestADebridAccountSignsInByTokenAndUnlocksLinks(t *testing.T) {
	a, _, secrets := newDebridApp(t)
	if _, err := a.AddDebridToken("realdebrid", "wrong"); err == nil || err.Error() != "Real-Debrid: sign in again" {
		t.Fatal(err)
	}
	if _, err := a.AddDebridToken("nosuch", token); err == nil {
		t.Fatal("an unknown service")
	}
	if n := len(a.GetDebridSettings().Accounts); n != 0 {
		t.Fatal("a refused token adds nothing:", n)
	}
	acct, err := a.AddDebridToken("realdebrid", " "+token+" ")
	if err != nil || acct.User != "labuser" || !acct.SignedIn || !acct.Enabled {
		t.Fatal(acct, err)
	}
	s := a.GetDebridSettings()
	if len(s.Accounts) != 1 || !s.Accounts[0].SignedIn || s.Keychain {
		t.Fatalf("%+v", s)
	}
	if m, err := secrets.Get(context.Background(), "debrid/"+acct.ID); err != nil || m["token"] != token {
		t.Fatal("the sign-in is in the secret store", err)
	}
	if err := a.RefreshDebridHosts(); err != nil {
		t.Fatal(err)
	}
	if info, err := a.CheckDebridAccount(acct.ID); err != nil || !info.Premium {
		t.Fatal(info, err)
	}

	d, err := a.AddDownload(AddRequest{URL: "http://hoster.test/file/1M/a.bin", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	var got domain.Download
	waitFor(t, "the download", func() bool {
		got, _ = a.GetDownload(d.ID)
		return got.Status == domain.Completed || got.Status == domain.Failed
	})
	if b, _ := os.ReadFile(filepath.Join(got.Destination, got.Filename)); !bytes.Equal(b, debridtest.Content("/file/1M/a.bin")) {
		t.Fatalf("%s: %s", got.Status, got.Error)
	}
	noSecretIn(t, a.dataDir)

	// Switched off, its links go directly; removed, its sign-in goes too.
	s.Accounts[0].Enabled = false
	if s, err = a.SaveDebridSettings(s); err != nil || s.Accounts[0].Enabled {
		t.Fatal(s, err)
	}
	if a.debridAccounts().UnlockerFor("http://hoster.test/file/1M/a.bin") != nil {
		t.Fatal("a switched-off account takes no link")
	}
	if err := a.RemoveDebridAccount(acct.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Get(context.Background(), "debrid/"+acct.ID); err == nil {
		t.Fatal("the sign-in was removed")
	}
	if len(a.GetDebridSettings().Accounts) != 0 {
		t.Fatal("the account was removed")
	}
}

func TestADebridAccountSignsInByCode(t *testing.T) {
	a, _, _ := newDebridApp(t)
	in, err := a.StartDebridSignIn("realdebrid")
	if err != nil || in.Code != "LABCODE1" || in.State != "waiting" || !strings.HasPrefix(in.Page, "https://real-debrid.com/") {
		t.Fatal(in, err)
	}
	waitFor(t, "the sign-in", func() bool { return a.GetDebridSignIn().State != "waiting" })
	done := a.GetDebridSignIn()
	if done.State != "done" || done.Account == nil || done.Account.User != "labuser" {
		t.Fatalf("%+v", done)
	}
	if s := a.GetDebridSettings(); len(s.Accounts) != 1 || !s.Accounts[0].SignedIn {
		t.Fatalf("%+v", s)
	}
	// Signed in again as the same user: the same account, not a second.
	if _, err := a.AddDebridToken("realdebrid", token); err != nil || len(a.GetDebridSettings().Accounts) != 1 {
		t.Fatal(err, a.GetDebridSettings())
	}
	if _, err := a.StartDebridSignIn("premiumize"); err == nil {
		t.Fatal("Premiumize signs in with its API token")
	}
	in, _ = a.StartDebridSignIn("alldebrid")
	a.CancelDebridSignIn()
	waitFor(t, "the cancel", func() bool { return a.GetDebridSignIn().State == "cancelled" })
	if in.Code != "LABP" {
		t.Error(in)
	}
}

func TestDebridAccountsSurviveARestart(t *testing.T) {
	a, fake, secrets := newDebridApp(t)
	acct, err := a.AddDebridToken("alldebrid", token)
	if err != nil {
		t.Fatal(err)
	}
	a.stopDebrid()
	// The same data folder and keychain, a new app.
	again := debridApp(t, fake, a.store, a.dataDir, secrets)
	if s := again.GetDebridSettings(); len(s.Accounts) != 1 || s.Accounts[0].ID != acct.ID || !s.Accounts[0].SignedIn {
		t.Fatalf("%+v", s)
	}
	waitFor(t, "the host lists", func() bool {
		id, _, ok := again.debridAccounts().AccountFor("http://hoster.test/x")
		return ok && id == acct.ID
	})
	// Signed out elsewhere (the keychain lost it): listed, not used.
	_ = secrets.Delete(context.Background(), "debrid/"+acct.ID)
	again.applyDebrid()
	if s := again.GetDebridSettings(); s.Accounts[0].SignedIn {
		t.Fatal("no sign-in kept")
	}
	if _, _, ok := again.debridAccounts().TorrentAccount(); ok {
		t.Fatal("an account without its sign-in isn't used")
	}
}

func TestTorrentsGoThroughTheDebridAccount(t *testing.T) {
	a, fake, _ := newDebridApp(t)
	if a.debridTorrentsOn() {
		t.Fatal("off without an account")
	}
	if _, err := a.SendTorrentToDebrid("magnet:?xt=urn:btih:1234abcd&dn=Lab%20Show", ""); err == nil {
		t.Fatal("no account yet")
	}
	if _, err := a.AddDebridToken("premiumize", token); err != nil {
		t.Fatal(err)
	}
	s := a.GetDebridSettings()
	s.Torrents = true
	if _, err := a.SaveDebridSettings(s); err != nil {
		t.Fatal(err)
	}
	if !a.debridTorrentsOn() {
		t.Fatal("on")
	}
	dir := filepath.Join(a.dataDir, "torrents-here")
	// As the command line, the browser or a paired phone hands it over.
	a.torrentCommand(context.Background(), ipc.Command{URL: "magnet:?xt=urn:btih:1234abcd&dn=Lab%20Show", Dir: dir, Silent: true})
	waitFor(t, "the torrent", func() bool {
		l := a.ListDebridTorrents()
		return len(l) == 1 && (l[0].State == "done" || l[0].State == "failed")
	})
	tor := a.ListDebridTorrents()[0]
	show := filepath.Join(dir, "Lab Show")
	if tor.State != "done" || tor.Path != show || tor.Done != (2<<20)+(64<<10) || tor.Transfer == "" {
		t.Fatalf("%+v", tor)
	}
	if b, _ := os.ReadFile(filepath.Join(show, "extras", "notes.txt")); !bytes.Equal(b, debridtest.Content("/file/64K/notes.txt")) {
		t.Fatal("the file differs")
	}
	if len(fake.Held()) != 0 {
		t.Fatal("removed from the account's list", fake.Held())
	}
	// Its files came as ordinary downloads.
	all, _ := a.ListDownloads()
	if len(all) != 2 || all[0].Status != domain.Completed || all[1].Status != domain.Completed {
		t.Fatalf("%+v", all)
	}
	if err := a.RemoveDebridTorrent(tor.ID); err != nil || len(a.ListDebridTorrents()) != 0 {
		t.Fatal(err)
	}
	noSecretIn(t, a.dataDir)
}

func TestADebridTorrentStopsAndContinues(t *testing.T) {
	a, fake, _ := newDebridApp(t)
	a.debrid.poll = 300 * time.Millisecond
	if _, err := a.AddDebridToken("realdebrid", token); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.dataDir, "again")
	tor, err := a.SendTorrentToDebrid("magnet:?xt=urn:btih:5678&dn=Again", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendTorrentToDebrid("magnet:?xt=urn:btih:5678&dn=Again", dir); err == nil {
		t.Fatal("the same torrent twice")
	}
	waitFor(t, "sent", func() bool { return a.ListDebridTorrents()[0].Transfer != "" })
	if err := a.StopDebridTorrent(tor.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped", func() bool { return a.ListDebridTorrents()[0].State == "stopped" })
	transfer := a.ListDebridTorrents()[0].Transfer
	if len(fake.Held()) != 1 {
		t.Fatal("kept at the service")
	}
	a.debrid.poll = 20 * time.Millisecond
	if err := a.ResumeDebridTorrent(tor.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "done", func() bool { s := a.ListDebridTorrents()[0].State; return s == "done" || s == "failed" })
	got := a.ListDebridTorrents()[0]
	if got.State != "done" || got.Transfer != transfer {
		t.Fatalf("%+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Again", "video.mkv")); !bytes.Equal(b, debridtest.Content("/file/2M/video.mkv")) {
		t.Fatal("the file differs")
	}
}
