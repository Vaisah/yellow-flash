// Ported from Mister Download Manager, crates/tools/src/playlist.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// A page's list of videos: a playlist, a channel, an album. Listed by yt-dlp
// flat (each entry its address, title and length, no video looked up) and
// lazily (--lazy-playlist: each entry printed as yt-dlp reads it, one JSON
// line), so a list of six and one of six thousand cost the same to start,
// the window shows them as they come, and the listing can be stopped at any
// point.
//
// Bounded on purpose: at most MaxEntries entries are kept, a line longer
// than maxLine is skipped, and nothing here recurses.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const (
	// MaxEntries are the entries kept at most (about 200 bytes each).
	// YouTube's own playlists stop at 5,000; a channel is listed a page at a
	// time (Page).
	MaxEntries = 20_000
	// maxLine bounds a line: a flat entry is a few hundred bytes.
	maxLine = 256 * 1024
	// Page is how many of a list whose size isn't known (a channel: its
	// newest first) are listed at a time.
	Page = 50
)

// Entry is one video of a list.
type Entry struct {
	// Index is its place in the list, from 1.
	Index int64 `json:"index"`
	// URL is the video's own page (what a download looks up).
	URL   string `json:"url"`
	Title string `json:"title"`
	// Duration is in seconds, when the site says; 0 otherwise.
	Duration float64 `json:"duration"`
	// Unavailable: private, deleted or otherwise not downloadable, by what
	// the site lists.
	Unavailable bool `json:"unavailable"`
}

// ListProgress is where a listing is.
type ListProgress struct {
	// Listed are the entries listed so far; Total the list's size, when the
	// site says (0 otherwise); Title the list's title, from its entries.
	Listed int    `json:"listed"`
	Total  int64  `json:"total"`
	Title  string `json:"title"`
	// Done: yt-dlp has finished (or was stopped). Capped: more were there
	// than MaxEntries, and the listing stopped there.
	Done   bool   `json:"done"`
	Capped bool   `json:"capped"`
	Error  string `json:"error"`
}

type rawEntry struct {
	ID            *string  `json:"id"`
	URL           *string  `json:"url"`
	WebpageURL    *string  `json:"webpage_url"`
	Title         *string  `json:"title"`
	Duration      *float64 `json:"duration"`
	PlaylistIndex *float64 `json:"playlist_index"`
	PlaylistCount *float64 `json:"playlist_count"`
	PlaylistTitle *string  `json:"playlist_title"`
	Availability  *string  `json:"availability"`
}

// gone are the titles YouTube gives the entries it won't show.
var gone = []string{"[Private video]", "[Deleted video]", "[Unavailable video]"}

// Clock is a length as a clock reads it: 9:18, 1:02:05.
func Clock(secs float64) string {
	s := int64(math.Round(max(0, secs)))
	h, m, sec := s/3600, s/60%60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// parseLine reads one line of --flat-playlist -j: the entry, and what it
// says of the list.
func parseLine(line []byte, fallbackIndex int64) (Entry, int64, string, bool) {
	var r rawEntry
	if json.Unmarshal(line, &r) != nil {
		return Entry{}, 0, "", false
	}
	u := ""
	if r.WebpageURL != nil {
		u = *r.WebpageURL
	} else if r.URL != nil {
		u = *r.URL
	}
	if !webPage(u) {
		return Entry{}, 0, "", false
	}
	title := u
	switch {
	case r.Title != nil && strings.TrimSpace(*r.Title) != "":
		title = strings.TrimSpace(*r.Title)
	case r.ID != nil:
		title = *r.ID
	}
	unavailable := slices.Contains(gone, title)
	if r.Availability != nil {
		switch *r.Availability {
		case "private", "premium_only", "subscriber_only", "needs_auth":
			unavailable = true
		}
	}
	e := Entry{Index: fallbackIndex, URL: u, Title: title, Unavailable: unavailable}
	if r.PlaylistIndex != nil && *r.PlaylistIndex >= 1 {
		e.Index = int64(*r.PlaylistIndex)
	}
	if d := orZero(r.Duration); d > 0 {
		e.Duration = d
	}
	listTitle := ""
	if r.PlaylistTitle != nil {
		listTitle = *r.PlaylistTitle
	}
	return e, int64(orZero(r.PlaylistCount)), listTitle, true
}

func webPage(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// listed is what a listing has gathered.
type listed struct {
	mu        sync.Mutex
	entries   []Entry
	progress  ListProgress
	cancelled bool
}

// readEntries reads yt-dlp's lines into l until it ends, the list is
// capped, or it's stopped.
func readEntries(r io.Reader, l *listed, from int64) {
	br := bufio.NewReaderSize(r, 64<<10)
	next := from
	var line []byte
	for {
		line = line[:0]
		tooLong := false
		var err error
		for {
			var chunk []byte
			chunk, err = br.ReadSlice('\n')
			if !tooLong {
				if len(line)+len(chunk) > maxLine {
					tooLong, line = true, line[:0]
				} else {
					line = append(line, chunk...)
				}
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				break
			}
		}
		if !tooLong && len(line) > 0 {
			if e, total, title, ok := parseLine(line, next); ok {
				next = e.Index + 1
				l.mu.Lock()
				if l.progress.Done {
					l.mu.Unlock()
					return
				}
				if len(l.entries) >= MaxEntries {
					l.progress.Capped = true
					l.mu.Unlock()
					return
				}
				if l.progress.Total == 0 {
					l.progress.Total = total
				}
				if l.progress.Title == "" && strings.TrimSpace(title) != "" {
					l.progress.Title = title
				}
				l.entries = append(l.entries, e)
				l.progress.Listed = len(l.entries)
				l.mu.Unlock()
			}
		}
		if err != nil {
			return
		}
	}
}

// Listing is a listing running in the background: entries come in as
// yt-dlp prints them.
type Listing struct {
	l    *listed
	proc *Proc
}

// StartListing lists pageURL's entries from to to (from 1; to 0: to the
// end), flat and lazily.
func StartListing(exe, pageURL string, browser FromBrowser, from, to int64) (*Listing, error) {
	from = max(from, 1)
	rng := strconv.FormatInt(from, 10) + ":"
	if to > 0 {
		rng += strconv.FormatInt(max(to, from), 10)
	}
	args := []string{"--flat-playlist", "--lazy-playlist", "-j", "--playlist-items", rng, "--no-warnings"}
	theirs, cookies, err := browser.Args(pageURL)
	if err != nil {
		return nil, err
	}
	cmd, err := YtDlpCmd(exe, nil, append(args, theirs...), pageURL)
	if err != nil {
		cookies.Remove()
		return nil, err
	}
	p, err := Spawn(cmd, "yt-dlp")
	if err != nil {
		cookies.Remove()
		return nil, err
	}
	ls := &Listing{l: &listed{}, proc: p}
	go func() {
		defer cookies.Remove()
		readEntries(p.Stdout, ls.l, from)
		ls.l.mu.Lock()
		capped := ls.l.progress.Capped
		ls.l.mu.Unlock()
		// Capped, nobody reads it any more: ended, not waited for.
		if capped {
			p.Kill()
		}
		_, _ = io.Copy(io.Discard, p.Stdout)
		werr := p.Wait()
		ls.l.mu.Lock()
		defer ls.l.mu.Unlock()
		// Told even part-way (a site that stopped answering): what was
		// listed stays, and the window says where it stopped. Stopped by us
		// (capped, cancelled): no error.
		if werr != nil && !ls.l.progress.Capped && !ls.l.cancelled {
			var failed *FailedError
			if errors.As(werr, &failed) {
				ls.l.progress.Error = failed.Message
			} else {
				ls.l.progress.Error = werr.Error()
			}
		}
		ls.l.progress.Done = true
	}()
	return ls, nil
}

// Progress is where the listing is.
func (ls *Listing) Progress() ListProgress {
	ls.l.mu.Lock()
	defer ls.l.mu.Unlock()
	return ls.l.progress
}

// EntriesFrom are the entries listed after the first seen (the window
// takes them as they come).
func (ls *Listing) EntriesFrom(seen int) []Entry {
	ls.l.mu.Lock()
	defer ls.l.mu.Unlock()
	if seen < 0 || seen >= len(ls.l.entries) {
		return nil
	}
	return slices.Clone(ls.l.entries[seen:])
}

// Cancel stops listing (what was listed stays).
func (ls *Listing) Cancel() {
	ls.l.mu.Lock()
	ls.l.cancelled = true
	ls.l.mu.Unlock()
	ls.proc.Kill()
}
