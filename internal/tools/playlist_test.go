// Ported from Mister Download Manager, crates/tools/src/playlist.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const playlistLine = `{"_type": "url", "ie_key": "Youtube", "id": "Zezt6EsG9WE", "url": "https://www.youtube.com/watch?v=Zezt6EsG9WE", "title": "NASA Moon Base", "duration": 558, "playlist_index": 7, "playlist_count": 3912, "playlist_title": "NASA - Videos"}`

func TestLengthsReadAsClocks(t *testing.T) {
	for secs, want := range map[float64]string{558: "9:18", 3725.4: "1:02:05", 7: "0:07"} {
		if got := Clock(secs); got != want {
			t.Errorf("%v: %s", secs, got)
		}
	}
}

func TestAnEntryAndWhatItSaysOfItsList(t *testing.T) {
	e, total, title, ok := parseLine([]byte(playlistLine), 1)
	if !ok || e != (Entry{Index: 7, URL: "https://www.youtube.com/watch?v=Zezt6EsG9WE", Title: "NASA Moon Base", Duration: 558}) ||
		total != 3912 || title != "NASA - Videos" {
		t.Errorf("%+v %d %q", e, total, title)
	}
	// A private one is kept, marked; a line that is no entry (or no web
	// address) isn't.
	p, _, _, ok := parseLine([]byte(`{"url": "https://www.youtube.com/watch?v=x", "title": "[Private video]"}`), 9)
	if !ok || !p.Unavailable || p.Index != 9 {
		t.Errorf("private %+v", p)
	}
	for _, bad := range []string{"not json", `{"url": "file:///etc/passwd", "title": "x"}`} {
		if _, _, _, ok := parseLine([]byte(bad), 1); ok {
			t.Errorf("%s", bad)
		}
	}
}

// Thousands of lines, a giant one among them, and garbage: read in order,
// the giant one skipped, the list capped at MaxEntries, and quickly.
func TestALongListIsReadInOrderAndCapped(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= MaxEntries+500; i++ {
		fmt.Fprintf(&b, `{"url": "https://v.example/%d", "title": "Video %d", "playlist_index": %d}`+"\n", i, i, i)
		if i == 3 {
			fmt.Fprintf(&b, "{\"title\": \"%s\"}\n", strings.Repeat("x", maxLine*2))
			b.WriteString("garbage\n")
		}
	}
	l := &listed{}
	start := time.Now()
	readEntries(strings.NewReader(b.String()), l, 1)
	if len(l.entries) != MaxEntries || !l.progress.Capped {
		t.Errorf("%d entries, capped %v", len(l.entries), l.progress.Capped)
	}
	if l.entries[3].Title != "Video 4" {
		t.Errorf("the giant line skipped, order kept: %q", l.entries[3].Title)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

// A real listing: entries as the helper prints them, its end said; a
// cancelled one ends with what it had, and no error.
func TestAListingRunsAndCanBeStopped(t *testing.T) {
	self, _ := os.Executable()
	t.Setenv(fakeModeEnv, "playlist")
	ls, err := StartListing(self, "https://www.youtube.com/playlist?list=PLx", FromBrowser{}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !ls.Progress().Done && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	p := ls.Progress()
	if !p.Done || p.Listed != 3 || p.Total != 3 || p.Title != "Talks" || p.Error != "" {
		t.Errorf("progress %+v", p)
	}
	if got := ls.EntriesFrom(1); len(got) != 2 || got[0].Title != "Two" {
		t.Errorf("entries %+v", got)
	}
	t.Setenv(fakeModeEnv, "sleep")
	slow, err := StartListing(self, "https://www.youtube.com/playlist?list=PLx", FromBrowser{}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	slow.Cancel()
	deadline = time.Now().Add(10 * time.Second)
	for !slow.Progress().Done && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p := slow.Progress(); !p.Done || p.Error != "" {
		t.Errorf("cancelled %+v", p)
	}
}
