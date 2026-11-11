// Ported from Mister Download Manager, crates/app/src/extractq.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Extracting archives when their download completes (Options → Archives →
// "Extract archives when their download completes"), and Extract by hand
// on a finished one. A split archive waits for its last volume. One archive
// at a time, in the background (internal/extract does the work, into a
// staging folder, so a failure leaves only the archive); each extracted
// file is marked as downloaded from the archive's site, as the archive is.
// Afterwards the archive goes to the Recycle Bin (the Trash) only when
// Options says so. "extract:done" tells the window how it went (a password
// needed, 7-Zip missing, …).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/extract"
	"github.com/vaisah/internal/platform"
)

const keyExtract = "extract.settings"

// ExtractSettings is Options → Archives.
type ExtractSettings struct {
	// Auto extracts every archive when its download (its last volume)
	// completes.
	Auto bool `json:"auto"`
	// Into is "smart" (a folder named after the archive when it holds
	// several things at its top), "always" or "never".
	Into extract.Subfolder `json:"into"`
	// DeleteArchive moves the archive (every volume) to the Recycle Bin
	// once it is extracted.
	DeleteArchive bool `json:"deleteArchive"`
	// SevenZip is 7-Zip's program, for 7z and RAR ("": the installed one).
	SevenZip string `json:"sevenZip"`
	// SevenZipFound is the 7-Zip used (not saved; "": none found).
	SevenZipFound string `json:"sevenZipFound"`
}

// ExtractResult is how an archive's extraction went.
type ExtractResult struct {
	// ID is the first volume's download.
	ID      string          `json:"id"`
	Archive string          `json:"archive"`
	OK      bool            `json:"ok"`
	Report  *extract.Report `json:"report,omitempty"`
	Error   string          `json:"error,omitempty"`
	// Reason: "password" (needs one), "wrong-password", "no-7zip",
	// "missing-volume", "damaged", "too-big", "too-many", "unsupported",
	// "cancelled", "io".
	Reason string `json:"reason,omitempty"`
	// Trashed: the archive went to the Recycle Bin (nil: not asked).
	Trashed *bool `json:"trashed,omitempty"`
}

type extractJob struct {
	ids      []string
	archive  string
	files    []string
	password string
	from     string
	referrer string
}

// extractor is the queue: one archive at a time.
type extractor struct {
	mu      sync.Mutex
	queue   []extractJob
	running *atomic.Bool // the running job's cancel; nil: idle
	// seen are first volumes queued, running or extracted this session: a
	// set is extracted once (a failure lets it be tried again).
	seen map[string]bool
	// done is told every outcome (tests).
	done func(ExtractResult)
}

// GetExtractSettings is Options → Archives.
func (a *App) GetExtractSettings() ExtractSettings {
	s := ExtractSettings{Into: extract.Smart}
	if a.store != nil {
		if raw, ok, _ := a.store.Settings().Get(context.Background(), keyExtract); ok {
			_ = json.Unmarshal([]byte(raw), &s)
		}
	}
	s.SevenZipFound = a.sevenZip(s)
	return s
}

// SaveExtractSettings saves Options → Archives.
func (a *App) SaveExtractSettings(s ExtractSettings) (ExtractSettings, error) {
	switch s.Into {
	case extract.Smart, extract.Always, extract.Never:
	case "":
		s.Into = extract.Smart
	default:
		return ExtractSettings{}, fmt.Errorf("unknown choice %q", s.Into)
	}
	if p := strings.TrimSpace(s.SevenZip); p != "" {
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() || !filepath.IsAbs(p) {
			return ExtractSettings{}, errors.New("7-Zip's program must be a file, given as an absolute path")
		}
		s.SevenZip = p
	}
	s.SevenZipFound = ""
	raw, _ := json.Marshal(s)
	if err := a.store.Settings().Set(context.Background(), keyExtract, string(raw)); err != nil {
		return ExtractSettings{}, err
	}
	return a.GetExtractSettings(), nil
}

// sevenZip is 7-Zip's program: the one Options names, else the installed one.
func (a *App) sevenZip(s ExtractSettings) string {
	if s.SevenZip != "" {
		return s.SevenZip
	}
	return extract.FindSevenZip()
}

// downloadFinished offers a completed download's archive when Options says
// to extract them.
func (a *App) downloadFinished(d domain.Download) {
	if d.Status != domain.Completed || !extract.ArchiveName(d.Filename) || !a.GetExtractSettings().Auto {
		return
	}
	a.offerExtract(d, "", false)
}

// ExtractDownload extracts a finished download's archive (with password,
// when it needs one); its outcome comes as "extract:done".
func (a *App) ExtractDownload(id, password string) error {
	if err := a.ready(); err != nil {
		return err
	}
	d, err := a.service.Get(context.Background(), id)
	if err != nil {
		return err
	}
	switch {
	case d.Status != domain.Completed:
		return errors.New("the download hasn't finished")
	case !extract.ArchiveName(d.Filename):
		return errors.New("it isn't an archive")
	}
	if !a.offerExtract(*d, password, true) {
		return errors.New("every volume of the archive must be downloaded first")
	}
	return nil
}

// volumeSet is the downloads of the archive d is (a volume of), in volume
// order: those in its folder whose names are volumes of the same set; nil
// when one in between is missing. Just d for an archive in one file.
func volumeSet(d domain.Download, all []domain.Download) []domain.Download {
	v, ok := extract.VolumeKey(d.Filename)
	if !ok {
		return []domain.Download{d}
	}
	type vol struct {
		n int
		d domain.Download
	}
	var set []vol
	for _, o := range all {
		w, ok := extract.VolumeKey(o.Filename)
		if ok && o.Destination == d.Destination && w.Kind == v.Kind && strings.EqualFold(w.Base, v.Base) &&
			!slices.ContainsFunc(set, func(x vol) bool { return x.n == w.Index }) {
			set = append(set, vol{w.Index, o})
		}
	}
	slices.SortFunc(set, func(x, y vol) int { return x.n - y.n })
	out := make([]domain.Download, 0, len(set))
	for k, x := range set {
		if x.n != k+1 {
			return nil // a gap: a volume still to come
		}
		out = append(out, x.d)
	}
	return out
}

// offerExtract queues d's archive when every volume is there; false when
// one isn't. By hand, an archive extracted already is tried again.
func (a *App) offerExtract(d domain.Download, password string, byHand bool) bool {
	all, err := a.service.List(context.Background())
	if err != nil {
		return false
	}
	set := volumeSet(d, all)
	if len(set) == 0 || slices.ContainsFunc(set, func(o domain.Download) bool { return o.Status != domain.Completed }) {
		return false
	}
	job := extractJob{archive: filepath.Join(set[0].Destination, set[0].Filename), password: password,
		from: set[0].URL, referrer: set[0].Headers["Referer"]}
	for _, o := range set {
		job.ids = append(job.ids, o.ID)
		job.files = append(job.files, filepath.Join(o.Destination, o.Filename))
	}
	x := &a.extracts
	x.mu.Lock()
	if x.seen == nil {
		x.seen = map[string]bool{}
	}
	waiting := slices.ContainsFunc(x.queue, func(j extractJob) bool { return j.archive == job.archive })
	if byHand && !waiting {
		delete(x.seen, job.archive)
	}
	if x.seen[job.archive] {
		x.mu.Unlock()
		return true
	}
	x.seen[job.archive] = true
	x.queue = append(x.queue, job)
	start := x.running == nil
	if start {
		x.running = &atomic.Bool{}
	}
	x.mu.Unlock()
	if start {
		go a.runExtracts()
	}
	return true
}

// runExtracts works through the queue, one archive at a time.
func (a *App) runExtracts() {
	x := &a.extracts
	for {
		x.mu.Lock()
		if len(x.queue) == 0 {
			x.running = nil
			x.mu.Unlock()
			return
		}
		job := x.queue[0]
		x.queue = x.queue[1:]
		cancel := x.running
		x.mu.Unlock()
		res := a.extractOne(job, cancel)
		if !res.OK {
			x.mu.Lock()
			delete(x.seen, job.archive) // it may be tried again
			x.mu.Unlock()
		}
		a.extractDone(res)
	}
}

func (a *App) extractOne(job extractJob, cancel *atomic.Bool) ExtractResult {
	s := a.GetExtractSettings()
	dest := filepath.Dir(job.archive)
	var size int64
	for _, f := range job.files {
		if fi, err := os.Stat(f); err == nil {
			size += fi.Size()
		}
	}
	limits := extract.ForArchive(size)
	// No more than the disk has room for (keeping a margin free).
	if v, ok := platform.VolumeOf(dest); ok {
		const margin = 128 << 20
		if free := int64(v.Free) - margin; free < limits.Total {
			limits.Total = max(free, 0)
		}
	}
	report, err := extract.Extract(job.archive, extract.Policy{Dest: dest, Subfolder: s.Into, Password: job.password,
		Limits: limits, Tool: a.sevenZip(s), Cancel: cancel,
		OnFile: func(p string) { platform.MarkFromInternet(p, job.from, job.referrer) }})
	res := ExtractResult{ID: job.ids[0], Archive: job.archive}
	if err != nil {
		res.Error, res.Reason = err.Error(), extractReason(err)
		return res
	}
	res.OK, res.Report = true, &report
	if s.DeleteArchive {
		var present []string
		for _, f := range job.files {
			if _, err := os.Stat(f); err == nil {
				present = append(present, f)
			}
		}
		gone := platform.Trash(present) == nil
		res.Trashed = &gone
	}
	return res
}

// extractReason is the kind of failure, for the window's words.
func extractReason(err error) string {
	var (
		mv  *extract.MissingVolumeError
		dmg *extract.DamagedError
		big *extract.TooBigError
	)
	switch {
	case errors.Is(err, extract.ErrNeedsPassword):
		return "password"
	case errors.Is(err, extract.ErrWrongPassword):
		return "wrong-password"
	case errors.Is(err, extract.ErrNoTool):
		return "no-7zip"
	case errors.As(err, &mv):
		return "missing-volume"
	case errors.As(err, &dmg):
		return "damaged"
	case errors.As(err, &big):
		return "too-big"
	case errors.Is(err, extract.ErrTooMany):
		return "too-many"
	case errors.Is(err, extract.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, extract.ErrCancelled):
		return "cancelled"
	}
	return "io"
}

// extractDone tells the window, and a notice.
func (a *App) extractDone(res ExtractResult) {
	if f := a.extracts.done; f != nil {
		f(res)
	}
	a.emit("extract:done", res)
	name := filepath.Base(res.Archive)
	switch {
	case res.OK:
		a.uiNotice("ok", fmt.Sprintf("Extracted %d file(s) from %s to %s", res.Report.Files, name, res.Report.Folder))
	case res.Reason == "password" || res.Reason == "wrong-password":
		// The window asks for it ("extract:done" says so).
	case res.Reason != "cancelled":
		a.uiNotice("error", "Couldn't extract "+name+": "+res.Error)
	}
}

// stopExtracts stops the archive being extracted (its staging folder goes)
// and forgets the queue.
func (a *App) stopExtracts() {
	x := &a.extracts
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.running != nil {
		x.running.Store(true)
	}
	x.queue = nil
}
