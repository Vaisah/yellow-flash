package main

import (
	"context"
	"fmt"
	"strings"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/ipc"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// commandBacklog is how many IPC commands may wait for the UI to be ready.
const commandBacklog = 64

// AddPrefill opens the Add URL dialog with these values (event
// "app:add-url").
type AddPrefill struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	Directory string `json:"directory"`
}

// Notice is a message for the UI to show as a toast (event "app:notice").
type Notice struct {
	Kind string `json:"kind"` // ok, error, info
	Text string `json:"text"`
}

// handleCommand is the ipc.Handler: it queues a command until the UI is
// ready. It returns false once the app is quitting (the client hears
// "gone"), or when the backlog is full.
func (a *App) handleCommand(in ipc.Incoming) bool {
	if a.closing.Load() {
		return false
	}
	// What can't be done yet is refused now, while the browser still holds
	// the download: the extension then leaves it to the browser.
	if unsupportedFromBrowser(in.Cmd) != "" {
		return false
	}
	select {
	case a.commands <- in:
		return true
	default:
		return false
	}
}

// domReady starts handling commands: events emitted before the UI listens
// would be lost.
func (a *App) domReady(ctx context.Context) {
	go func() {
		for {
			select {
			case <-a.stop:
				return
			case in := <-a.commands:
				a.dispatch(ctx, in)
			}
		}
	}()
}

func (a *App) stopCommands() {
	a.closing.Store(true)
	a.stopOnce.Do(func() { close(a.stop) })
}

// dispatch runs one command.
func (a *App) dispatch(ctx context.Context, in ipc.Incoming) {
	cmd := in.Cmd
	// A question or action (the automation API, or a paired device's):
	// answered with one line (api_app.go).
	if in.Reply != nil {
		a.answer(ctx, in)
		return
	}
	switch {
	case cmd.Exit:
		runtime.Quit(ctx)
		return
	case cmd.Startup:
		return // a launch at login: the window stays where it is
	case cmd.Show != "":
		a.focusWindow(ctx)
		runtime.EventsEmit(ctx, "app:show", cmd.Show)
		return
	case cmd.ExcludeSite != "" || cmd.PanelHideSite != "":
		a.browserSite(ctx, cmd)
		return
	case cmd.VideoPage != "":
		a.videoFromBrowser(ctx, cmd)
		return
	}
	if a.ready() != nil {
		a.notice(ctx, "error", "Yellow Flash is not ready to add downloads")
		return
	}
	if cmd.StartQueue {
		if r := a.queues.Load(); r != nil {
			if err := r.Start(ctx, a.queueID(cmd.Queue)); err != nil {
				a.notice(ctx, "error", "Could not start the queue: "+err.Error())
			}
		}
	}
	if isTorrentCommand(cmd) {
		a.torrentCommand(ctx, cmd)
		return
	}
	url := strings.TrimSpace(cmd.URL)
	if url == "" {
		if !cmd.StartQueue {
			a.focusWindow(ctx) // a plain second launch, as IDM does
		}
		return
	}
	if _, err := network.ValidateURL(url); err != nil {
		a.focusWindow(ctx)
		a.notice(ctx, "error", fmt.Sprintf("Cannot download %s: %v", url, err))
		return
	}
	// A download from the browser starts at once: the Add URL dialog would
	// lose its context (cookies, referer) and its restriction.
	if !cmd.Browser && !cmd.Later && !cmd.Silent {
		a.focusWindow(ctx)
		runtime.EventsEmit(ctx, "app:add-url", AddPrefill{URL: url, Filename: cmd.Name, Directory: cmd.Dir})
		return
	}
	a.addSilently(ctx, url, cmd)
}

// addSilently adds a download without a dialog: /n starts it, /a only adds it.
func (a *App) addSilently(ctx context.Context, url string, cmd ipc.Command) {
	s := a.GetSettings()
	dir := cmd.Dir
	if dir == "" {
		dir = s.DownloadDirectory
	}
	req := domain.CreateRequest{
		URL: url, Directory: dir, Filename: cmd.Name, Connections: s.Connections,
		Headers: requestHeaders(cmd),
		// An address from a web page may not reach this computer or its
		// network (the browser's name, size and type are not trusted: the
		// engine checks the name and asks the server for the rest).
		Restricted: cmd.Browser,
	}
	var (
		d   *domain.Download
		err error
	)
	if cmd.Later {
		d, err = a.service.Create(ctx, req)
	} else {
		d, err = a.service.Add(ctx, req)
	}
	// /a: the download waits in the main queue (or the one named).
	if err == nil && cmd.Later {
		if r := a.queues.Load(); r != nil {
			err = r.Add(ctx, a.queueID(cmd.Queue), d.ID)
		}
	}
	switch {
	case err != nil:
		a.notice(ctx, "error", "Could not add "+url+": "+err.Error())
	case cmd.Later:
		a.notice(ctx, "ok", "Added "+d.Filename+" to the queue (not started)")
	default:
		a.notice(ctx, "ok", "Downloading "+d.Filename)
	}
}

// queueID is the queue a command names, else the main queue.
func (a *App) queueID(name string) string {
	if r := a.queues.Load(); r != nil && name != "" {
		if id, ok := r.FindByName(name); ok {
			return id
		}
	}
	return queue.MainID
}

// requestHeaders carries the browser's request context. Credentials among
// them go to the SecretStore, never the database (engine.splitHeaders).
func requestHeaders(cmd ipc.Command) map[string]string {
	h := map[string]string{}
	if cmd.Referer != "" {
		h["Referer"] = cmd.Referer
	}
	if cmd.Cookies != "" {
		h["Cookie"] = cmd.Cookies
	}
	if cmd.UserAgent != "" {
		h["User-Agent"] = cmd.UserAgent
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

func (a *App) notice(ctx context.Context, kind, text string) {
	runtime.EventsEmit(ctx, "app:notice", Notice{Kind: kind, Text: text})
}

// focusWindow restores and raises the window. On Windows the sender has
// already allowed this process to take the foreground (platform.AllowForeground).
func (a *App) focusWindow(ctx context.Context) {
	runtime.WindowUnminimise(ctx)
	runtime.WindowShow(ctx)
	runtime.WindowSetAlwaysOnTop(ctx, true)
	runtime.WindowSetAlwaysOnTop(ctx, false)
}
