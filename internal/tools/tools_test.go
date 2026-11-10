// Ported from Mister Download Manager, crates/tools/src/lib.rs and chosen.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary is also a fake helper: with fakeHelperEnv set it prints
// that as its version and exits.
const fakeHelperEnv = "YELLOWFLASH_FAKE_HELPER"

// fakeModeEnv makes the test binary another kind of fake helper:
//   - fail: yt-dlp's warnings and an error on stderr, then exit 1;
//   - sleep: run for a minute;
//   - parent: start itself as child, then run for a minute;
//   - child: append a line to $YELLOWFLASH_FAKE_MARKER every 100 ms;
//   - args: print its arguments, one a line.
const fakeModeEnv = "YELLOWFLASH_FAKE_MODE"

func TestMain(m *testing.M) {
	if v, ok := os.LookupEnv(fakeHelperEnv); ok {
		fmt.Print(v)
		os.Exit(0)
	}
	switch os.Getenv(fakeModeEnv) {
	case "":
		os.Exit(m.Run())
	case "fail":
		fmt.Fprintln(os.Stderr, "[youtube] Extracting URL: https://www.youtube.com/watch?v=x")
		fmt.Fprintln(os.Stderr, "WARNING: unknown cookie version: \"b'v20'\"")
		fmt.Fprintln(os.Stderr, "ERROR: [youtube] x: Sign in to confirm you're not a bot.")
		os.Exit(1)
	case "sleep":
		time.Sleep(time.Minute)
	case "parent":
		// On Windows the helper joins its job just after it starts: its
		// child, started later, is born in the job.
		time.Sleep(500 * time.Millisecond)
		self, _ := os.Executable()
		child := exec.Command(self)
		child.Env = append(os.Environ(), fakeModeEnv+"=child")
		_ = child.Start()
		time.Sleep(time.Minute)
	case "child":
		for range 600 {
			f, err := os.OpenFile(os.Getenv("YELLOWFLASH_FAKE_MARKER"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				_, _ = f.WriteString("x\n")
				_ = f.Close()
			}
			time.Sleep(100 * time.Millisecond)
		}
	case "playlist":
		for i, title := range []string{"One", "Two", "Three"} {
			fmt.Printf(`{"url": "https://v.example/%d", "title": %q, "playlist_index": %d, "playlist_count": 3, "playlist_title": "Talks"}`+"\n", i+1, title, i+1)
		}
	case "ytdlp", "ytdlp-slow":
		fakeYtDlp(os.Getenv(fakeModeEnv) == "ytdlp-slow")
	case "gallery-dl":
		fakeGalleryDl()
	case "args":
		for _, a := range os.Args[1:] {
			fmt.Println(a)
		}
	}
	os.Exit(0)
}

func TestARealReleaseChecksumListIsSignedByThePinnedKey(t *testing.T) {
	sums, sig := read(t, "testdata/SHA2-256SUMS"), read(t, "testdata/SHA2-256SUMS.sig")
	if !SignedByYtDlp(sums, sig) {
		t.Fatal("yt-dlp's real list")
	}
	forged := bytes.Clone(sums)
	forged[0] ^= 1
	if SignedByYtDlp(forged, sig) || SignedByYtDlp(sums, []byte("not a signature")) {
		t.Error("a changed list or a bad signature passed")
	}
}

func TestChecksumsByName(t *testing.T) {
	sums := string(read(t, "testdata/SHA2-256SUMS"))
	exe, ok := SumFor(sums, "yt-dlp.exe")
	if !ok || hex.EncodeToString(exe[:]) != "f5389d3698ecde4ef06f4e6b1ce0294fe9ac1396228c8990b8d3416d6d177a7d" {
		t.Errorf("yt-dlp.exe: %x %v", exe, ok)
	}
	if _, ok := SumFor(sums, "yt-dlp.ex"); ok {
		t.Error("a name it doesn't list")
	}
}

func TestVersionsCompareAsDates(t *testing.T) {
	if !newer("2026.10.26", "2026.9.30") || !newer("2026.10.26.1", "2026.10.26") || newer("2026.10.26", "2026.10.26") {
		t.Error("newer")
	}
}

func TestInstallsSwitchAndKeepOnePreviousVersion(t *testing.T) {
	dir := t.TempDir()
	name := YtDlpAsset()
	for _, v := range []string{"2026.01.01", "2026.02.01"} {
		if _, err := put(dir, "yt-dlp", v, name, []byte(v), "https://x/"); err != nil {
			t.Fatal(err)
		}
	}
	now, err := put(dir, "yt-dlp", "2026.03.01", name, []byte("three"), "https://x/")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := YtDlp(dir); !ok || got != now {
		t.Errorf("installed %+v", got)
	}
	if b, _ := os.ReadFile(now.Exe); string(b) != "three" {
		t.Errorf("program %q", b)
	}
	root := filepath.Join(dir, "yt-dlp")
	if !isDir(filepath.Join(root, "2026.02.01")) {
		t.Error("the previous version is gone")
	}
	if isDir(filepath.Join(root, "2026.01.01")) {
		t.Error("an older version stays")
	}
	for _, bad := range []string{"../evil", "", ".hidden", `a\b`, "c:d"} {
		if _, err := put(dir, "yt-dlp", bad, name, []byte("x"), "https://x/"); err == nil {
			t.Errorf("version %q accepted", bad)
		}
	}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// fakeRelease serves a yt-dlp release: its data, a checksum list naming
// program (or sumsFor when set), a signature ("good" passes the test's
// check) and the program.
type fakeRelease struct {
	srv                 *httptest.Server
	tag                 string
	program, sumsFor    []byte
	sig                 string
	programHits, served atomic.Int64
}

func newFakeRelease(t *testing.T, tag string, program []byte) *fakeRelease {
	f := &fakeRelease{tag: tag, program: program, sig: "good"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "assets": []map[string]string{
				{"name": "SHA2-256SUMS", "browser_download_url": base + "/sums"},
				{"name": "SHA2-256SUMS.sig", "browser_download_url": base + "/sig"},
				{"name": YtDlpAsset(), "browser_download_url": base + "/program"},
			}})
		case "/sums":
			listed := f.program
			if f.sumsFor != nil {
				listed = f.sumsFor
			}
			sum := sha256.Sum256(listed)
			fmt.Fprintf(w, "%x  yt-dlp.tar.gz\n%x  %s\n", sha256.Sum256(nil), sum, YtDlpAsset())
		case "/sig":
			_, _ = w.Write([]byte(f.sig))
		case "/program":
			f.programHits.Add(1)
			rec := &countingWriter{ResponseWriter: w, n: &f.served}
			http.ServeContent(rec, r, "yt-dlp", time.Time{}, bytes.NewReader(f.program))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	saved := ytdlpSource
	ytdlpSource = source{releases: f.srv.URL + "/latest", signed: func(_, sig []byte) bool { return string(sig) == "good" }}
	t.Cleanup(func() { ytdlpSource = saved })
	return f
}

type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	c.n.Add(int64(len(b)))
	return c.ResponseWriter.Write(b)
}

func TestAReleaseIsInstalledOnlyWhenSignedAndMatching(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := newFakeRelease(t, "2026.10.26", []byte("yt-dlp, the first"))
	client := f.srv.Client()

	f.sig = "forged"
	if _, err := InstallYtDlp(ctx, dir, client, nil); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("unsigned: %v", err)
	}
	if _, ok := YtDlp(dir); ok || f.programHits.Load() != 0 {
		t.Fatal("installed, or fetched, from an unsigned list")
	}

	f.sig = "good"
	var progressed int64
	got, err := InstallYtDlp(ctx, dir, client, func(n, _ int64) { progressed = n })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got.Exe); got.Version != "2026.10.26" || string(b) != "yt-dlp, the first" || progressed != int64(len(b)) {
		t.Errorf("installed %+v %q (progress %d)", got, b, progressed)
	}
	// The same release again: nothing fetched.
	hits := f.programHits.Load()
	if again, err := InstallYtDlp(ctx, dir, client, nil); err != nil || again != got || f.programHits.Load() != hits {
		t.Errorf("same release: %+v %v, fetched %d", again, err, f.programHits.Load()-hits)
	}
	// An older one is never installed.
	f.tag = "2026.01.01"
	if older, _ := InstallYtDlp(ctx, dir, client, nil); older != got {
		t.Errorf("downgraded to %+v", older)
	}

	// A newer release whose program doesn't match its signed checksum.
	f.tag, f.program, f.sumsFor = "2026.11.01", []byte("tampered"), []byte("the real one")
	if _, err := InstallYtDlp(ctx, dir, client, nil); !errors.Is(err, ErrBadChecksum) {
		t.Fatalf("tampered: %v", err)
	}
	if now, _ := YtDlp(dir); now != got {
		t.Errorf("switched to a tampered program: %+v", now)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "yt-dlp", "*.download"))
	if len(leftovers) != 0 {
		t.Errorf("left %v", leftovers)
	}

	// A newer, good one: switched to, the previous kept.
	f.sumsFor = nil
	f.program = []byte("yt-dlp, the second")
	next, err := InstallYtDlp(ctx, dir, client, nil)
	if err != nil || next.Version != "2026.11.01" {
		t.Fatalf("newer: %+v %v", next, err)
	}
	if !isDir(filepath.Dir(got.Exe)) {
		t.Error("the previous version is gone")
	}
}

func TestACutOffDownloadResumes(t *testing.T) {
	program := bytes.Repeat([]byte("0123456789"), 10000)
	f := newFakeRelease(t, "2026.10.26", program)
	dir := t.TempDir()
	sum := sha256.Sum256(program)
	root := filepath.Join(dir, "yt-dlp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(root, "yt-dlp-"+hex.EncodeToString(sum[:8])+".download")
	if err := os.WriteFile(partial, program[:60000], 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := InstallYtDlp(context.Background(), dir, f.srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got.Exe); !bytes.Equal(b, program) {
		t.Error("the program differs")
	}
	if f.served.Load() != int64(len(program)-60000) {
		t.Errorf("served %d bytes, %d were missing", f.served.Load(), len(program)-60000)
	}
}

// A chosen program is used first, and our updates leave it alone: no
// download, no network.
func TestChosenProgramsAreUsedAndNotUpdated(t *testing.T) {
	f := newFakeRelease(t, "2099.01.01", []byte("ours"))
	dir := t.TempDir()
	exe := filepath.Join(dir, "my-yt-dlp")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := Installed{Version: "2026.01.01", Exe: exe}
	if err := writePrograms(dir, map[Helper]Installed{HelperYtDlp: mine, HelperFfmpeg: mine, HelperGalleryDl: mine}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []Helper{HelperYtDlp, HelperFfmpeg, HelperGalleryDl} {
		if got, ok := Chosen(dir, h); !ok || got != mine {
			t.Errorf("%s: %+v", h, got)
		}
	}
	got, err := InstallYtDlp(context.Background(), dir, f.srv.Client(), nil)
	if err != nil || got != mine || isDir(filepath.Join(dir, "yt-dlp")) || f.programHits.Load() != 0 {
		t.Errorf("updated a chosen program: %+v %v", got, err)
	}
}

func TestAFileSavedWithAByteOrderMarkIsRead(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ff")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, _ := json.Marshal(exe)
	raw := string([]byte{0xef, 0xbb, 0xbf}) + "{\"ffmpeg\":{\"version\": \"8.0\", \"exe\": " + string(path) + "}}"
	if err := os.WriteFile(filepath.Join(dir, programsFile), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := Chosen(dir, HelperFfmpeg); !ok || got.Exe != exe {
		t.Errorf("chosen %+v", got)
	}
}

func TestChosenProgramsAreKeptAndForgotten(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writePrograms(dir, map[Helper]Installed{HelperFfmpeg: {Version: "8.0", Exe: exe}}); err != nil {
		t.Fatal(err)
	}
	if got, ok := Chosen(dir, HelperFfmpeg); !ok || got.Exe != exe {
		t.Error("ffmpeg not chosen")
	}
	if _, ok := Chosen(dir, HelperYtDlp); ok {
		t.Error("yt-dlp chosen")
	}
	// A program since removed counts as none.
	_ = os.Remove(exe)
	if _, ok := Chosen(dir, HelperFfmpeg); ok {
		t.Error("a removed program")
	}
	if err := Forget(dir, HelperFfmpeg); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPrograms(dir)[HelperFfmpeg]; ok {
		t.Error("not forgotten")
	}
}

func TestVersionsAreReadAsEachProgramPrintsThem(t *testing.T) {
	cases := []struct {
		h        Helper
		out, v   string
		accepted bool
	}{
		{HelperYtDlp, "2026.10.26\n", "2026.10.26", true},
		{HelperFfmpeg, "ffmpeg version 8.0-full_build-www.gyan.dev Copyright (c) 2000-2025\n", "8.0-full_build-www.gyan.dev", true},
		{HelperFfmpeg, "ffmpeg version n9.0.2-47-gb204e94e90 Copyright\n", "n9.0.2-47-gb204e94e90", true},
		{HelperFfmpeg, "Python 3.13.1\n", "", false}, // another program's answer
		{HelperYtDlp, "Usage: notepad <file> & more\n", "", false},
		{HelperYtDlp, "", "", false},
		{HelperGalleryDl, "\n1.30.0\n\n", "1.30.0", true},
	}
	for _, c := range cases {
		if v, ok := parseVersion(c.h, c.out); v != c.v || ok != c.accepted {
			t.Errorf("%s %q: %q %v", c.h, c.out, v, ok)
		}
	}
}

// A program is chosen once it has answered as that program, gallery-dl in
// a release we can drive.
func TestAProgramIsChosenOnceItAnswers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeHelperEnv, "2026.10.26\n")
	got, err := Choose(ctx, dir, HelperYtDlp, self)
	if err != nil || got.Version != "2026.10.26" {
		t.Fatalf("yt-dlp: %+v %v", got, err)
	}
	if mine, ok := YtDlp(dir); !ok || mine.Exe != self {
		t.Error("not used")
	}
	t.Setenv(fakeHelperEnv, "1.19.3\n")
	if _, err := Choose(ctx, dir, HelperGalleryDl, self); !errors.Is(err, ErrTooOld) {
		t.Errorf("gallery-dl 1.19.3: %v", err)
	}
	t.Setenv(fakeHelperEnv, "Usage: something else\n")
	if _, err := Choose(ctx, dir, HelperFfmpeg, self); !errors.Is(err, ErrNotTheProgram) {
		t.Errorf("not ffmpeg: %v", err)
	}
	if _, err := Choose(ctx, dir, HelperFfmpeg, filepath.Join(dir, "missing")); !errors.Is(err, ErrNotTheProgram) {
		t.Errorf("no program: %v", err)
	}
	for v, old := range map[string]bool{"1.20.0": false, "v1.19.9": true, "1.30.0": false, "0.9": true, "weird": false} {
		if galleryTooOld(v) != old {
			t.Errorf("gallery-dl %s: too old %v", v, !old)
		}
	}
	if strings.Contains(UserAgent, "Mister") {
		t.Error(UserAgent)
	}
}
