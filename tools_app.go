package main

// The helper programs (yt-dlp, ffmpeg, gallery-dl) and the video and
// gallery downloads they run, for the UI. Their downloads are kept in
// memory while the app runs; the download list doesn't hold them yet.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/platform"
	"github.com/vaisah/internal/tools"
	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

// HelperInfo is a helper program's state.
type HelperInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Exe     string `json:"exe"`
	// Chosen: the person's own copy, not Yellow Flash's.
	Chosen bool `json:"chosen"`
	// ChosenTooOld names a chosen gallery-dl too old to drive, passed over.
	ChosenTooOld string `json:"chosenTooOld,omitempty"`
}

var helperNames = []tools.Helper{tools.HelperYtDlp, tools.HelperFfmpeg, tools.HelperGalleryDl}

func (a *App) toolsDir() string { return tools.Dir(a.dataDir) }

// helper is the program to use for h.
func (a *App) helper(h tools.Helper) (tools.Installed, bool) {
	switch h {
	case tools.HelperYtDlp:
		return tools.YtDlp(a.toolsDir())
	case tools.HelperFfmpeg:
		return tools.Ffmpeg(a.toolsDir())
	case tools.HelperGalleryDl:
		return tools.GalleryDl(a.toolsDir())
	}
	return tools.Installed{}, false
}

func (a *App) helperInfo(h tools.Helper) HelperInfo {
	info := HelperInfo{Name: string(h)}
	if i, ok := a.helper(h); ok {
		info.Version, info.Exe = i.Version, i.Exe
		mine, chosen := tools.Chosen(a.toolsDir(), h)
		info.Chosen = chosen && mine.Exe == i.Exe
	}
	if h == tools.HelperGalleryDl {
		if old, ok := tools.GalleryDlChosenTooOld(a.toolsDir()); ok {
			info.ChosenTooOld = old.Version
		}
	}
	return info
}

func helperNamed(name string) (tools.Helper, error) {
	for _, h := range helperNames {
		if string(h) == name {
			return h, nil
		}
	}
	return "", fmt.Errorf("no helper named %q", name)
}

// GetHelpers says which helpers are there, and whose.
func (a *App) GetHelpers() []HelperInfo {
	var out []HelperInfo
	for _, h := range helperNames {
		out = append(out, a.helperInfo(h))
	}
	return out
}

// helperNetwork is how helpers and media-server checks are fetched: public
// addresses only, through the network settings' proxy.
func (a *App) helperNetwork() network.Options {
	var opts network.Options
	switch s := a.GetNetworkSettings(); s.ProxyMode {
	case "fixed":
		opts.Proxy = s.ProxyURL
	case "none":
		opts.Proxy = "direct"
	}
	return opts
}

// InstallHelper installs (or updates) Yellow Flash's own copy of a helper,
// its signature and checksum checked, telling its progress
// ("helpers:progress").
func (a *App) InstallHelper(name string) (HelperInfo, error) {
	h, err := helperNamed(name)
	if err != nil {
		return HelperInfo{}, err
	}
	client := network.NewClient(a.helperNetwork())
	progress := func(got, total int64) {
		if a.ctx != nil {
			wails.EventsEmit(a.ctx, "helpers:progress", map[string]any{"name": name, "got": got, "total": total})
		}
	}
	ctx := context.Background()
	switch h {
	case tools.HelperYtDlp:
		_, err = tools.InstallYtDlp(ctx, a.toolsDir(), client, progress)
	case tools.HelperFfmpeg:
		_, err = tools.InstallFfmpeg(ctx, a.toolsDir(), client, progress)
	case tools.HelperGalleryDl:
		_, err = tools.InstallGalleryDl(ctx, a.toolsDir(), client, progress)
	}
	return a.helperInfo(h), err
}

// ChooseHelper uses the person's own program for a helper from now on, once
// it answers as one.
func (a *App) ChooseHelper(name, exe string) (HelperInfo, error) {
	h, err := helperNamed(name)
	if err != nil {
		return HelperInfo{}, err
	}
	_, err = tools.Choose(context.Background(), a.toolsDir(), h, exe)
	return a.helperInfo(h), err
}

// ForgetHelper goes back to Yellow Flash's own copy of a helper.
func (a *App) ForgetHelper(name string) (HelperInfo, error) {
	h, err := helperNamed(name)
	if err != nil {
		return HelperInfo{}, err
	}
	err = tools.Forget(a.toolsDir(), h)
	return a.helperInfo(h), err
}

// VideoLookUp is what a page holds, for the video window.
type VideoLookUp struct {
	Title    string            `json:"title"`
	Duration float64           `json:"duration"`
	Choices  []tools.Choice    `json:"choices"`
	Subs     []tools.SubChoice `json:"subs"`
	// NeedsFfmpeg: ffmpeg would bring better qualities than those listed.
	NeedsFfmpeg bool `json:"needsFfmpeg"`
	// List is set for a page that lists videos (its videos aren't looked
	// up).
	List *tools.ListPage `json:"list,omitempty"`
}

// webPageURL accepts an http(s) page address.
func webPageURL(raw string) (*url.URL, error) {
	u, err := network.ValidateURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("not a web page's address")
	}
	return u, nil
}

// LookUpVideo looks a page up with yt-dlp: its title and the choices to
// download it in; language (the interface's) picks the subtitles offered.
func (a *App) LookUpVideo(pageURL, language string) (VideoLookUp, error) {
	if _, err := webPageURL(pageURL); err != nil {
		return VideoLookUp{}, err
	}
	ytdlp, ok := a.helper(tools.HelperYtDlp)
	if !ok {
		return VideoLookUp{}, errors.New("yt-dlp is not installed")
	}
	looked, err := tools.LookUpPage(context.Background(), ytdlp.Exe, pageURL, a.browserVideos.get(pageURL))
	if err != nil {
		return VideoLookUp{}, err
	}
	if looked.List != nil {
		return VideoLookUp{Title: looked.List.Title, List: looked.List}, nil
	}
	v := *looked.Video
	_, ffmpeg := a.helper(tools.HelperFfmpeg)
	return VideoLookUp{Title: v.Title, Duration: v.Duration, Choices: tools.Choices(v, ffmpeg),
		Subs: tools.SubChoices(v, language), NeedsFfmpeg: !ffmpeg && tools.NeedsMerging(v)}, nil
}

// VideoRequest is a video to download, as the video window asks it.
type VideoRequest struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Choice is the one picked from LookUpVideo's.
	Choice tools.Choice `json:"choice"`
	// Directory is where to save it ("": the download folder).
	Directory string     `json:"directory"`
	Subs      tools.Subs `json:"subs"`
}

// GalleryRequest is a gallery to download.
type GalleryRequest struct {
	URL string `json:"url"`
	// Directory is where its folder goes ("": the download folder).
	Directory string `json:"directory"`
	// Limit is at most this many files (0: all).
	Limit int64 `json:"limit"`
}

// MediaRow is a video or gallery download.
type MediaRow struct {
	ID       string              `json:"id"`
	Kind     string              `json:"kind"` // video, gallery
	Title    string              `json:"title"`
	URL      string              `json:"url"`
	Progress tools.VideoProgress `json:"progress"`
	// Dir is the folder it is saved in; Started when it began (the list's
	// order and "added" column).
	Dir     string    `json:"dir"`
	Started time.Time `json:"started"`
}

type mediaJob interface {
	Progress() tools.VideoProgress
	Stop()
}

type mediaEntry struct {
	row     MediaRow
	job     mediaJob
	started time.Time
}

// mediaJobs are the video and gallery downloads of this session.
type mediaJobs struct {
	mu   sync.Mutex
	jobs map[string]*mediaEntry
}

func (m *mediaJobs) add(e *mediaEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs == nil {
		m.jobs = map[string]*mediaEntry{}
	}
	m.jobs[e.row.ID] = e
}

func (m *mediaJobs) get(id string) (*mediaEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.jobs[id]
	return e, ok
}

func (m *mediaJobs) rows() []MediaRow {
	m.mu.Lock()
	entries := make([]*mediaEntry, 0, len(m.jobs))
	for _, e := range m.jobs {
		entries = append(entries, e)
	}
	m.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].started.Before(entries[j].started) })
	out := make([]MediaRow, 0, len(entries))
	for _, e := range entries {
		r := e.row
		r.Progress = e.job.Progress()
		out = append(out, r)
	}
	return out
}

// saveDir is where a media download goes: dir, else the download folder.
func (a *App) saveDir(dir string) string {
	if strings.TrimSpace(dir) != "" {
		return dir
	}
	return a.GetSettings().DownloadDirectory
}

// StartVideoDownload downloads a video page in the chosen format, telling
// its progress ("media:progress") until it ends.
func (a *App) StartVideoDownload(req VideoRequest) (MediaRow, error) {
	if _, err := webPageURL(req.URL); err != nil {
		return MediaRow{}, err
	}
	ytdlp, ok := a.helper(tools.HelperYtDlp)
	if !ok {
		return MediaRow{}, errors.New("yt-dlp is not installed")
	}
	dir := a.saveDir(req.Directory)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return MediaRow{}, err
	}
	stem, err := filesystem.SanitizeFilename(req.Title)
	if err != nil {
		stem = "video"
	}
	ffmpeg, _ := a.helper(tools.HelperFfmpeg)
	job, err := tools.StartVideo(tools.VideoOptions{Exe: ytdlp.Exe, URL: req.URL, Selector: req.Choice.Selector,
		Dir: dir, Stem: stem, Ffmpeg: ffmpeg.Exe, Expected: req.Choice.Size, Subs: req.Subs,
		Browser: a.browserVideos.get(req.URL),
		Client:  network.NewClient(a.helperNetwork())})
	if err != nil {
		return MediaRow{}, err
	}
	return a.track(&mediaEntry{row: MediaRow{ID: uuid.NewString(), Kind: "video", Title: req.Title, URL: req.URL, Dir: dir}, job: job}), nil
}

// StartGalleryDownload downloads a gallery into its own folder.
func (a *App) StartGalleryDownload(req GalleryRequest) (MediaRow, error) {
	u, err := webPageURL(req.URL)
	if err != nil {
		return MediaRow{}, err
	}
	gallery, ok := a.helper(tools.HelperGalleryDl)
	if !ok {
		return MediaRow{}, errors.New("gallery-dl is not installed")
	}
	name := tools.GalleryName(u)
	dir := filepath.Join(a.saveDir(req.Directory), name)
	job, err := tools.StartGallery(gallery, req.URL, dir,
		tools.GalleryAsk{Limit: max(req.Limit, 0)}, tools.FromBrowser{})
	if err != nil {
		return MediaRow{}, err
	}
	return a.track(&mediaEntry{row: MediaRow{ID: uuid.NewString(), Kind: "gallery", Title: name, URL: req.URL, Dir: dir}, job: job}), nil
}

// track keeps a media download and tells its progress until it ends.
func (a *App) track(e *mediaEntry) MediaRow {
	e.started = time.Now()
	e.row.Started = e.started
	a.media.add(e)
	ctx := a.ctx
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			p := e.job.Progress()
			if ctx != nil {
				wails.EventsEmit(ctx, "media:progress", map[string]any{"id": e.row.ID, "progress": p})
			}
			if p.State != tools.VideoRunning {
				if ctx != nil {
					wails.EventsEmit(ctx, "media:changed", e.row.ID)
				}
				return
			}
		}
	}()
	r := e.row
	r.Progress = e.job.Progress()
	return r
}

// ListMediaDownloads are this session's video and gallery downloads, oldest
// first.
func (a *App) ListMediaDownloads() []MediaRow { return a.media.rows() }

// StopMediaDownload stops a video or gallery download (what was saved
// stays).
func (a *App) StopMediaDownload(id string) error {
	e, ok := a.media.get(id)
	if !ok {
		return fmt.Errorf("no download %s", id)
	}
	e.job.Stop()
	return nil
}

// mediaTarget is a media download's saved file once done (a gallery's
// folder), else its folder.
func (m *mediaJobs) target(id string) (string, bool, error) {
	e, ok := m.get(id)
	if !ok {
		return "", false, fmt.Errorf("no download %s", id)
	}
	p := e.job.Progress()
	if p.State == tools.VideoDone && p.File != "" {
		return p.File, true, nil
	}
	return e.row.Dir, p.State == tools.VideoDone, nil
}

// OpenMediaFile opens a finished video (a gallery's folder).
func (a *App) OpenMediaFile(id string) error {
	path, done, err := a.media.target(id)
	if err != nil {
		return err
	}
	if !done {
		return errors.New("the download has not finished yet")
	}
	return platform.OpenPath(path)
}

// ShowMediaInFolder shows a video or gallery download in the file manager.
func (a *App) ShowMediaInFolder(id string) error {
	path, _, err := a.media.target(id)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		path = filepath.Dir(path)
	}
	return platform.RevealPath(path)
}

// RemoveMediaDownload takes a video or gallery download off the list,
// stopping it first; what was saved stays.
func (a *App) RemoveMediaDownload(id string) error {
	e, ok := a.media.get(id)
	if !ok {
		return fmt.Errorf("no download %s", id)
	}
	e.job.Stop()
	a.media.mu.Lock()
	delete(a.media.jobs, id)
	a.media.mu.Unlock()
	return nil
}
