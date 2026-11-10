// Ported from Mister Download Manager, crates/app/src/torrents.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheListIsKeptBetweenRuns(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenList(dir)
	if err != nil || len(l.All()) != 0 {
		t.Fatal(l.All(), err)
	}
	now := time.Now()
	if err := l.Add(Record{Hash: "BB", Name: "second", Added: now.Add(time.Second)}, []byte("d2:bbe")); err != nil {
		t.Fatal(err)
	}
	if err := l.Add(Record{Hash: "aa", Name: "first", Dir: "x", Files: []int{1}, Added: now}, []byte("d2:aae")); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Update("AA", func(r *Record) { r.Paused, r.Uploaded = true, 7 }); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := l.Update("cc", func(*Record) {}); ok {
		t.Error("updated what isn't there")
	}
	// Noted in memory, kept at the next save.
	if !l.Note("bb", func(r *Record) { r.SeededSecs = 9 }) || l.Note("cc", func(*Record) {}) {
		t.Error("Note")
	}
	if l, _ := OpenList(dir); func() int64 { r, _ := l.Get("bb"); return r.SeededSecs }() != 0 {
		t.Error("noted, already kept")
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := OpenList(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := again.All()
	if len(all) != 2 || all[0].Name != "first" || !all[0].Paused || all[0].Uploaded != 7 || all[0].Files[0] != 1 || all[1].Hash != "bb" || all[1].SeededSecs != 9 {
		t.Errorf("%+v", all)
	}
	if b, err := again.Torrent("AA"); err != nil || string(b) != "d2:aae" {
		t.Error(string(b), err)
	}

	if err := again.Delete("aa"); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get("aa"); ok {
		t.Error("still there")
	}
	if _, err := os.Stat(again.TorrentPath("aa")); !os.IsNotExist(err) {
		t.Error("its .torrent stayed")
	}
	// A record whose .torrent is gone is no torrent.
	if err := os.Remove(again.TorrentPath("bb")); err != nil {
		t.Fatal(err)
	}
	if l, _ := OpenList(dir); len(l.All()) != 0 {
		t.Errorf("%+v", l.All())
	}
	if err := os.WriteFile(filepath.Join(dir, "torrents.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenList(dir); err == nil {
		t.Error("a broken list read")
	}
}

func TestSharingLimits(t *testing.T) {
	none := SharingLimits{}
	if none.Reached(1<<40, 1, 1<<40) {
		t.Error("no limits")
	}
	ratio := SharingLimits{Ratio: 2}
	if ratio.Reached(199, 100, 0) || !ratio.Reached(200, 100, 0) || ratio.Reached(5, 0, 0) {
		t.Error("ratio")
	}
	secs := SharingLimits{Secs: 60}
	if secs.Reached(0, 0, 59) || !secs.Reached(0, 0, 60) {
		t.Error("time")
	}
}

func TestTheAccountantAddsUpRunAfterRun(t *testing.T) {
	var a Accountant
	steps := []struct{ live, want int64 }{{10, 10}, {25, 15}, {25, 0}, {4, 4}, {6, 2}}
	for _, s := range steps {
		if got := a.Sent("h", s.live); got != s.want {
			t.Errorf("live %d: %d, want %d", s.live, got, s.want)
		}
	}
	a.Forget("h")
	if got := a.Sent("h", 6); got != 6 {
		t.Error(got)
	}
}

func TestTheBlocklistIsKeptForItsAddress(t *testing.T) {
	dir := t.TempDir()
	url := "https://lists.example/level1.gz"
	now := time.Now()
	if BlocklistFresh(dir, url, now) || LoadBlocklist(dir, url) != nil {
		t.Error("none yet")
	}
	if _, ok, err := StoreBlocklist(dir, url, []byte("<html>")); ok || err != nil {
		t.Error("not a list", ok, err)
	}
	list, ok, err := StoreBlocklist(dir, url, []byte("Bad:1.2.3.0-1.2.3.255\n"))
	if !ok || err != nil || len(list.Ranges) != 1 {
		t.Fatal(list, ok, err)
	}
	if !BlocklistFresh(dir, url, now) || BlocklistFresh(dir, url, now.Add(BlocklistDays*24*time.Hour+time.Minute)) {
		t.Error("fresh")
	}
	if BlocklistFresh(dir, "https://other.example/list", now) || LoadBlocklist(dir, "https://other.example/list") != nil {
		t.Error("another address")
	}
	if got := LoadBlocklist(dir, url); got == nil || len(got.Ranges) != 1 || got.Ranges[0].Start.String() != "1.2.3.0" {
		t.Errorf("%+v", got)
	}
	if LoadBlocklist(dir, "") != nil {
		t.Error("none asked")
	}
}
