package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/torrent"
)

func torrentApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: store, dataDir: dir}
	t.Cleanup(func() {
		a.stopTorrents()
		_ = store.Close()
	})
	// Nothing reaches out: no DHT, no router.
	s := defaultTorrentSettings()
	s.DHT, s.UPnP = false, false
	if _, err := a.SaveTorrentSettings(s); err != nil {
		t.Fatal(err)
	}
	return a
}

// seed writes a folder "Pack" of two files in dir, and its .torrent.
func seed(t *testing.T, dir string) string {
	t.Helper()
	for name, size := range map[string]int{"a.bin": 40000, "b.bin": 9000} {
		p := filepath.Join(dir, "Pack", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte(name[:1]), size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(filepath.Join(dir, "Pack")); err != nil {
		t.Fatal(err)
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: ib}).Write(&b); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "pack.torrent")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTorrentSettingsAreChecked(t *testing.T) {
	a := torrentApp(t)
	got := a.GetTorrentSettings()
	if got.DHT || got.UPnP || !got.Incoming || got.KeepSharing || got.StopRatio != 200 || got.StopMinutes != 1440 {
		t.Errorf("%+v", got)
	}
	for _, bad := range []TorrentSettings{{Port: -1}, {Port: 70000}, {UploadLimitKB: -1}, {Blocklist: "file:///etc/hosts"}, {Blocklist: "not a url"}} {
		if _, err := a.SaveTorrentSettings(bad); err == nil {
			t.Errorf("%+v saved", bad)
		}
	}
	s := TorrentSettings{UploadLimitOn: true, UploadLimitKB: 0, StopRatioOn: true, StopRatio: 150, StopMinutesOn: true, StopMinutes: 0}
	if s.uploadLimit() != 1<<10 || s.limits().Ratio != 1.5 || s.limits().Secs != 60 {
		t.Errorf("%d %+v", s.uploadLimit(), s.limits())
	}
	if (TorrentSettings{}).uploadLimit() != 0 || (TorrentSettings{}).limits() != (torrent.SharingLimits{}) {
		t.Error("off")
	}
}

func TestTorrentCommandsAreRecognised(t *testing.T) {
	abs, _ := filepath.Abs("x.torrent")
	for _, cmd := range []ipc.Command{{URL: "magnet:?xt=urn:btih:ab"}, {URL: " " + abs}, {TorrentFile: []byte("d")}} {
		if !isTorrentCommand(cmd) {
			t.Errorf("%+v", cmd)
		}
	}
	for _, cmd := range []ipc.Command{{URL: "https://x.example/a.torrent"}, {URL: "x.torrent"}, {URL: "https://x.example/"}} {
		if isTorrentCommand(cmd) {
			t.Errorf("%+v", cmd)
		}
	}
}

func TestTheFilesChosenAreChecked(t *testing.T) {
	m := torrent.Meta{Files: []torrent.FileEntry{{Path: "a"}, {Path: "pad", Padding: true}, {Path: "b"}}}
	if got, err := chosenFiles(m, nil); got != nil || err != nil {
		t.Error(got, err)
	}
	if got, err := chosenFiles(m, []int{2, 0, 2}); err != nil || len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Error(got, err)
	}
	for _, bad := range [][]int{{1}, {3}, {-1}, {}} {
		if _, err := chosenFiles(m, bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// A .torrent whose files are there already: looked up, added paused,
// resumed, checked, finished and stopped (it doesn't keep sharing), kept
// between runs, then removed with its files.
func TestATorrentFromLookUpToRemoval(t *testing.T) {
	a := torrentApp(t)
	src := t.TempDir()
	file := seed(t, src)

	if _, err := a.AddTorrent(AddTorrentRequest{Hash: strings.Repeat("ab", 20)}); err == nil {
		t.Error("added without a look-up")
	}
	p, err := a.LookUpTorrent(file)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Pack" || len(p.Files) != 2 || p.Total != 49000 || p.InList {
		t.Errorf("%+v", p)
	}
	if _, err := a.LookUpTorrent("relative.torrent"); !errors.Is(err, torrent.ErrNotATorrent) {
		t.Error(err)
	}
	row, err := a.AddTorrent(AddTorrentRequest{Hash: p.Hash, Directory: src, Paused: true})
	if err != nil {
		t.Fatal(err)
	}
	if row.Folder != filepath.Join(src, "Pack") || row.Stats.Phase != torrent.Paused {
		t.Errorf("%+v", row)
	}
	if _, err := a.AddTorrent(AddTorrentRequest{Hash: p.Hash, Directory: src}); err == nil {
		t.Error("added twice")
	}
	if again, _ := a.LookUpTorrent(file); !again.InList {
		t.Error("not in the list")
	}

	if err := a.ResumeTorrent(p.Hash); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		rows, _ := a.ListTorrents()
		if len(rows) == 1 && rows[0].Finished && rows[0].Stats.Phase == torrent.Paused {
			if rows[0].Stats.Done != 49000 {
				t.Errorf("%+v", rows[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not finished and stopped: %+v", rows)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if link, err := a.TorrentMagnet(p.Hash); err != nil || !strings.HasPrefix(link, "magnet:?xt=urn:btih:"+p.Hash) {
		t.Error(link, err)
	}
	if v, err := a.TorrentFiles(p.Hash); err != nil || len(v.Files) != 2 || v.Chosen != nil {
		t.Error(v, err)
	}

	// Kept between runs: a new session finds it, finished and paused.
	a.stopTorrents()
	a.torrents.list = nil
	if rows, _ := a.ListTorrents(); len(rows) != 1 || !rows[0].Finished || rows[0].Hash != p.Hash {
		t.Errorf("%+v", rows)
	}

	// Removed while the session is stopped: its files go too.
	if err := a.RemoveTorrent(p.Hash, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "Pack")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("its folder stayed: %v", err)
	}
	if rows, _ := a.ListTorrents(); len(rows) != 0 {
		t.Errorf("%+v", rows)
	}
	if err := a.RemoveTorrent(p.Hash, false); !errors.Is(err, torrent.ErrUnknown) {
		t.Error(err)
	}
}

func TestTorrentsAreRefusedBehindAFixedProxy(t *testing.T) {
	a := torrentApp(t)
	n := defaultNetworkSettings()
	n.ProxyMode, n.ProxyURL = "fixed", "http://127.0.0.1:3128"
	if _, err := a.SaveNetworkSettings(n); err != nil {
		t.Fatal(err)
	}
	p, err := a.LookUpTorrent(seed(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddTorrent(AddTorrentRequest{Hash: p.Hash}); !errors.Is(err, torrent.ErrProxy) {
		t.Error(err)
	}
	if _, err := a.LookUpTorrent("magnet:?xt=urn:btih:" + p.Hash); !errors.Is(err, torrent.ErrProxy) {
		t.Error(err)
	}
}
