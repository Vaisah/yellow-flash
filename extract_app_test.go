package main

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/application/download"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
)

func zipBytes(t *testing.T, files map[string]string, flags uint16) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, text := range files {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Flags: flags})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(text))
	}
	_ = z.Close()
	return b.Bytes()
}

// extractApp is an app whose downloads go to a temporary folder, each
// extraction's outcome sent on the channel.
func extractApp(t *testing.T) (*App, chan ExtractResult, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: store, dataDir: dir, commands: make(chan ipc.Incoming, commandBacklog), stop: make(chan struct{})}
	mgr, err := engine.New(store, engine.Config{AllowPrivateNetworks: true})
	if err != nil {
		t.Fatal(err)
	}
	a.manager, a.service = mgr, download.New(mgr)
	results := make(chan ExtractResult, 8)
	a.extracts.done = func(r ExtractResult) { results <- r }
	t.Cleanup(func() {
		a.stopExtracts()
		_ = mgr.Shutdown(context.Background())
		_ = store.Close()
	})
	return a, results, filepath.Join(dir, "files")
}

// fetch downloads url into dir and waits for its end.
func fetch(t *testing.T, a *App, url, dir string) domain.Download {
	t.Helper()
	d, err := a.service.Add(context.Background(), domain.CreateRequest{URL: url, Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := a.service.Get(context.Background(), d.ID)
		if got.Status == domain.Completed {
			return *got
		}
		if got.Status == domain.Failed {
			t.Fatal(got.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
	return domain.Download{}
}

func result(t *testing.T, ch chan ExtractResult) ExtractResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("no extraction")
	}
	return ExtractResult{}
}

func nothingMore(t *testing.T, ch chan ExtractResult, why string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatal(why, r)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAnArchiveIsExtractedWhenItsDownloadCompletes(t *testing.T) {
	trash := filepath.Join(t.TempDir(), "trash")
	t.Setenv(platform.TrashDirEnv, trash)
	pack := zipBytes(t, map[string]string{"a.txt": "alpha", "sub/b.txt": "beta"}, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "pack.zip", time.Unix(1_700_000_000, 0), bytes.NewReader(pack))
	}))
	defer srv.Close()
	a, results, dir := extractApp(t)

	// Off (the default): left as it is.
	d := fetch(t, a, srv.URL+"/pack.zip", dir)
	a.downloadFinished(d)
	nothingMore(t, results, "extracted while off")

	s := a.GetExtractSettings()
	s.Auto, s.DeleteArchive = true, true
	if _, err := a.SaveExtractSettings(s); err != nil {
		t.Fatal(err)
	}
	a.downloadFinished(d)
	r := result(t, results)
	if !r.OK || r.Report.Files != 2 || r.Report.Folder != filepath.Join(dir, "pack") || r.Trashed == nil || !*r.Trashed {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "pack", "sub", "b.txt")); string(b) != "beta" {
		t.Fatal("b.txt")
	}
	if _, err := os.Stat(filepath.Join(trash, "pack.zip")); err != nil {
		t.Fatal("the archive went to the trash", err)
	}
	if runtime.GOOS == "windows" {
		mark, err := os.ReadFile(filepath.Join(dir, "pack", "a.txt") + ":Zone.Identifier")
		if err != nil || !strings.Contains(string(mark), "HostUrl="+srv.URL+"/pack.zip") {
			t.Fatal("marked as downloaded from the archive's site", string(mark), err)
		}
	}
	// Once only, whatever tells it again.
	a.downloadFinished(d)
	nothingMore(t, results, "twice")
	if _, err := a.SaveExtractSettings(ExtractSettings{Into: "sideways"}); err == nil {
		t.Fatal("an unknown choice")
	}
}

func TestASplitArchiveWaitsForEveryVolume(t *testing.T) {
	mk := func(id, name string, st domain.Status) domain.Download {
		return domain.Download{ID: id, Filename: name, Destination: "dl", Status: st}
	}
	all := []domain.Download{
		mk("1", "m.part1.rar", domain.Completed),
		mk("2", "m.part2.rar", domain.Downloading),
		mk("3", "m.part3.rar", domain.Completed),
		// Another folder's volume of the same name is not this set's.
		{ID: "4", Filename: "m.part2.rar", Destination: "other", Status: domain.Completed},
	}
	set := volumeSet(all[0], all)
	if len(set) != 3 || set[0].ID != "1" || set[1].ID != "2" || set[2].ID != "3" {
		t.Fatalf("%+v", set)
	}
	gap := []domain.Download{mk("5", "g.7z.001", domain.Completed), mk("6", "g.7z.003", domain.Completed)}
	if volumeSet(gap[1], gap) != nil {
		t.Fatal("a gap waits")
	}
	if one := volumeSet(mk("7", "a.zip", domain.Completed), nil); len(one) != 1 {
		t.Fatal(one)
	}
}

func TestAnArchiveThatNeedsAPasswordSaysSo(t *testing.T) {
	locked := zipBytes(t, map[string]string{"s.txt": "secret"}, 0x1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "locked.zip", time.Unix(1_700_000_000, 0), bytes.NewReader(locked))
	}))
	defer srv.Close()
	a, results, dir := extractApp(t)
	d := fetch(t, a, srv.URL+"/locked.zip", dir)
	if err := a.ExtractDownload(d.ID, ""); err != nil {
		t.Fatal(err)
	}
	if r := result(t, results); r.OK || r.Reason != "password" || r.ID != d.ID {
		t.Fatalf("%+v", r)
	}
	// By hand again, with a password: 7-Zip opens it (or says it's needed).
	if err := a.ExtractDownload(d.ID, "pw"); err != nil {
		t.Fatal(err)
	}
	if r := result(t, results); !r.OK && r.Reason != "no-7zip" && r.Reason != "wrong-password" && r.Reason != "damaged" {
		t.Fatalf("%+v", r)
	}
	if err := a.ExtractDownload("no-such-id", ""); err == nil {
		t.Fatal("no such download")
	}
}
