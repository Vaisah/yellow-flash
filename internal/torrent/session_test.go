// Ported from Mister Download Manager, crates/torrent/src/lib.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

// makeTorrent writes a folder "Pack" of files in dir and is a .torrent for
// it (16 KiB pieces, a tracker on this computer and one on the internet).
func makeTorrent(t *testing.T, dir string, sizes ...int) []byte {
	t.Helper()
	root := filepath.Join(dir, "Pack")
	r := rand.New(rand.NewPCG(1, 2))
	for i, n := range sizes {
		b := make([]byte, n)
		for j := range b {
			b[j] = byte(r.Uint32())
		}
		p := filepath.Join(root, "sub", "f"+strconv.Itoa(i)+".bin")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	return marshal(t, info, "http://127.0.0.1:1/announce", "udp://tracker.example:1337/announce")
}

func marshal(t *testing.T, info metainfo.Info, trackers ...string) []byte {
	t.Helper()
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: ib}
	for _, tr := range trackers {
		mi.AnnounceList = append(mi.AnnounceList, []string{tr})
	}
	if len(trackers) > 0 {
		mi.Announce = trackers[0]
	}
	var b bytes.Buffer
	if err := mi.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func session(t *testing.T) *Torrents {
	t.Helper()
	cfg := NewConfig(t.TempDir())
	cfg.Offline = true
	s, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func phase(s *Torrents, hash string) Phase {
	st, _ := s.Stats(hash)
	return st.Phase
}

func addr(s *Torrents) string { return "127.0.0.1:" + strconv.Itoa(s.ListenPort()) }

func TestATorrentIsReadWithItsFiles(t *testing.T) {
	src := t.TempDir()
	b := makeTorrent(t, src, 40000, 5)
	m, err := MetaFromTorrent(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Pack" || len(m.Files) != 2 || m.Files[0].Path != "sub/f0.bin" || m.Files[1].Size != 5 || m.Total() != 40005 || m.PieceLength != 16<<10 {
		t.Errorf("%+v", m)
	}
	if m.FolderIn("x") != filepath.Join("x", "Pack") {
		t.Error(m.FolderIn("x"))
	}
	if got := m.ExistingIn(src); len(got) != 2 {
		t.Errorf("%q", got)
	}
	if got := m.ExistingIn(t.TempDir()); len(got) != 0 {
		t.Errorf("%q", got)
	}
	link, ok := MagnetLink(b)
	if !ok || !strings.HasPrefix(link, "magnet:?xt=urn:btih:"+m.InfoHash+"&dn=Pack&tr=") {
		t.Error(link)
	}
	for _, bad := range [][]byte{nil, []byte("junk"), []byte("d4:infoi1ee")} {
		if _, err := MetaFromTorrent(bad); !errors.Is(err, ErrNotATorrent) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if !IsMagnet("  MAGNET:?xt=urn:btih:x") || IsMagnet("http://x") {
		t.Error("IsMagnet")
	}
}

func TestUnsafeNamesRefuseATorrent(t *testing.T) {
	for _, path := range [][]string{{"..", "evil"}, {"a", "b\\c"}, {""}} {
		info := metainfo.Info{Name: "x", PieceLength: 16 << 10, Pieces: make([]byte, 20),
			Files: []metainfo.FileInfo{{Path: path, Length: 1}}}
		var u *UnsafeName
		if _, err := MetaFromTorrent(marshal(t, info)); !errors.As(err, &u) {
			t.Errorf("%q: %v", path, err)
		}
	}
	single := metainfo.Info{Name: "..", PieceLength: 16 << 10, Pieces: make([]byte, 20), Length: 1}
	if _, err := MetaFromTorrent(marshal(t, single)); err == nil {
		t.Error("a single file named ..")
	}
}

func TestTorrentFilesAreLimited(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.torrent")
	if err := os.WriteFile(p, make([]byte, MaxTorrentFile+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p); !errors.Is(err, ErrNotATorrent) {
		t.Error(err)
	}
	if err := os.WriteFile(p, []byte("d1:ai1ee"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFile(p); err != nil || string(b) != "d1:ai1ee" {
		t.Error(b, err)
	}
}

func TestAProxyRefusesTorrents(t *testing.T) {
	cfg := NewConfig(t.TempDir())
	cfg.Proxy = "socks5://127.0.0.1:9050"
	if _, err := Start(cfg); !errors.Is(err, ErrProxy) {
		t.Error(err)
	}
}

func TestLimits(t *testing.T) {
	if l, _ := limit(0); l != rate.Inf {
		t.Error(l)
	}
	if l, b := limit(1); float64(l) != MinRate || b != MinRate {
		t.Error(l, b)
	}
	if l, _ := limit(1 << 20); float64(l) != 1<<20 {
		t.Error(l)
	}
}

// Peers a magnet names on this computer or its network are dropped.
func TestMagnetPeersOnTheLocalNetworkAreDropped(t *testing.T) {
	got := peersAllowed([]string{"127.0.0.1:6881", "192.168.0.2:1", "8.8.8.8:6881", "[2001:4860::1]:6881", "[::1]:1", "junk"})
	if !slices.Equal(got, []string{"8.8.8.8:6881", "[2001:4860::1]:6881"}) {
		t.Errorf("%q", got)
	}
}

// A seeder and a leecher on loopback: the leecher gets the files, pauses,
// resumes, and removes them.
func TestALeecherGetsTheFilesFromASeeder(t *testing.T) {
	src := t.TempDir()
	b := makeTorrent(t, src, 100000, 3000)
	seeder, leecher := session(t), session(t)
	hash, err := seeder.Add(b, src, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the seeder to check its files", func() bool { return phase(seeder, hash) == Finished })

	// Only the trackers allowed are kept.
	seeder.mu.Lock()
	kept := TrackersListed(seeder.items[hash].torrent)
	seeder.mu.Unlock()
	if !slices.Equal(kept, []string{"udp://tracker.example:1337/announce"}) {
		t.Errorf("%q", kept)
	}

	dst := t.TempDir()
	if h, err := leecher.Add(b, dst, nil, false); err != nil || h != hash {
		t.Fatal(h, err)
	}
	if again, err := leecher.Add(b, dst, nil, false); err != nil || again != hash || len(leecher.Hashes()) != 1 {
		t.Fatal("added twice", err)
	}
	leecher.addPeer(hash, addr(seeder))
	waitFor(t, "the download", func() bool { return phase(leecher, hash) == Finished })
	st, _ := leecher.Stats(strings.ToUpper(hash))
	if st.Done != 103000 || st.Total != 103000 || len(st.FileDone) != 2 {
		t.Errorf("%+v", st)
	}
	for _, f := range []string{"f0.bin", "f1.bin"} {
		want, _ := os.ReadFile(filepath.Join(src, "Pack", "sub", f))
		got, err := os.ReadFile(filepath.Join(dst, "Pack", "sub", f))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs (%v)", f, err)
		}
	}
	waitFor(t, "the seeder to count the upload", func() bool {
		st, _ := seeder.Stats(hash)
		return st.Uploaded > 0
	})

	if err := leecher.Pause(hash); err != nil {
		t.Fatal(err)
	}
	if st, _ := leecher.Stats(hash); st.Phase != Paused || st.Done != 103000 || leecher.Peers(hash) != nil {
		t.Errorf("paused %+v", st)
	}
	if err := leecher.Resume(hash); err != nil {
		t.Fatal(err)
	}
	// What was checked stays checked.
	waitFor(t, "resumed", func() bool { return phase(leecher, hash) == Finished })

	// A file deleted while paused is fetched again (nothing holds it open).
	if err := leecher.Pause(hash); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dst, "Pack", "sub", "f1.bin")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := leecher.Resume(hash); err != nil {
		t.Fatal(err)
	}
	// f1.bin, and the piece it shares with f0.bin's end.
	if st, _ := leecher.Stats(hash); st.Done != 6*16<<10 || st.FileDone[1] != 0 || st.Phase != Downloading {
		t.Errorf("after the deletion %+v", st)
	}
	leecher.addPeer(hash, addr(seeder))
	waitFor(t, "the deleted file", func() bool { return phase(leecher, hash) == Finished })
	want, _ := os.ReadFile(filepath.Join(src, "Pack", "sub", "f1.bin"))
	if got, _ := os.ReadFile(gone); !bytes.Equal(got, want) {
		t.Error("f1.bin differs")
	}

	if err := leecher.Remove(hash, true); err != nil {
		t.Fatal(err)
	}
	if leecher.Has(hash) || len(leecher.Hashes()) != 0 {
		t.Error("still there")
	}
	if _, err := os.Stat(filepath.Join(dst, "Pack")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder stayed: %v", err)
	}
	if _, err := leecher.Stats(hash); !errors.Is(err, ErrUnknown) {
		t.Error(err)
	}
	if err := leecher.Remove(hash, false); !errors.Is(err, ErrUnknown) {
		t.Error(err)
	}
}

// Only the files chosen are fetched; a torrent added paused waits.
func TestOnlyTheFilesChosen(t *testing.T) {
	src := t.TempDir()
	// Each file a whole number of pieces, so none shares one.
	b := makeTorrent(t, src, 64<<10, 32<<10)
	seeder, leecher := session(t), session(t)
	hash, _ := seeder.Add(b, src, nil, false)
	waitFor(t, "the seeder", func() bool { return phase(seeder, hash) == Finished })

	dst := t.TempDir()
	if _, err := leecher.Add(b, dst, []int{1}, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := leecher.Stats(hash); st.Phase != Paused || st.Total != 32<<10 || st.Done != 0 {
		t.Errorf("%+v", st)
	}
	if err := leecher.Resume(hash); err != nil {
		t.Fatal(err)
	}
	leecher.addPeer(hash, addr(seeder))
	waitFor(t, "the chosen file", func() bool { return phase(leecher, hash) == Finished })
	st, _ := leecher.Stats(hash)
	if st.Done != 32<<10 || st.FileDone[0] != 0 {
		t.Errorf("%+v", st)
	}
	if _, err := os.Stat(filepath.Join(dst, "Pack", "sub", "f1.bin")); err != nil {
		t.Error(err)
	}
}

// A magnet's file list comes from the peers; the trackers it names (only
// those allowed) are in the .torrent made of it.
func TestAMagnetsFileListComesFromThePeers(t *testing.T) {
	src := t.TempDir()
	b := makeTorrent(t, src, 20000)
	seeder, asker := session(t), session(t)
	hash, _ := seeder.Add(b, src, nil, false)
	waitFor(t, "the seeder", func() bool { return phase(seeder, hash) == Finished })

	magnet := "magnet:?xt=urn:btih:" + hash + "&dn=Pack&tr=" + url.QueryEscape("http://192.168.1.1/a") +
		"&tr=" + url.QueryEscape("https://t.example/announce") + "&x.pe=" + addr(seeder)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, err := asker.Metadata(ctx, magnet)
	if err != nil {
		t.Fatal(err)
	}
	if m.InfoHash != hash || m.Name != "Pack" || m.Total() != 20000 {
		t.Errorf("%+v", m)
	}
	if got := TrackersListed(m.Torrent); !slices.Equal(got, []string{"https://t.example/announce"}) {
		t.Errorf("%q", got)
	}
	// Nothing is kept of the look-up.
	if len(asker.Hashes()) != 0 || len(asker.cl.Torrents()) != 0 {
		t.Error("the look-up stayed")
	}

	// No peer: it gives up when told to.
	short, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if _, err := asker.Metadata(short, "magnet:?xt=urn:btih:"+strings.Repeat("ab", 20)); !errors.Is(err, ErrMetadataTimeout) {
		t.Error(err)
	}
	if _, err := asker.Metadata(ctx, "http://x"); !errors.Is(err, ErrNotATorrent) {
		t.Error(err)
	}
}
