// Ported from Mister Download Manager, crates/tools/src/video.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeYtDlp answers as yt-dlp does: -J with a page's information;
// --load-info-json by saving the file under its -o name, with our progress,
// quality and final-path lines (slow: then it runs on, never finishing).
func fakeYtDlp(slow bool) {
	args := os.Args[1:]
	if slices.Contains(args, "-J") {
		fmt.Print(`{"title": "A talk", "formats": [{"format_id": "18", "ext": "mp4", "height": 360, "url": "https://media.test/v.mp4"}]}`)
		return
	}
	i := slices.Index(args, "-o")
	if i < 0 || !slices.Contains(args, "--load-info-json") {
		os.Exit(2)
	}
	template := args[i+1]
	stem := strings.ReplaceAll(strings.TrimSuffix(filepath.Base(template), ".%(ext)s"), "%%", "%")
	out := filepath.Join(filepath.Dir(template), stem+".mp4")
	fmt.Println("YFQ 18 640x360")
	fmt.Println("YFP 50 100 NA 10")
	if slow {
		time.Sleep(time.Minute)
		return
	}
	_ = os.WriteFile(out, []byte("a video"), 0o644)
	fmt.Println("YFP 100 100 NA 10")
	fmt.Println("YFF " + out)
}

func waitFor(t *testing.T, job *VideoJob, done func(VideoProgress) bool) VideoProgress {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p := job.Progress(); done(p) {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("still %+v", job.Progress())
	return VideoProgress{}
}

// A whole video download through the fake yt-dlp: looked up, downloaded
// under our name (a % in the title kept as text), its size the saved
// file's.
func TestAVideoIsLookedUpAndSaved(t *testing.T) {
	self, _ := os.Executable()
	t.Setenv(fakeModeEnv, "ytdlp")
	dir := t.TempDir()
	job, err := StartVideo(VideoOptions{Exe: self, URL: "https://www.youtube.com/watch?v=x", Selector: "b[height=360]",
		Dir: dir, Stem: "A talk 100%", Expected: 100})
	if err != nil {
		t.Fatal(err)
	}
	p := waitFor(t, job, func(p VideoProgress) bool { return p.State != VideoRunning })
	want := filepath.Join(dir, "A talk 100%.mp4")
	if p.State != VideoDone || p.File != want || p.Downloaded != 7 || p.Total != 7 {
		t.Errorf("progress %+v", p)
	}
	if b, _ := os.ReadFile(want); string(b) != "a video" {
		t.Errorf("file %q", b)
	}
}

// Stopping ends yt-dlp: the download is stopped, not failed, and nothing
// goes on.
func TestAVideoDownloadIsStopped(t *testing.T) {
	self, _ := os.Executable()
	t.Setenv(fakeModeEnv, "ytdlp-slow")
	job, err := StartVideo(VideoOptions{Exe: self, URL: "https://www.youtube.com/watch?v=x", Selector: "b", Dir: t.TempDir(), Stem: "x"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, job, func(p VideoProgress) bool { return p.Downloaded == 50 })
	start := time.Now()
	job.Stop()
	time.Sleep(300 * time.Millisecond)
	if p := job.Progress(); p.State != VideoStopped || p.Error != "" || time.Since(start) > 5*time.Second {
		t.Errorf("stopped %+v", p)
	}
}

func meterRun(expected int64, lines []string) [][2]int64 {
	m := newMeter(expected)
	var p VideoProgress
	var out [][2]int64
	for _, l := range lines {
		m.line(l, &p)
		out = append(out, [2]int64{p.Downloaded, p.Total})
	}
	return out
}

// A fragmented 1080p stream whose estimate grows, then the separate audio
// from 0: the bar only moves forward, and ends at the whole.
func TestVideoThenAudioNeverMovesBack(t *testing.T) {
	lines := []string{"100 NA 1000 5", "300 NA 1300 5", "600 NA 1500 5", "1400 NA 1600 5",
		"1600 1600 NA 5", "50 200 NA 5", "150 200 NA 5", "200 200 NA 5"}
	for _, expected := range []int64{0, 1800} {
		v := meterRun(expected, lines)
		prev := 0.0
		for _, x := range v {
			f := float64(x[0]) / float64(x[1])
			if f < prev-1e-9 {
				t.Errorf("went back: %v", v)
			}
			prev = f
		}
		last := v[len(v)-1]
		if last[0] != 1800 || math.Abs(float64(last[0])/float64(last[1])-1) > 1e-9 {
			t.Errorf("end %v", last)
		}
	}
}

// The size the window showed counts both streams from the start.
func TestExpectedSizeHoldsTheBarBack(t *testing.T) {
	if v := meterRun(1000, []string{"400 400 NA 0"}); v[0] != [2]int64{400, 1000} {
		t.Errorf("%v", v)
	}
	if v := meterRun(0, []string{"NA NA NA NA"}); v[0] != [2]int64{0, 0} {
		t.Errorf("%v", v)
	}
}

func TestSubtitleArguments(t *testing.T) {
	s := func(langs []string, automatic, embed, ffmpeg bool) string {
		return strings.Join(subtitleArgs(Subs{Langs: langs, Automatic: automatic, Embed: embed}, ffmpeg), " ")
	}
	cases := []struct{ got, want string }{
		{s(nil, false, false, true), ""},
		{s([]string{"fr"}, false, false, true), "--write-subs --sub-langs fr --sub-format srt/best --convert-subs srt --ignore-errors"},
		{s([]string{"fr"}, false, false, false), "--write-subs --sub-langs fr --sub-format srt/vtt/best --ignore-errors"},
		{s([]string{"en-orig"}, true, true, true), "--write-auto-subs --sub-langs en-orig --sub-format srt/best --convert-subs srt --embed-subs --ignore-errors"},
		{s([]string{AllUploaders}, false, true, true), "--sub-langs all,-live_chat --sub-format srt/best --convert-subs srt --embed-subs --ignore-errors"},
		// Embedding needs ffmpeg: without it they're saved beside the video.
		{s([]string{"de", "fr"}, false, true, false), "--write-subs --sub-langs de,fr --sub-format srt/vtt/best --ignore-errors"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got  %q\nwant %q", c.got, c.want)
		}
	}
}

// yt-dlp's warnings for subtitles it couldn't get: each language once, a
// site's "too many requests" told apart from other reasons.
func TestRefusedSubtitlesFromTheWarnings(t *testing.T) {
	w := []string{
		"Unable to download video subtitles for 'en': HTTP Error 429: Too Many Requests",
		"[youtube] abc: Some formats are missing",
		"Unable to download video subtitles for 'de': HTTP Error 404: Not Found",
		"Unable to download video subtitles for 'en': HTTP Error 429: Too Many Requests",
	}
	want := []subFailure{{"en", true, "HTTP Error 429: Too Many Requests"}, {"de", false, "HTTP Error 404: Not Found"}}
	if got := subtitleFailures(w); !reflect.DeepEqual(got, want) {
		t.Errorf("%+v", got)
	}
	if len(subtitleFailures(nil)) != 0 {
		t.Error("none")
	}
	// The retry fetches only those, beside the video, never into it.
	r := subsRetry{automatic: true, embed: true, ffmpeg: true, base: []string{"-o", "x.%(ext)s"}}
	if got := strings.Join(r.args([]string{"en"}), " "); got != "-o x.%(ext)s --skip-download --write-auto-subs --sub-langs en --sub-format srt/best --convert-subs srt --ignore-errors" {
		t.Errorf("retry %q", got)
	}
	info := map[string]any{"automatic_captions": map[string]any{"en": []any{map[string]any{"name": "English"}}}}
	if subName(info, "en") != "English" || subName(info, "ru") != "ru" {
		t.Error("subName")
	}
}

func TestSideFilesAreTheVideosSubtitles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Talk.mp4", "Talk.fr.srt", "Talk.en-orig.vtt", "Talk.part", "Talk 2.en.srt", "Other.fr.srt", "Talk.jpg"} {
		_ = os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	var names []string
	for _, p := range sideFiles(filepath.Join(dir, "Talk.mp4")) {
		names = append(names, filepath.Base(p))
	}
	if !reflect.DeepEqual(names, []string{"Talk.en-orig.vtt", "Talk.fr.srt"}) {
		t.Errorf("%q", names)
	}
}

func TestAMissingQualityFallsBackToTheNearestBelow(t *testing.T) {
	if got := relaxed("bv*[height=1080]+ba/b[height=1080]"); got != "bv*[height<=1080]+ba/b[height<=1080]/bv*+ba/bv*" {
		t.Error(got)
	}
	if relaxed("ba/b") != "" || relaxed("b") != "" {
		t.Error("no height, no fallback")
	}
	ours := []string{"-f", "bv*[height=720]+ba/b[height=720]", "-o", "x"}
	if selectorOf(ours) != "bv*[height=720]+ba/b[height=720]" || !slices.Equal(withSelector(ours, "b"), []string{"-f", "b", "-o", "x"}) {
		t.Error("selector")
	}
}

// The saved file found by our own output name: the media file, not its
// subtitles, its unfinished part or another video's file; % in a title as
// it was written.
func TestTheSavedFileIsFoundByItsName(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Şirin 100% ğüş.mp4", "Şirin 100% ğüş.ru.srt", "Şirin 100% ğüş.f137.mp4.part", "Other.mp4"} {
		_ = os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	template := filepath.Join(dir, templateText("Şirin 100% ğüş")+".%(ext)s")
	if got := savedByName([]string{"-o", template}); filepath.Base(got) != "Şirin 100% ğüş.mp4" {
		t.Errorf("%q", got)
	}
	if got := savedByName([]string{"-o", filepath.Join(dir, "Missing.%(ext)s")}); got != "" {
		t.Errorf("%q", got)
	}
	if templateText("100% fun") != "100%% fun" {
		t.Error("templateText")
	}
}
