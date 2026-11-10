// Ported from Mister Download Manager, crates/tools/src/gallery.rs (downloading)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// A gallery download is a folder: gallery-dl prints each file it saves (one
// path a line, "# path" for one already there), and skips what is already
// there when started again, so a gallery started again fetches only its new
// images. It runs supervised as yt-dlp does (Spawn), so Stop and Yellow
// Flash's end take it and all it started.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/ipc"
)

// GalleryUserAgent is Yellow Flash as sites see it: gallery-dl's own name
// is refused by some (Wikimedia's servers answer 429 to unnamed tools);
// this one says who asks and where to read about it.
var GalleryUserAgent = "YellowFlash/" + ipc.AppVersion + " (+https://github.com/Vaisah/yellow-flash) gallery-dl"

// GalleryHandles reports whether gallery-dl has a way to read pageURL (a
// gallery, a user's page, a post…). Asked offline: its extractors are
// matched against the address, nothing is fetched (-E, about a second).
func GalleryHandles(ctx context.Context, tool Installed, pageURL string) (bool, error) {
	cmd, err := GalleryDlCmd(tool, []string{"--extractor-info"}, pageURL)
	if err != nil {
		return false, err
	}
	_, err = outputCmd(ctx, cmd, "gallery-dl", 30*time.Second)
	var failed *FailedError
	switch {
	case err == nil:
		return true, nil
	// Its words since 1.24, and before (a distribution's may be that old).
	case errors.As(err, &failed) && (strings.Contains(failed.Message, "Unsupported URL") ||
		strings.Contains(failed.Message, "No suitable extractor found")):
		return false, nil
	}
	return false, err
}

// GalleryName is a gallery's name, for its folder and its row: its site and
// the address's last part (commons.wikimedia.org - Category_Birds), else
// its site; safe as a folder name.
func GalleryName(page *url.URL) string {
	host := strings.TrimPrefix(page.Hostname(), "www.")
	if host == "" {
		host = "gallery"
	}
	name := host
	segs := segments(page.EscapedPath())
	if n := len(segs); n > 0 {
		last, err := url.PathUnescape(segs[n-1])
		if err != nil {
			last = segs[n-1]
		}
		name = host + " - " + strings.ReplaceAll(strings.ToValidUTF8(last, "�"), "_", " ")
	}
	name = strings.NewReplacer("/", "_", `\`, "_").Replace(name)
	if safe, err := filesystem.SanitizeFilename(name); err == nil {
		return safe
	}
	return "gallery"
}

// GalleryAsk is what a gallery download is asked for.
type GalleryAsk struct {
	// Limit is at most this many files, the first ones gallery-dl lists (on
	// most sites the newest); 0: all.
	Limit int64
	// Rate is bytes/s for each file; 0: no limit.
	Rate int64
}

// keepPaths are the paths kept of the files saved (to mark them as
// downloaded from the web at the end); a gallery past this many still
// downloads, its later files unmarked.
const keepPaths = 100_000

// GalleryJob is a gallery downloading into a folder.
type GalleryJob struct {
	mu       sync.Mutex
	progress VideoProgress
	// saved this run, there already (skipped).
	saved, there int64
	files        []string
	proc         *Proc
}

// StartGallery downloads pageURL's files into folder, the gallery's own.
func StartGallery(tool Installed, pageURL, folder string, ask GalleryAsk, browser FromBrowser) (*GalleryJob, error) {
	ours := []string{
		// The files right in folder (the gallery's own), not in subfolders of
		// the site's.
		"--directory", folder,
		// One file a second at most: gentle with the sites (Wikimedia
		// answers 429 to faster tools) and with the person's line.
		"--sleep", "1",
	}
	if ask.Limit > 0 {
		ours = append(ours, "--range", "1-"+strconv.FormatInt(ask.Limit, 10))
	}
	if ask.Rate > 0 {
		ours = append(ours, "--limit-rate", strconv.FormatInt(ask.Rate, 10))
	}
	// The browser's cookies and User-Agent (gallery-dl has no Referer
	// option: left out).
	theirs, cookies, err := browser.Args(pageURL)
	if err != nil {
		return nil, err
	}
	ours = append(ours, galleryBrowserArgs(theirs, GalleryAcceptsOf(tool.Version))...)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		cookies.Remove()
		return nil, fmt.Errorf("could not start gallery-dl: %w", err)
	}
	cmd, err := GalleryDlCmd(tool, ours, pageURL)
	if err != nil {
		cookies.Remove()
		return nil, err
	}
	p, err := Spawn(cmd, "gallery-dl")
	if err != nil {
		cookies.Remove()
		return nil, err
	}
	job := &GalleryJob{progress: VideoProgress{State: VideoRunning}, proc: p}
	go func() {
		defer cookies.Remove()
		job.read(p.Stdout)
		_, _ = io.Copy(io.Discard, p.Stdout)
		job.finish(p.Wait(), folder)
	}()
	return job, nil
}

// Progress is where the gallery's download is.
func (j *GalleryJob) Progress() VideoProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	p := j.progress
	p.Events = append([]Event(nil), p.Events...)
	return p
}

// Saved are the files saved so far this run (asked once, at the end).
func (j *GalleryJob) Saved() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.files...)
}

// Stop ends gallery-dl and all it started; what was saved stays.
func (j *GalleryJob) Stop() {
	j.mu.Lock()
	if j.progress.State == VideoRunning {
		j.progress.State, j.progress.Speed = VideoStopped, 0
	}
	p := j.proc
	j.mu.Unlock()
	p.Kill()
}

// galleryBrowserArgs are the browser's context (FromBrowser.Args, made for
// yt-dlp) as gallery-dl takes it: its cookies, and its User-Agent or ours
// (--user-agent, or -o user-agent=… before 1.24); no Referer, which
// gallery-dl has no option for.
func galleryBrowserArgs(theirs []string, accepts GalleryAccepts) []string {
	var out []string
	agent := ""
	for i := 0; i < len(theirs); i++ {
		switch theirs[i] {
		case "--referer":
			i++
		case "--user-agent":
			if i+1 < len(theirs) {
				agent = theirs[i+1]
			}
			i++
		case "--cookies":
			out = append(out, theirs[i])
			if i+1 < len(theirs) {
				out = append(out, theirs[i+1])
			}
			i++
		default:
			out = append(out, theirs[i])
		}
	}
	if agent == "" {
		agent = GalleryUserAgent
	}
	if accepts.UserAgent {
		return append(out, "--user-agent", agent)
	}
	return append(out, "-o", "user-agent="+agent)
}

// galleryLine is what one line of gallery-dl's says: a file saved, or one
// already there.
func galleryLine(line string) (path string, there, ok bool) {
	line = strings.TrimRight(line, "\r\n")
	if p, found := strings.CutPrefix(line, "# "); found {
		return p, true, true
	}
	if filepath.IsAbs(line) {
		return line, false, true
	}
	return "", false, false
}

// read counts gallery-dl's files as it prints them. Lines are kept short:
// a path is at most a few thousand bytes, and a longer line is no path.
func (j *GalleryJob) read(r io.Reader) {
	started := time.Now()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		raw, err := br.ReadSlice('\n')
		tooLong := errors.Is(err, bufio.ErrBufferFull)
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = br.ReadSlice('\n')
		}
		if !tooLong && len(raw) > 0 {
			if p, there, ok := galleryLine(strings.ToValidUTF8(string(raw), "�")); ok {
				j.mu.Lock()
				if there {
					j.there++
				} else {
					j.saved++
					if fi, err := os.Stat(p); err == nil {
						j.progress.Downloaded += fi.Size()
					}
					if len(j.files) < keepPaths {
						j.files = append(j.files, p)
					}
				}
				secs := max(time.Since(started).Seconds(), 0.5)
				j.progress.Speed = float64(j.progress.Downloaded) / secs
				j.mu.Unlock()
			}
		}
		if err != nil {
			return
		}
	}
}

// finish is gallery-dl's end: what it saved and what it couldn't, said in
// the download's log.
func (j *GalleryJob) finish(werr error, folder string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.progress.State == VideoStopped {
		return
	}
	j.progress.Speed = 0
	j.progress.Events = append(j.progress.Events, Event{At: time.Now(),
		Text: fmt.Sprintf("gallery-dl saved %d files (%d already there)", j.saved, j.there)})
	failure := ""
	var failed *FailedError
	switch {
	case errors.As(werr, &failed):
		failure = failed.Message
	case werr != nil:
		failure = werr.Error()
	}
	switch {
	case werr == nil:
		j.progress.State, j.progress.File = VideoDone, folder
	// Some files couldn't be had (4: a download failed): the rest is saved,
	// and starting it again tries those only.
	case j.saved+j.there > 0:
		j.progress.Events = append(j.progress.Events, Event{At: time.Now(), Text: "Some files failed: " + failure})
		j.progress.State, j.progress.File = VideoDone, folder
	default:
		j.progress.State, j.progress.Error = VideoFailed, failure
	}
}
