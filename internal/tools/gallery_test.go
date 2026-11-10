// Ported from Mister Download Manager, crates/tools/src/gallery.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestARealGalleryDlListIsSignedAndNamesItsProgram(t *testing.T) {
	sums := read(t, "testdata/gallery-dl-SHA256SUMS")
	sum, ok := SumFor(string(sums), "gallery-dl.exe")
	if !ok || hex.EncodeToString(sum[:]) != "4dda3f14efbc8a8e616af1fc7e1e88d795995326b47692453e620117254dec18" {
		t.Errorf("%x", sum)
	}
	if SignedByYtDlp(sums, read(t, "testdata/gallery-dl-SHA256SUMS.sig")) {
		t.Error("yt-dlp's key signed gallery-dl's list")
	}
}

// The browser's User-Agent (else ours) and cookies, as each release takes
// them.
func TestTheBrowsersContextAsEachGalleryDlReleaseTakesIt(t *testing.T) {
	theirs := []string{"--referer", "https://x.test/", "--user-agent", "Firefox/150", "--cookies", "/c.txt"}
	newer, older := GalleryAcceptsOf("1.30.0"), GalleryAcceptsOf("1.20.5")
	for _, c := range []struct {
		got, want []string
	}{
		{galleryBrowserArgs(theirs, newer), []string{"--cookies", "/c.txt", "--user-agent", "Firefox/150"}},
		{galleryBrowserArgs(theirs, older), []string{"--cookies", "/c.txt", "-o", "user-agent=Firefox/150"}},
		{galleryBrowserArgs(nil, newer), []string{"--user-agent", GalleryUserAgent}},
		{galleryBrowserArgs(nil, older), []string{"-o", "user-agent=" + GalleryUserAgent}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if strings.Contains(GalleryUserAgent, "Mister") {
		t.Error(GalleryUserAgent)
	}
}

func TestItsLinesAreFilesSavedOrAlreadyThere(t *testing.T) {
	saved := "/home/x/Pictures/wikimediacommons/Birds/Rhea (de11b288).jpg"
	if runtime.GOOS == "windows" {
		saved = `C:\Users\x\Pictures\wikimediacommons\Birds\Rhea (de11b288).jpg`
	}
	if p, there, ok := galleryLine(saved + "\r\n"); !ok || there || p != saved {
		t.Errorf("saved: %q %v %v", p, there, ok)
	}
	if p, there, ok := galleryLine("# " + saved); !ok || !there || p != saved {
		t.Errorf("there: %q %v %v", p, there, ok)
	}
	for _, other := range []string{"[wikimedia][info] something", ""} {
		if _, _, ok := galleryLine(other); ok {
			t.Errorf("%q", other)
		}
	}
}

func TestALongGalleryOutputIsCountedWithoutHoldingIt(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.jpg")
	_ = os.WriteFile(file, make([]byte, 1000), 0o644)
	var b strings.Builder
	for range 5000 {
		fmt.Fprintf(&b, "%s\n# %s\n", file, file)
	}
	b.WriteString(strings.Repeat("x", 200_000) + "\n")
	j := &GalleryJob{}
	j.read(strings.NewReader(b.String()))
	if j.saved != 5000 || j.there != 5000 || j.progress.Downloaded != 5_000_000 {
		t.Errorf("saved %d, there %d, %d bytes", j.saved, j.there, j.progress.Downloaded)
	}
}

func TestAGalleryIsNamedByItsAddress(t *testing.T) {
	for raw, want := range map[string]string{
		"https://commons.wikimedia.org/wiki/Category:Featured_pictures_of_birds":    "commons.wikimedia.org - Category_Featured pictures of birds",
		"https://www.flickr.com/photos/nasawebbtelescope/albums/72177720313923911/": "flickr.com - 72177720313923911",
		"https://imgur.com/":       "imgur.com",
		"https://x.test/a%2Fb%5Cc": "x.test - a_b_c",
	} {
		u, _ := url.Parse(raw)
		if got := GalleryName(u); got != want {
			t.Errorf("%s: %q", raw, got)
		}
	}
}

// A whole gallery through a fake gallery-dl: files saved, one already
// there, the folder done; a gallery-dl that fails with nothing saved fails
// the download.
func TestAGalleryIsDownloadedIntoItsFolder(t *testing.T) {
	self, _ := os.Executable()
	folder := filepath.Join(t.TempDir(), "x.test - g")
	t.Setenv(fakeModeEnv, "gallery-dl")
	job, err := StartGallery(Installed{Exe: self, Version: "1.30.0"}, "https://x.test/g", folder, GalleryAsk{Limit: 2}, FromBrowser{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for job.Progress().State == VideoRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	p := job.Progress()
	if p.State != VideoDone || p.File != folder || p.Downloaded != 6 || len(job.Saved()) != 2 {
		t.Errorf("progress %+v, saved %q", p, job.Saved())
	}
	if len(p.Events) == 0 || p.Events[0].Text != "gallery-dl saved 2 files (1 already there)" {
		t.Errorf("events %+v", p.Events)
	}
	t.Setenv(fakeModeEnv, "fail")
	job, err = StartGallery(Installed{Exe: self, Version: "1.30.0"}, "https://x.test/g", folder, GalleryAsk{}, FromBrowser{})
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for job.Progress().State == VideoRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p := job.Progress(); p.State != VideoFailed || !strings.Contains(p.Error, "Sign in") {
		t.Errorf("failed %+v", p)
	}
}

// fakeGalleryDl saves two files into --directory and names a third as
// already there.
func fakeGalleryDl() {
	args := os.Args[1:]
	i := slices.Index(args, "--directory")
	if i < 0 || slices.Index(args, "--range") < 0 || !slices.Contains(args, "--no-input") {
		os.Exit(2)
	}
	dir := args[i+1]
	for _, n := range []string{"a.jpg", "b.jpg"} {
		p := filepath.Join(dir, n)
		_ = os.WriteFile(p, []byte("abc"), 0o644)
		fmt.Println(p)
	}
	fmt.Println("# " + filepath.Join(dir, "c.jpg"))
}
