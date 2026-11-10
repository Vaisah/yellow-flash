// Ported from Mister Download Manager, crates/tools/src/video.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// A video download run by yt-dlp under supervision: our file name, progress
// read from a fixed --progress-template, the final path from --print
// after_move. Stopping kills it; starting again continues (--continue). Each
// start looks the page up afresh (-J), points links at servers that answer
// (servers.go) and downloads from that list (--load-info-json), so the page
// is read once.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VideoState is where a video download is.
type VideoState string

const (
	VideoRunning VideoState = "running"
	VideoDone    VideoState = "done"
	VideoFailed  VideoState = "failed"
	VideoStopped VideoState = "stopped"
)

// Event is a note for the download's log.
type Event struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// SubProblem is a subtitle language the site wouldn't give.
type SubProblem struct {
	// Code is yt-dlp's (en), Name the site's name for it (English; the code
	// when none).
	Code string `json:"code"`
	Name string `json:"name"`
	// TooManyRequests: HTTP 429, the site limits how often subtitles may be
	// fetched. Otherwise Reason is yt-dlp's own words.
	TooManyRequests bool   `json:"tooManyRequests"`
	Reason          string `json:"reason"`
	// Beside: they came on the second try, but beside the video, not in it
	// (they were to be embedded).
	Beside bool `json:"beside"`
}

// VideoProgress is where a video download is.
type VideoProgress struct {
	State VideoState `json:"state"`
	// File is the saved video once done; Error why it failed.
	File  string `json:"file,omitempty"`
	Error string `json:"error,omitempty"`
	// Downloaded and Total are bytes (Total 0: unknown); Speed bytes/s.
	Downloaded int64   `json:"downloaded"`
	Total      int64   `json:"total"`
	Speed      float64 `json:"speed"`
	// Events are notes for the download's log, oldest first.
	Events []Event `json:"events"`
	// SideFiles are its subtitle files saved beside it, once done;
	// SubProblems the subtitles that didn't come (the video is saved all the
	// same).
	SideFiles   []string     `json:"sideFiles"`
	SubProblems []SubProblem `json:"subProblems"`
}

// Subs are the subtitles to save with a video. Langs empty: none.
type Subs struct {
	// Langs are yt-dlp's language codes, or AllUploaders alone for every
	// one the uploader gave.
	Langs []string `json:"langs"`
	// Automatic: the site's automatic captions rather than the uploader's.
	Automatic bool `json:"automatic"`
	// Embed puts them into the video file instead of beside it (needs
	// ffmpeg; otherwise beside it).
	Embed bool `json:"embed"`
}

// AllUploaders is Subs.Langs for all of the uploader's subtitles.
const AllUploaders = "all"

// subExts are the files subtitles are saved as.
var subExts = []string{"srt", "vtt", "ass", "ttml"}

// subsRetryDelay is how long to wait before asking once more for the
// subtitles a site refused: a site answering "too many requests" wants a
// pause. YELLOWFLASH_SUBS_RETRY_SECS shortens it for the tests.
func subsRetryDelay() time.Duration {
	if s, err := strconv.Atoi(os.Getenv("YELLOWFLASH_SUBS_RETRY_SECS")); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return 20 * time.Second
}

type subFailure struct {
	code            string
	tooManyRequests bool
	why             string
}

// subtitleFailures are the subtitle languages yt-dlp said it couldn't
// download (with --ignore-errors it goes on with the video and only warns:
// Unable to download video subtitles for 'en': HTTP Error 429: Too Many
// Requests), and why.
func subtitleFailures(warnings []string) []subFailure {
	const said = "Unable to download video subtitles for '"
	var out []subFailure
	for _, w := range warnings {
		_, rest, ok := strings.Cut(w, said)
		if !ok {
			continue
		}
		code, why, ok := strings.Cut(rest, "'")
		if !ok {
			continue
		}
		why = strings.TrimSpace(strings.TrimLeft(why, ":"))
		f := subFailure{code: code, why: why,
			tooManyRequests: strings.Contains(why, "429") || strings.Contains(strings.ToLower(why), "too many requests")}
		if !slices.ContainsFunc(out, func(o subFailure) bool { return o.code == code }) {
			out = append(out, f)
		}
	}
	return out
}

// subName is the site's name for a subtitle language, from the looked-up
// page (code when none).
func subName(info map[string]any, code string) string {
	for _, k := range []string{"subtitles", "automatic_captions"} {
		m, _ := info[k].(map[string]any)
		files, _ := m[code].([]any)
		if len(files) > 0 {
			f, _ := files[0].(map[string]any)
			if n, ok := f["name"].(string); ok && strings.TrimSpace(n) != "" {
				return strings.TrimSpace(n)
			}
		}
	}
	return code
}

// subtitleArgs are yt-dlp's arguments for subs: written beside the video as
// <stem>.<lang>.srt (converted to SRT by ffmpeg, which every player reads;
// without ffmpeg SRT when the site has it, else its own format), or
// embedded with ffmpeg (yt-dlp removes the separate files then).
func subtitleArgs(subs Subs, ffmpeg bool) []string {
	if len(subs.Langs) == 0 {
		return nil
	}
	langs := strings.Join(subs.Langs, ",")
	if len(subs.Langs) == 1 && subs.Langs[0] == AllUploaders {
		// Not the chat replay of a live stream, which yt-dlp lists with them.
		langs = "all,-live_chat"
	}
	embed := subs.Embed && ffmpeg
	var out []string
	// --embed-subs fetches the uploader's itself, and keeps no files behind
	// when it is the only one asked.
	switch {
	case subs.Automatic:
		out = append(out, "--write-auto-subs")
	case !embed:
		out = append(out, "--write-subs")
	}
	out = append(out, "--sub-langs", langs)
	if ffmpeg {
		out = append(out, "--sub-format", "srt/best", "--convert-subs", "srt")
	} else {
		out = append(out, "--sub-format", "srt/vtt/best")
	}
	if embed {
		out = append(out, "--embed-subs")
	}
	// A subtitle the site refuses must not cost the video: yt-dlp stops on
	// it unless told to go on, and then only warns.
	return append(out, "--ignore-errors")
}

// subsRetry is what a second, subtitles-only run needs: what the first
// asked for, and its own arguments (output name, browser context, ffmpeg).
type subsRetry struct {
	automatic, embed, ffmpeg bool
	base                     []string
}

// args fetch codes again, beside the video (a second run can't put them
// into a video already saved).
func (r subsRetry) args(codes []string) []string {
	out := append(slices.Clone(r.base), "--skip-download")
	return append(out, subtitleArgs(Subs{Langs: codes, Automatic: r.automatic}, r.ffmpeg)...)
}

// sideFiles are the subtitle files saved beside video
// (<stem>.<lang>.<srt|vtt…>, as yt-dlp names them).
func sideFiles(video string) []string {
	dir := filepath.Dir(video)
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(e.Name()), "."))
		if p != video && e.Type().IsRegular() && strings.HasPrefix(e.Name(), stem+".") && slices.Contains(subExts, ext) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// selectorOf is the format selector in yt-dlp's arguments (-f <selector>).
func selectorOf(ours []string) string {
	if i := slices.Index(ours, "-f"); i >= 0 && i+1 < len(ours) {
		return ours[i+1]
	}
	return ""
}

// withSelector is ours with another format selector.
func withSelector(ours []string, selector string) []string {
	out := slices.Clone(ours)
	if i := slices.Index(out, "-f"); i >= 0 && i+1 < len(out) {
		out[i+1] = selector
	}
	return out
}

// relaxed is the nearest quality to a choice from the window
// (bv*[height=1080]+ba/b[height=1080]) when that one isn't offered (the
// servers this computer reaches don't serve it): the best at or below that
// height, else the best video there is, always a video. "" when the choice
// names no height.
func relaxed(selector string) string {
	if !strings.Contains(selector, "[height=") {
		return ""
	}
	return strings.ReplaceAll(selector, "[height=", "[height<=") + "/bv*+ba/bv*"
}

// meter makes yt-dlp's progress lines one steady figure. Each stream of a
// merged download (video, then audio) reports from 0 with its own total,
// and a fragmented stream's total (HLS, DASH) is an estimate that moves with
// every fragment: the streams are added up, the total never falls below the
// size the window showed (expected), and the fraction done never goes back.
type meter struct {
	expected                     int64
	finished, lastTotal, lastGot int64
	frac                         float64
}

func newMeter(expected int64) *meter { return &meter{expected: max(expected, 0)} }

func (m *meter) line(rest string, p *VideoProgress) {
	fields := strings.Fields(rest)
	num := func(i int) (float64, bool) {
		if i >= len(fields) {
			return 0, false
		}
		v, err := strconv.ParseFloat(fields[i], 64)
		return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
	}
	g, _ := num(0)
	got := int64(max(g, 0))
	total, hasTotal := num(1)
	if !hasTotal {
		total, hasTotal = num(2)
	}
	// Back near 0: the next stream started; the one before counts in full.
	if got < m.lastGot && m.lastGot > 0 {
		m.finished += max(m.lastTotal, m.lastGot)
		m.lastTotal = 0
	}
	m.lastGot = got
	if hasTotal {
		m.lastTotal = int64(max(total, 0))
	}
	downloaded := m.finished + got
	whole := max(m.finished+max(m.lastTotal, got), m.expected)
	p.Downloaded = downloaded
	p.Speed, _ = num(3)
	if whole == 0 {
		p.Total = 0
		return
	}
	frac := float64(downloaded) / float64(whole)
	if frac >= m.frac {
		m.frac = frac
		p.Total = whole
	} else {
		// The estimate grew: keep the bar where it was rather than move it
		// back.
		p.Total = int64(max(math.Round(float64(downloaded)/m.frac), float64(downloaded)))
	}
}

// Our lines from yt-dlp: progress (YFP <downloaded> <total> <estimate>
// <speed>, NA when unknown), the final file (YFF <path>), and the quality it
// chose before downloading (YFQ <format id> <resolution>).
const (
	progressTemplate = "download:YFP %(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s %(progress.speed)s"
	finalTemplate    = "after_move:YFF %(filepath)s"
	qualityTemplate  = "video:YFQ %(format_id)s %(resolution)s"
)

// templateText keeps % from being read as a template field by yt-dlp.
func templateText(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// savedByName is the media file yt-dlp saved under our output name
// (<dir>/<stem>.%(ext)s), when it didn't say where, or said it in a form
// that names no file. Its subtitles and unfinished parts don't count; the
// newest one wins.
func savedByName(ours []string) string {
	i := slices.Index(ours, "-o")
	if i < 0 || i+1 >= len(ours) {
		return ""
	}
	template := ours[i+1]
	stem, ok := strings.CutSuffix(filepath.Base(template), ".%(ext)s")
	if !ok {
		return ""
	}
	stem = strings.ReplaceAll(stem, "%%", "%")
	best, newest := "", time.Time{}
	for _, ext := range []string{"mp4", "mkv", "webm", "mov", "m4v", "m4a", "mp3", "opus", "ogg", "oga", "flac", "wav", "aac", "flv", "3gp"} {
		p := filepath.Join(filepath.Dir(template), stem+"."+ext)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && (best == "" || fi.ModTime().After(newest)) {
			best, newest = p, fi.ModTime()
		}
	}
	return best
}

// VideoOptions say what to download and how.
type VideoOptions struct {
	// Exe is yt-dlp; URL the page.
	Exe, URL string
	// Selector is the choice's (Choice.Selector).
	Selector string
	// Dir and Stem: the video is saved as Dir/<Stem>.<ext>.
	Dir, Stem string
	// Rate caps the speed in bytes/s (0: none).
	Rate int64
	// Ffmpeg is ffmpeg's program, to merge separate video and audio into an
	// MP4 ("" without).
	Ffmpeg string
	// Expected is the size the choice was shown with (every stream), so the
	// progress starts from the whole (0: unknown).
	Expected int64
	// Browser is what the browser's request for the page carried.
	Browser FromBrowser
	// Subs are the subtitles to save with it (none for a sound alone).
	Subs Subs
	// Client asks media servers whether they serve a stream (servers.go);
	// nil: http.DefaultClient.
	Client *http.Client
}

// VideoJob is a video download running in the background.
type VideoJob struct {
	mu       sync.Mutex
	progress VideoProgress
	proc     *Proc
	stopped  chan struct{}
	once     sync.Once
}

// StartVideo downloads opts.URL in the chosen format.
func StartVideo(opts VideoOptions) (*VideoJob, error) {
	if err := safe(opts.URL); err != nil {
		return nil, err
	}
	output := filepath.Join(opts.Dir, templateText(opts.Stem)+".%(ext)s")
	// An MP3, Opus or FLAC choice: the best sound, converted by ffmpeg.
	conv, convert := AudioConversion(opts.Selector)
	format, expected := opts.Selector, opts.Expected
	if convert {
		// What comes down is the sound as the site has it, not the file the
		// choice's size estimated: the progress follows yt-dlp's own total.
		format, expected = "ba/b", 0
	}
	// Subtitles go with a picture only.
	withSubs := !convert && len(opts.Subs.Langs) > 0
	ours := []string{"-f", format, "-o", output, "--no-playlist", "--newline",
		// --print makes yt-dlp quiet; this keeps the progress lines.
		"--progress", "--continue", "--no-mtime", "--progress-template", progressTemplate,
		"--print", finalTemplate, "--print", qualityTemplate}
	if opts.Rate > 0 {
		ours = append(ours, "--limit-rate", strconv.FormatInt(opts.Rate, 10))
	}
	// A subtitles-only second run, for the ones a site refuses: the same
	// output name.
	retryBase := []string{"-o", output, "--no-playlist"}
	if convert {
		ours = append(ours, "-x", "--audio-format", conv.Format)
		if conv.Quality != "" {
			ours = append(ours, "--audio-quality", conv.Quality)
		}
	} else {
		ours = append(ours, subtitleArgs(opts.Subs, opts.Ffmpeg != "")...)
	}
	theirs, cookies, err := opts.Browser.Args(opts.URL)
	if err != nil {
		return nil, err
	}
	retryBase = append(retryBase, theirs...)
	ours = append(ours, theirs...)
	if opts.Ffmpeg != "" {
		dir := filepath.Dir(opts.Ffmpeg)
		ours = append(ours, "--ffmpeg-location", dir, "--merge-output-format", "mp4")
		retryBase = append(retryBase, "--ffmpeg-location", dir)
	}
	var retry *subsRetry
	if withSubs {
		retry = &subsRetry{automatic: opts.Subs.Automatic, embed: opts.Subs.Embed && opts.Ffmpeg != "",
			ffmpeg: opts.Ffmpeg != "", base: retryBase}
	}
	for _, a := range append(slices.Clone(ours), retryBase...) {
		if err := safe(a); err != nil {
			cookies.Remove()
			return nil, err
		}
	}
	job := &VideoJob{progress: VideoProgress{State: VideoRunning}, stopped: make(chan struct{})}
	// Whose cookies yt-dlp has, for the log: the browser's name only, never
	// a cookie or the profile (a folder may name the person).
	if b := browserCookies(); b != nil && cookies == nil {
		job.note("Cookies read from " + b.Browser + " on this computer (Options → Sites Logins)")
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	s := &supervisor{job: job, exe: opts.Exe, url: opts.URL, ours: ours, browser: theirs,
		expected: expected, retry: retry, prober: StreamProber{Client: client}}
	go func() {
		// The cookie file lives as long as the run.
		defer cookies.Remove()
		defer func() {
			// A panic in here must not leave the download "running" forever,
			// nor anything downloading unseen.
			if r := recover(); r != nil {
				job.end()
				job.finish(func(p *VideoProgress) {
					p.State, p.Error = VideoFailed, "the video download stopped on an internal error"
				})
			}
		}()
		s.supervise()
	}()
	return job, nil
}

// Progress is where the download is.
func (j *VideoJob) Progress() VideoProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	p := j.progress
	p.Events = slices.Clone(p.Events)
	return p
}

// Stop stops the download (the partial file stays; starting again
// continues it).
func (j *VideoJob) Stop() {
	j.mu.Lock()
	j.progress.State, j.progress.Speed = VideoStopped, 0
	p := j.proc
	j.mu.Unlock()
	j.once.Do(func() { close(j.stopped) })
	if p != nil {
		p.Kill()
	}
}

func (j *VideoJob) isStopped() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progress.State == VideoStopped
}

// note adds a line to the download's log (English, as yt-dlp's own).
func (j *VideoJob) note(text string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.progress.Events = append(j.progress.Events, Event{At: time.Now(), Text: text})
}

// end ends yt-dlp and everything it started: whenever the download is given
// up, nothing may go on downloading unseen.
func (j *VideoJob) end() {
	j.mu.Lock()
	p := j.proc
	j.proc = nil
	j.mu.Unlock()
	if p != nil {
		p.Kill()
	}
}

// finish sets the download's end, unless it was stopped meanwhile.
func (j *VideoJob) finish(set func(*VideoProgress)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.progress.State != VideoStopped {
		j.progress.Speed = 0
		set(&j.progress)
	}
}

// pause waits d, or less if the download is stopped meanwhile (false then).
func (j *VideoJob) pause(d time.Duration) bool {
	select {
	case <-j.stopped:
		return false
	case <-time.After(d):
		return !j.isStopped()
	}
}

type supervisor struct {
	job           *VideoJob
	exe, url      string
	ours, browser []string
	expected      int64
	retry         *subsRetry
	prober        StreamProber
}

// attemptResult is how one run of yt-dlp ended.
type attemptResult struct {
	done     bool
	stopped  bool
	file     string
	problems []SubProblem
	quality  string
	// error and rerouted: it failed; links were moved to other servers for
	// this run.
	error    string
	rerouted bool
}

// supervise runs the download, and again when a run fails in a way a new
// look-up can mend:
//   - a media server yt-dlp couldn't reach (it resolved when looked up, then
//     didn't) is treated as dead, so its streams move to an alternative;
//   - a run whose links had to be moved gets one fresh look-up;
//   - the chosen quality not offered at all falls back to the nearest one
//     below it, said in the log;
//   - no video left at all after the move: once more on the page's own
//     servers, unmoved.
//
// Whenever it is given up, yt-dlp's whole job is ended.
func (s *supervisor) supervise() {
	ours := slices.Clone(s.ours)
	dead := map[string]bool{}
	lookedAgain, reroute := false, true
	relaxedTo := ""
	last := "yt-dlp stopped"
	for range 5 {
		r := s.attempt(ours, dead, reroute)
		if r.stopped {
			return
		}
		s.job.end()
		if r.done {
			if relaxedTo != "" && r.quality != "" {
				s.job.note("Downloaded " + r.quality + " instead")
			}
			// The saved file's own size: a merged video or a converted MP3
			// isn't the size of what came down.
			side := sideFiles(r.file)
			s.job.finish(func(p *VideoProgress) {
				p.State, p.File, p.SideFiles, p.SubProblems = VideoDone, r.file, side, r.problems
				if fi, err := os.Stat(r.file); err == nil {
					p.Total, p.Downloaded = fi.Size(), fi.Size()
				}
			})
			return
		}
		var gone []string
		for _, h := range UnreachableIn(r.error) {
			if !dead[h] {
				gone = append(gone, h)
			}
		}
		unavailable := FormatUnavailable(r.error)
		switch {
		// On the page's own servers (no moving any more), an unreachable
		// one is the end: another look-up would go to it again.
		case len(gone) > 0 && reroute:
			for _, h := range gone {
				s.job.note("The server " + h + " couldn't be reached; looking the page up again without it")
				dead[h] = true
			}
		case unavailable && r.rerouted && !lookedAgain:
			lookedAgain = true
			s.job.note(r.error + "; looking the page up again")
		case unavailable && relaxedTo == "" && relaxed(selectorOf(ours)) != "":
			s.job.note("The chosen quality isn't offered by the servers this computer can reach; downloading the nearest quality below it")
			relaxedTo = relaxed(selectorOf(ours))
			ours = withSelector(ours, relaxedTo)
		case unavailable && r.rerouted && reroute:
			s.job.note("No video the servers this computer can reach serve is left; trying the page's own servers")
			reroute = false
		case r.rerouted && !lookedAgain:
			lookedAgain = true
			s.job.note(r.error + "; looking the page up again")
		default:
			s.job.finish(func(p *VideoProgress) { p.State, p.Error = VideoFailed, r.error })
			return
		}
		last = r.error
	}
	s.job.end()
	s.job.finish(func(p *VideoProgress) { p.State, p.Error = VideoFailed, last })
}

// lookUpInfo is the page's video information from yt-dlp (one video, not a
// playlist), asked as the browser asked for the page, decoded with its
// numbers kept exact; and whether it took looking it up without cookies.
func (s *supervisor) lookUpInfo() (map[string]any, bool, error) {
	args := append([]string{"-J", "--no-playlist", "--no-warnings"}, s.browser...)
	out, err := LookUp(context.Background(), s.exe, args, s.url, 90*time.Second)
	if err != nil {
		return nil, false, err
	}
	d := json.NewDecoder(bytes.NewReader(out.JSON))
	d.UseNumber()
	var info map[string]any
	if d.Decode(&info) != nil || info == nil {
		return nil, false, errors.New("yt-dlp gave no video information")
	}
	return info, out.WithoutCookies, nil
}

// attempt is one run: look the page up, move links off unreachable servers
// (and off dead, the ones an earlier run couldn't reach), download from
// that list. Subtitles the site refused are asked for once more after a
// pause, beside the video; the ones still refused are reported with it,
// never failing it.
func (s *supervisor) attempt(ours []string, dead map[string]bool, reroute bool) attemptResult {
	failed := func(err string, rerouted bool) attemptResult { return attemptResult{error: err, rerouted: rerouted} }
	if s.job.isStopped() {
		return attemptResult{stopped: true}
	}
	info, withoutCookies, err := s.lookUpInfo()
	if err != nil {
		var f *FailedError
		if errors.As(err, &f) {
			return failed(f.Message, false)
		}
		return failed(err.Error(), false)
	}
	if withoutCookies {
		s.job.note("The site offered no format with the browser's cookies; looked the page up without them")
	}
	var changed Rerouted
	if reroute {
		changed = Reroute(info, func(h string) bool { return !dead[h] && Reachable(h) }, s.prober.Serves)
	}
	for _, sw := range changed.Swaps {
		s.job.note("The server " + sw.Dead + " can't be reached; downloading from " + sw.Alternative)
	}
	if len(changed.LeftOut) > 0 {
		s.job.note("Not served by the servers this computer can reach, so left out: " + strings.Join(changed.LeftOut, ", "))
	}
	rerouted := !changed.Empty()
	// A private file for the looked-up page (its links are signed; removed
	// when the run ends).
	f, err := os.CreateTemp("", "yellowflash-video-*.json")
	if err != nil {
		return failed("Cannot write the video information: "+err.Error(), rerouted)
	}
	file := f.Name()
	defer os.Remove(file)
	if err := json.NewEncoder(f).Encode(info); err != nil {
		f.Close()
		return failed("Cannot write the video information: "+err.Error(), rerouted)
	}
	f.Close()
	ran, ok, err := s.run(ours, file, newMeter(s.expected))
	if err != nil {
		return failed(err.Error(), rerouted)
	}
	if !ok {
		return attemptResult{stopped: true}
	}
	var video string
	switch {
	// --ignore-errors (subtitles asked) lets yt-dlp end well with no file:
	// the file itself is the proof.
	case ran.success && ran.saved != "" && isFile(ran.saved):
		video = ran.saved
	// It said where, in a form that names no file, or didn't say: our own
	// name for it.
	case ran.success && savedByName(ours) != "":
		video = savedByName(ours)
	case ran.success && ran.saved != "":
		return failed("yt-dlp said it saved the file, but it isn't there", rerouted)
	case ran.success:
		return failed("yt-dlp didn't say where it saved the file", rerouted)
	default:
		return failed(ran.failure, rerouted)
	}
	done := attemptResult{done: true, file: video, quality: ran.quality}
	refused := subtitleFailures(ran.warnings)
	if len(refused) == 0 {
		return done
	}
	for _, r := range refused {
		why := r.why
		if r.tooManyRequests {
			why = "the site refused (too many requests)"
		}
		s.job.note("Subtitles in " + r.code + " couldn't be downloaded: " + why)
	}
	// Once more after a pause, for the ones refused (a site limiting
	// requests often gives them a little later).
	still := refused
	if s.retry != nil {
		delay := subsRetryDelay()
		s.job.note(fmt.Sprintf("Asking for the subtitles again in %d s", int(delay.Seconds())))
		if !s.job.pause(delay) {
			return done
		}
		var codes []string
		for _, r := range refused {
			codes = append(codes, r.code)
		}
		again, ok, err := s.run(s.retry.args(codes), file, nil)
		if !ok && err == nil {
			return done
		}
		if err == nil && again.success {
			still = subtitleFailures(again.warnings)
		}
	}
	for _, r := range refused {
		i := slices.IndexFunc(still, func(f subFailure) bool { return f.code == r.code })
		switch {
		case i >= 0:
			done.problems = append(done.problems, SubProblem{Code: r.code, Name: subName(info, r.code),
				TooManyRequests: still[i].tooManyRequests, Reason: still[i].why})
		default:
			s.job.note("Subtitles in " + r.code + " came on the second try")
			// Asked into the video, they're beside it now.
			if s.retry != nil && s.retry.embed {
				done.problems = append(done.problems, SubProblem{Code: r.code, Name: subName(info, r.code),
					TooManyRequests: r.tooManyRequests, Reason: r.why, Beside: true})
			}
		}
	}
	return done
}

// ran is how one yt-dlp run on the looked-up page ended.
type ran struct {
	success bool
	// saved is where it said it saved the file; quality what it chose.
	saved, quality string
	warnings       []string
	failure        string
}

// run runs yt-dlp with ours on the page looked up into file, following its
// progress with m (nil: not followed). ok false: stopped before or while it
// ran.
func (s *supervisor) run(ours []string, file string, m *meter) (ran, bool, error) {
	cmd, err := YtDlpInfoCmd(s.exe, nil, ours, file)
	if err != nil {
		return ran{}, false, err
	}
	// Under the job's lock: Stop either came first (nothing starts) or finds
	// the process to kill.
	s.job.mu.Lock()
	if s.job.progress.State == VideoStopped {
		s.job.mu.Unlock()
		return ran{}, false, nil
	}
	p, err := Spawn(cmd, "yt-dlp")
	if err != nil {
		s.job.mu.Unlock()
		return ran{}, false, err
	}
	s.job.proc = p
	s.job.mu.Unlock()
	var r ran
	// Every line to the end, read as bytes: a line that isn't UTF-8 (a title
	// in a legacy code page) is read leniently, and the saved file is also
	// found by its name.
	br := bufio.NewReader(p.Stdout)
	for {
		raw, err := br.ReadBytes('\n')
		line := strings.TrimRight(strings.ToValidUTF8(string(raw), "�"), "\r\n")
		switch {
		case strings.HasPrefix(line, "YFF "):
			r.saved = strings.TrimSpace(line[4:])
		case strings.HasPrefix(line, "YFQ "):
			r.quality = strings.TrimSpace(line[4:])
		case strings.HasPrefix(line, "YFP ") && m != nil:
			s.job.mu.Lock()
			m.line(line[4:], &s.job.progress)
			s.job.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	_, _ = io.Copy(io.Discard, p.Stdout)
	werr := p.Wait()
	if s.job.isStopped() {
		return ran{}, false, nil
	}
	r.success = werr == nil
	r.warnings = p.Warnings()
	var f *FailedError
	switch {
	case errors.As(werr, &f):
		r.failure = f.Message
	case werr != nil:
		r.failure = werr.Error()
	}
	return r, true, nil
}
