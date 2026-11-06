package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vaisah/internal/application/download"
	"github.com/vaisah/internal/application/events"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/persistence/sqlite"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx     context.Context
	service *download.Service
	store   *sqlite.Store
	manager *engine.Manager
}

type domainDownload = domain.Download

func NewApp() (*App, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dataDir := filepath.Join(configDir, "Download Manager")
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(filepath.Join(dataDir, "downloads.db"))
	if err != nil {
		return nil, err
	}
	app := &App{store: store}
	return app, nil
}

// Startup attaches Wails' application context and restores interrupted work.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	publisher := events.PublisherFunc(func(e events.Event) {
		runtime.EventsEmit(ctx, "downloads:updated", e.Download)
		if e.Type == events.ProgressUpdated {
			runtime.EventsEmit(ctx, "downloads:progress", e.Progress)
		}
	})
	// URLs typed by the user may legitimately point at LAN hosts (NAS,
	// intranet). Browser-originated requests will use a restricted client.
	manager, err := engine.New(a.store, engine.Config{AllowPrivateNetworks: true, Publisher: publisher})
	if err != nil {
		runtime.LogErrorf(ctx, "start download engine: %v", err)
		return
	}
	a.manager = manager
	a.service = download.New(a.manager)
	if err := a.service.Recover(ctx); err != nil {
		runtime.LogErrorf(ctx, "recover downloads: %v", err)
	}
}

func (a *App) Shutdown(ctx context.Context) {
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

func (a *App) ListDownloads() ([]domainDownload, error) {
	if a.service == nil {
		return nil, fmt.Errorf("application is not ready")
	}
	items, err := a.service.List(a.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domainDownload, len(items))
	for i := range items {
		out[i] = domainDownload(items[i])
	}
	return out, nil
}

func (a *App) AddDownload(rawURL, destination, filename string) (domainDownload, error) {
	if a.service == nil {
		return domainDownload{}, fmt.Errorf("application is not ready")
	}
	item, err := a.service.Create(a.ctx, domain.CreateRequest{URL: rawURL, Directory: destination, Filename: filename})
	if err != nil {
		return domainDownload{}, err
	}
	if err = a.service.Start(a.ctx, item.ID); err != nil {
		return domainDownload{}, err
	}
	return domainDownload(*item), nil
}
func (a *App) PauseDownload(id string) error  { return a.service.Pause(a.ctx, id) }
func (a *App) ResumeDownload(id string) error { return a.service.Start(a.ctx, id) }
func (a *App) CancelDownload(id string) error { return a.service.Cancel(a.ctx, id) }
func (a *App) RetryDownload(id string) error  { return a.service.Retry(a.ctx, id) }
func (a *App) DeleteDownload(id string) error { return a.service.Delete(a.ctx, id) }
func (a *App) GetDownload(id string) (domainDownload, error) {
	item, err := a.service.Get(a.ctx, id)
	if err != nil {
		return domainDownload{}, err
	}
	return domainDownload(*item), nil
}

func (a *App) SelectDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose download folder"})
}
