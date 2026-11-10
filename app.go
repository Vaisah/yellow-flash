package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vaisah/internal/application/datacap"
	"github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/application/events"
	"github.com/vaisah/internal/application/queues"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// AppName is the product name shown in the window and used for app data.
const (
	AppName    = ipc.AppName
	AppVersion = ipc.AppVersion
)

// errNotReady is returned when the UI calls in before startup finished.
var errNotReady = errors.New("application is not ready")

// App is the Wails binding layer. It translates UI calls into service
// calls and holds no download logic of its own.
type App struct {
	ctx     context.Context
	service *download.Service
	store   *jsonstore.Store
	manager *engine.Manager
	dataDir string
	// queues runs the download queues and their schedules (set once the
	// engine is up; read by the engine's event publisher).
	queues     atomic.Pointer[queues.Runner]
	dataCap    atomic.Pointer[datacap.Watcher]
	stopQueues context.CancelFunc
	// media holds the video and gallery downloads run by the helpers.
	media mediaJobs

	// Commands from the IPC channel wait here until the UI is ready.
	commands chan ipc.Incoming
	stop     chan struct{}
	stopOnce sync.Once
	closing  atomic.Bool
}

// Settings are user preferences persisted in the download list (downloads.json).
type Settings struct {
	DownloadDirectory string `json:"downloadDirectory"`
	Connections       int    `json:"connections"`
	StartImmediately  bool   `json:"startImmediately"`
	UseClipboard      bool   `json:"useClipboard"`
}

// AddRequest is what the Add URL dialog submits.
type AddRequest struct {
	URL         string `json:"url"`
	Directory   string `json:"directory"`
	Filename    string `json:"filename"`
	Connections int    `json:"connections"`
	Start       bool   `json:"start"`
}

// AppInfo describes the running application.
type AppInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	DataDir string `json:"dataDir"`
}

const (
	keyDirectory   = "ui.downloadDirectory"
	keyConnections = "ui.connections"
	keyStart       = "ui.startImmediately"
	keyClipboard   = "ui.useClipboard"
)

// NewApp opens the state database in dataDir (see ipc.DataDir). The caller
// must hold the instance lock for dataDir.
func NewApp(dataDir string) (*App, error) {
	if err := ipc.PrivateDir(dataDir); err != nil {
		return nil, err
	}
	store, err := jsonstore.Open(filepath.Join(dataDir, "downloads.json"))
	if err != nil {
		return nil, err
	}
	return &App{
		store:    store,
		dataDir:  dataDir,
		commands: make(chan ipc.Incoming, commandBacklog),
		stop:     make(chan struct{}),
	}, nil
}

// Startup attaches Wails' application context and restores interrupted work.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	publisher := events.PublisherFunc(func(e events.Event) {
		runtime.EventsEmit(ctx, "downloads:updated", e.Download)
		if e.Type == events.ProgressUpdated {
			runtime.EventsEmit(ctx, "downloads:progress", e.Progress)
		} else if r := a.queues.Load(); r != nil {
			r.Notify() // a download changed: its queue may start the next one
		}
	})
	// URLs typed by the user may legitimately point at LAN hosts (NAS,
	// intranet). Browser-originated downloads use the engine's restricted
	// client (CreateRequest.Restricted). DNS, proxy and FTP mode from the
	// saved network settings.
	dns, netOpts, err := a.engineNetwork(ctx)
	if err != nil {
		runtime.LogErrorf(ctx, "network settings: %v", err)
	}
	manager, err := engine.New(a.store, engine.Config{AllowPrivateNetworks: true, Publisher: publisher,
		HostLimits: a.store, DNS: dns, Network: netOpts})
	if err != nil {
		runtime.LogErrorf(ctx, "start download engine: %v", err)
		return
	}
	a.manager = manager
	a.service = download.New(a.manager)
	if err := a.service.Recover(ctx); err != nil {
		runtime.LogErrorf(ctx, "recover downloads: %v", err)
	}
	runner, err := queues.New(ctx, a.manager, a.store.Queues(), nil)
	if err != nil {
		runtime.LogErrorf(ctx, "load queues: %v", err)
		return
	}
	runner.OnDone = func(q queue.Queue) { a.queueDone(ctx, q) }
	a.queues.Store(runner)
	qctx, cancel := context.WithCancel(ctx)
	a.stopQueues = cancel
	runner.Startup(qctx)
	go runner.Run(qctx)
	go a.powerLoop(qctx)

	// Download limits (a data cap) and the global speed limit.
	cap := datacap.New(a.manager, nil)
	cap.OnChange = func(blocked bool, until time.Time) {
		runtime.EventsEmit(ctx, "datacap:changed", map[string]any{"blocked": blocked, "until": until})
		if blocked {
			a.notice(ctx, "info", "The download limit is reached: downloads wait until "+until.Local().Format("15:04"))
		}
	}
	a.dataCap.Store(cap)
	a.applyLiveSettings(a.GetNetworkSettings())
	go cap.Run(qctx)
	go a.registerBrowserHost(ctx)
}

// queueDone does what a finished queue's schedule asks.
func (a *App) queueDone(ctx context.Context, q queue.Queue) {
	runtime.EventsEmit(ctx, "queues:done", q)
	s := q.Schedule
	if s.OpenWhenDone != "" {
		if err := platform.OpenPath(s.OpenWhenDone); err != nil {
			a.notice(ctx, "error", "Could not open "+s.OpenWhenDone+": "+err.Error())
		}
	}
	if s.TurnOff != "" {
		// Turning off ends the app too: no separate exit.
		a.startTurnOff(ctx, string(s.TurnOff), s.ForceClose)
		return
	}
	if s.ExitWhenDone {
		runtime.Quit(ctx)
	}
}

// Shutdown pauses active downloads, persisting progress, then closes the DB.
func (a *App) Shutdown(ctx context.Context) {
	a.stopCommands()
	if a.stopQueues != nil {
		a.stopQueues()
	}
	canClose := true
	if a.service != nil {
		waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := a.service.Shutdown(waitCtx); err != nil {
			canClose = false
			runtime.LogErrorf(ctx, "shutdown downloads: %v", err)
		}
	}
	if a.store != nil && canClose {
		if err := a.store.Close(); err != nil {
			runtime.LogErrorf(ctx, "close database: %v", err)
		}
	}
}

func (a *App) ready() error {
	if a.service == nil {
		return errNotReady
	}
	return nil
}

// Info returns the application name, version and data folder.
func (a *App) Info() AppInfo { return AppInfo{Name: AppName, Version: AppVersion, DataDir: a.dataDir} }

// ListDownloads returns every download, newest first.
func (a *App) ListDownloads() ([]domain.Download, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.service.List(a.ctx)
}

// GetDownload returns one download.
func (a *App) GetDownload(id string) (domain.Download, error) {
	if err := a.ready(); err != nil {
		return domain.Download{}, err
	}
	d, err := a.service.Get(a.ctx, id)
	if err != nil {
		return domain.Download{}, err
	}
	return *d, nil
}

// InspectLink previews a link's filename, size and type before adding it.
func (a *App) InspectLink(rawURL string) (domain.LinkInfo, error) {
	if err := a.ready(); err != nil {
		return domain.LinkInfo{}, err
	}
	info, err := a.service.Inspect(a.ctx, rawURL, nil)
	if err != nil {
		return domain.LinkInfo{}, err
	}
	return *info, nil
}

// AddDownload creates a download and, if requested, starts it.
func (a *App) AddDownload(req AddRequest) (domain.Download, error) {
	if err := a.ready(); err != nil {
		return domain.Download{}, err
	}
	create := domain.CreateRequest{URL: req.URL, Directory: req.Directory, Filename: req.Filename, Connections: req.Connections}
	var (
		d   *domain.Download
		err error
	)
	if req.Start {
		d, err = a.service.Add(a.ctx, create)
	} else {
		d, err = a.service.Create(a.ctx, create)
	}
	if d == nil {
		return domain.Download{}, err
	}
	return *d, err
}

// ResumeDownload starts a queued, paused or failed download.
func (a *App) ResumeDownload(id string) error { return a.act(id, a.svc().Start) }

// PauseDownload pauses an active download.
func (a *App) PauseDownload(id string) error { return a.act(id, a.svc().Pause) }

// CancelDownload stops a download and discards its partial data.
func (a *App) CancelDownload(id string) error { return a.act(id, a.svc().Cancel) }

// RetryDownload restarts a failed download.
func (a *App) RetryDownload(id string) error { return a.act(id, a.svc().Retry) }

// PauseAll pauses every active download.
func (a *App) PauseAll() error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.service.PauseAll(a.ctx)
}

// ResumeAll starts every queued or paused download.
func (a *App) ResumeAll() error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.service.ResumeAll(a.ctx)
}

// DeleteDownload removes the record. With deleteFile, a completed file is
// also removed from disk.
func (a *App) DeleteDownload(id string, deleteFile bool) error {
	if err := a.ready(); err != nil {
		return err
	}
	d, err := a.service.Get(a.ctx, id)
	if err != nil {
		return err
	}
	if err = a.service.Delete(a.ctx, id); err != nil {
		return err
	}
	if deleteFile && d.Status == domain.Completed {
		if err = os.Remove(filepath.Join(d.Destination, d.Filename)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("record removed, but the file could not be deleted: %w", err)
		}
	}
	return nil
}

// GetSegments returns the last persisted byte-range progress of a download.
func (a *App) GetSegments(id string) ([]domain.Segment, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.ListSegments(a.ctx, id)
}

// OpenFile opens a completed download with its default application.
func (a *App) OpenFile(id string) error {
	d, err := a.GetDownload(id)
	if err != nil {
		return err
	}
	if d.Status != domain.Completed {
		return errors.New("the download has not finished yet")
	}
	return platform.OpenPath(filepath.Join(d.Destination, d.Filename))
}

// ShowInFolder reveals the download in the system file manager.
func (a *App) ShowInFolder(id string) error {
	d, err := a.GetDownload(id)
	if err != nil {
		return err
	}
	target := filepath.Join(d.Destination, d.Filename)
	if _, statErr := os.Stat(target); statErr != nil {
		target = d.Destination
	}
	return platform.RevealPath(target)
}

// OpenFolder opens a directory, such as the default download folder.
func (a *App) OpenFolder(dir string) error { return platform.OpenPath(dir) }

// SelectDirectory shows a native folder picker.
func (a *App) SelectDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose download folder"})
}

// ClipboardURL returns the clipboard text when it is a downloadable
// http(s) URL, otherwise "".
func (a *App) ClipboardURL() string {
	text, err := runtime.ClipboardGetText(a.ctx)
	if err != nil {
		return ""
	}
	text = strings.TrimSpace(text)
	if _, err = network.ValidateURL(text); err != nil {
		return ""
	}
	return text
}

// GetSettings returns preferences, filling in defaults.
func (a *App) GetSettings() Settings {
	s := Settings{Connections: 8, StartImmediately: true, UseClipboard: true}
	if home, err := os.UserHomeDir(); err == nil {
		s.DownloadDirectory = filepath.Join(home, "Downloads")
	}
	if a.store == nil {
		return s
	}
	all, err := a.store.Settings().All(context.Background())
	if err != nil {
		return s
	}
	if v := all[keyDirectory]; v != "" {
		s.DownloadDirectory = v
	}
	if n, err := strconv.Atoi(all[keyConnections]); err == nil && n >= 1 && n <= 16 {
		s.Connections = n
	}
	if v, ok := all[keyStart]; ok {
		s.StartImmediately = v == "true"
	}
	if v, ok := all[keyClipboard]; ok {
		s.UseClipboard = v == "true"
	}
	return s
}

// SaveSettings validates and persists preferences.
func (a *App) SaveSettings(s Settings) (Settings, error) {
	if s.Connections < 1 || s.Connections > 16 {
		return Settings{}, errors.New("connections must be between 1 and 16")
	}
	dir := strings.TrimSpace(s.DownloadDirectory)
	if dir == "" || !filepath.IsAbs(dir) {
		return Settings{}, errors.New("download folder must be an absolute path")
	}
	ctx := context.Background()
	for k, v := range map[string]string{
		keyDirectory: filepath.Clean(dir), keyConnections: strconv.Itoa(s.Connections),
		keyStart: strconv.FormatBool(s.StartImmediately), keyClipboard: strconv.FormatBool(s.UseClipboard),
	} {
		if err := a.store.Settings().Set(ctx, k, v); err != nil {
			return Settings{}, err
		}
	}
	return a.GetSettings(), nil
}

// svc returns the service; act checks readiness before any call is made.
func (a *App) svc() *download.Service { return a.service }

func (a *App) act(id string, fn func(context.Context, string) error) error {
	if err := a.ready(); err != nil {
		return err
	}
	return fn(a.ctx, id)
}
