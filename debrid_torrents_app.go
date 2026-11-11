// Ported from Mister Download Manager, crates/app (torrents through a debrid account, #44)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Torrents through a debrid account: with Options → Debrid → "Torrents"
// on, a magnet link or .torrent file goes to the first account, which
// downloads it on its own servers; its files then come over HTTPS as
// ordinary downloads (in the main list too), into the torrent's folder. No
// peer ever connects to this computer, so none sees its address, and no
// SOCKS5 proxy is needed. The list is kept in <data>/debrid/torrents.json;
// a torrent stopped by closing the app continues at the next start from
// its transfer at the service.

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vaisah/internal/debrid"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/torrent"
)

// DebridTorrent is a torrent sent to a debrid account.
type DebridTorrent struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Hash    string      `json:"hash"`
	Magnet  string      `json:"magnet"`
	Account string      `json:"account"`
	Service debrid.Kind `json:"service"`
	// Transfer is its id at the service, once sent.
	Transfer string `json:"transfer,omitempty"`
	// Dir is the folder chosen; Path, once done, is the torrent's folder
	// (one file: that file).
	Dir  string `json:"dir"`
	Path string `json:"path,omitempty"`
	// State is "sending", "service" (the service is fetching it), "files"
	// (its files are coming), "done", "failed" or "stopped".
	State string `json:"state"`
	// Progress is the service's (0 to 1; -1 unknown); Done and Total are the
	// files' bytes here.
	Progress float64   `json:"progress"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"`
	Error    string    `json:"error,omitempty"`
	Notes    []string  `json:"notes,omitempty"`
	Added    time.Time `json:"added"`
}

func (t *DebridTorrent) active() bool {
	return t.State == "sending" || t.State == "service" || t.State == "files"
}

// debridTorrents is the list and the runs.
type debridTorrents struct {
	mu      sync.Mutex
	loaded  bool
	list    []*DebridTorrent
	running map[string]context.CancelFunc
	// stopping: stopped by the user (not by closing the app).
	stopping map[string]bool
	wg       sync.WaitGroup
}

func (a *App) debridTorrentsFile() string { return filepath.Join(a.dataDir, "debrid", "torrents.json") }

// debridListLocked is the list, read once.
func (a *App) debridListLocked() []*DebridTorrent {
	h := &a.debrid.torrents
	if !h.loaded {
		h.loaded = true
		if b, err := os.ReadFile(a.debridTorrentsFile()); err == nil {
			_ = json.Unmarshal(b, &h.list)
		}
	}
	return h.list
}

func (a *App) saveDebridTorrentsLocked() {
	b, _ := json.MarshalIndent(a.debrid.torrents.list, "", " ")
	err := ipc.PrivateDir(filepath.Dir(a.debridTorrentsFile()))
	if err == nil {
		err = ipc.PrivateWrite(a.debridTorrentsFile(), b)
	}
	if err != nil {
		a.logf("debrid torrents: %v", err)
	}
}

// changeDebridTorrent changes a torrent, saves the list and tells the window.
func (a *App) changeDebridTorrent(id string, change func(t *DebridTorrent)) {
	h := &a.debrid.torrents
	h.mu.Lock()
	for _, t := range a.debridListLocked() {
		if t.ID == id {
			change(t)
		}
	}
	a.saveDebridTorrentsLocked()
	h.mu.Unlock()
	a.emit("debrid:torrents", nil)
}

// debridTorrentsOn reports whether torrents go to a debrid account.
func (a *App) debridTorrentsOn() bool {
	if !a.savedDebrid().Torrents {
		return false
	}
	_, _, ok := a.debridAccounts().TorrentAccount()
	return ok
}

// magnetOf is source as a magnet link: a magnet itself, or a .torrent
// file's (an absolute path).
func magnetOf(source string) (string, error) {
	source = strings.TrimSpace(source)
	switch {
	case torrent.IsMagnet(source):
		return source, nil
	case isTorrentPath(source):
		b, err := torrent.ReadFile(source)
		if err != nil {
			return "", err
		}
		if m, ok := torrent.MagnetLink(b); ok {
			return m, nil
		}
	}
	return "", torrent.ErrNotATorrent
}

// SendTorrentToDebrid sends a magnet link or a .torrent file (an absolute
// path) to the first debrid account; its files come into dir (empty: the
// download folder).
func (a *App) SendTorrentToDebrid(source, dir string) (DebridTorrent, error) {
	magnet, err := magnetOf(source)
	if err != nil {
		return DebridTorrent{}, err
	}
	return a.sendMagnetToDebrid(magnet, dir)
}

func (a *App) sendMagnetToDebrid(magnet, dir string) (DebridTorrent, error) {
	acct, kind, ok := a.debridAccounts().TorrentAccount()
	if !ok {
		return DebridTorrent{}, errors.New("sign in to a debrid account first (Options → Debrid)")
	}
	dir = a.saveDir(dir)
	if !filepath.IsAbs(dir) {
		return DebridTorrent{}, errors.New("the folder must be an absolute path")
	}
	u, err := url.Parse(magnet)
	if err != nil {
		return DebridTorrent{}, torrent.ErrNotATorrent
	}
	q := u.Query()
	hash := strings.ToLower(strings.TrimPrefix(q.Get("xt"), "urn:btih:"))
	name := q.Get("dn")
	if name == "" {
		name = hash
	}
	t := &DebridTorrent{ID: uuid.NewString(), Name: name, Hash: hash, Magnet: magnet, Account: acct, Service: kind,
		Dir: dir, State: "sending", Progress: -1, Added: time.Now()}
	h := &a.debrid.torrents
	h.mu.Lock()
	if slices.ContainsFunc(a.debridListLocked(), func(o *DebridTorrent) bool { return hash != "" && o.Hash == hash && o.active() }) {
		h.mu.Unlock()
		return DebridTorrent{}, errors.New("this torrent is with the debrid account already")
	}
	h.list = append(h.list, t)
	a.saveDebridTorrentsLocked()
	out := *t
	h.mu.Unlock()
	a.emit("debrid:torrents", nil)
	a.runDebridTorrent(t.ID)
	return out, nil
}

// ListDebridTorrents is the torrents sent to debrid accounts, newest first.
func (a *App) ListDebridTorrents() []DebridTorrent {
	h := &a.debrid.torrents
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []DebridTorrent
	for _, t := range a.debridListLocked() {
		c := *t
		c.Notes = slices.Clone(t.Notes)
		out = append(out, c)
	}
	slices.Reverse(out)
	return out
}

// StopDebridTorrent stops a torrent; it stays at the service, and
// ResumeDebridTorrent continues it.
func (a *App) StopDebridTorrent(id string) error {
	h := &a.debrid.torrents
	h.mu.Lock()
	cancel := h.running[id]
	if cancel != nil {
		if h.stopping == nil {
			h.stopping = map[string]bool{}
		}
		h.stopping[id] = true
	}
	h.mu.Unlock()
	if cancel == nil {
		return errors.New("the torrent isn't running")
	}
	cancel()
	return nil
}

// ResumeDebridTorrent continues a stopped or failed torrent (from its
// transfer at the service, when it was sent).
func (a *App) ResumeDebridTorrent(id string) error {
	h := &a.debrid.torrents
	h.mu.Lock()
	i := slices.IndexFunc(a.debridListLocked(), func(t *DebridTorrent) bool { return t.ID == id })
	running := h.running[id] != nil
	h.mu.Unlock()
	switch {
	case i < 0:
		return errors.New("no such torrent")
	case running:
		return errors.New("the torrent is running")
	}
	a.changeDebridTorrent(id, func(t *DebridTorrent) { t.State, t.Error = "sending", "" })
	a.runDebridTorrent(id)
	return nil
}

// RemoveDebridTorrent stops a torrent and takes it from the list; its
// files stay where they are.
func (a *App) RemoveDebridTorrent(id string) error {
	_ = a.StopDebridTorrent(id)
	h := &a.debrid.torrents
	h.mu.Lock()
	list := a.debridListLocked()
	i := slices.IndexFunc(list, func(t *DebridTorrent) bool { return t.ID == id })
	if i < 0 {
		h.mu.Unlock()
		return errors.New("no such torrent")
	}
	h.list = slices.Delete(list, i, i+1)
	a.saveDebridTorrentsLocked()
	h.mu.Unlock()
	a.emit("debrid:torrents", nil)
	return nil
}

// runDebridTorrent runs a torrent in the background until it ends or is
// stopped.
func (a *App) runDebridTorrent(id string) {
	h := &a.debrid.torrents
	h.mu.Lock()
	i := slices.IndexFunc(a.debridListLocked(), func(t *DebridTorrent) bool { return t.ID == id })
	if i < 0 || h.running[id] != nil {
		h.mu.Unlock()
		return
	}
	t := *h.list[i]
	ctx, cancel := context.WithCancel(context.Background())
	if h.running == nil {
		h.running = map[string]context.CancelFunc{}
	}
	h.running[id] = cancel
	delete(h.stopping, id)
	h.wg.Add(1)
	h.mu.Unlock()

	order := debrid.Order{Account: t.Account, Magnet: t.Magnet, Transfer: t.Transfer, Folder: t.Dir, Poll: a.debrid.poll}
	hooks := debrid.Hooks{
		Sent: func(transfer string) {
			a.changeDebridTorrent(id, func(t *DebridTorrent) { t.Transfer, t.State = transfer, "service" })
		},
		Note: func(line string) {
			a.changeDebridTorrent(id, func(t *DebridTorrent) {
				t.Notes = append(t.Notes, time.Now().Format("15:04:05")+" "+line)
				if n := len(t.Notes); n > 50 {
					t.Notes = t.Notes[n-50:]
				}
			})
		},
		AtService: func(p float64) {
			a.changeDebridTorrent(id, func(t *DebridTorrent) { t.State, t.Progress = "service", p })
		},
		Files: func(done, total int64) {
			a.changeDebridTorrent(id, func(t *DebridTorrent) { t.State, t.Done, t.Total = "files", done, total })
		},
	}
	go func() {
		defer h.wg.Done()
		res, err := a.debridAccounts().RunTorrent(ctx, order, a.debridFetch, hooks)
		h.mu.Lock()
		stopped := h.stopping[id]
		delete(h.running, id)
		delete(h.stopping, id)
		h.mu.Unlock()
		cancel()
		a.changeDebridTorrent(id, func(t *DebridTorrent) {
			switch {
			case err == nil:
				t.State, t.Path, t.Done, t.Error = "done", res.Path, res.Size, ""
				t.Total = max(t.Total, res.Size)
			case errors.Is(err, context.Canceled) && stopped:
				t.State = "stopped"
			case errors.Is(err, context.Canceled):
				// The app is closing: it continues at the next start.
			default:
				t.State, t.Error = "failed", err.Error()
			}
		})
		switch {
		case err == nil:
			a.uiNotice("ok", "The torrent "+t.Name+" came down through "+t.Service.Name())
		case !errors.Is(err, context.Canceled):
			a.uiNotice("error", "The torrent "+t.Name+": "+err.Error())
		}
	}()
}

// resumeDebridTorrents continues the torrents the app was running when it
// closed.
func (a *App) resumeDebridTorrents() {
	h := &a.debrid.torrents
	h.mu.Lock()
	var ids []string
	for _, t := range a.debridListLocked() {
		if t.active() {
			ids = append(ids, t.ID)
		}
	}
	h.mu.Unlock()
	for _, id := range ids {
		a.runDebridTorrent(id)
	}
}

// stopDebridTorrents stops every run (they continue at the next start) and
// waits a little for them.
func (a *App) stopDebridTorrents() {
	h := &a.debrid.torrents
	h.mu.Lock()
	for _, cancel := range h.running {
		cancel()
	}
	h.mu.Unlock()
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
	}
}

// debridFetch downloads one of a torrent's files as an ordinary download
// (an earlier run's, unfinished, is continued), to its end. Stopped, the
// download is paused: its bytes stay for the next run.
func (a *App) debridFetch(ctx context.Context, link, dir, name string) (string, int64, error) {
	if err := a.ready(); err != nil {
		return "", 0, err
	}
	var id string
	if all, err := a.service.List(ctx); err == nil {
		for _, d := range all {
			if d.URL == link && d.Destination == dir && (name == "" || d.Filename == name) && d.Status != domain.Completed &&
				d.Status != domain.Cancelled {
				id = d.ID
			}
		}
	}
	if id == "" {
		d, err := a.service.Add(ctx, domain.CreateRequest{URL: link, Directory: dir, Filename: name})
		if err != nil {
			return "", 0, err
		}
		id = d.ID
	} else {
		_ = a.service.Start(ctx, id) // it may be running already
	}
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = a.service.Pause(context.Background(), id)
			return "", 0, ctx.Err()
		case <-t.C:
		}
		d, err := a.service.Get(context.Background(), id)
		if err != nil {
			return "", 0, err
		}
		switch d.Status {
		case domain.Completed:
			return filepath.Join(d.Destination, d.Filename), d.Size, nil
		case domain.Failed:
			return "", 0, errors.New(d.Filename + ": " + d.Error)
		case domain.Cancelled:
			return "", 0, errors.New(d.Filename + " was cancelled")
		}
	}
}

// debridTorrentCommand sends a torrent given to the app (the command line,
// the browser, remote control) to the debrid account.
func (a *App) debridTorrentCommand(cmd ipc.Command) {
	go func() {
		var (
			magnet string
			err    error
		)
		if len(cmd.TorrentFile) > 0 {
			var ok bool
			if magnet, ok = torrent.MagnetLink(cmd.TorrentFile); !ok {
				err = torrent.ErrNotATorrent
			}
		} else {
			magnet, err = magnetOf(cmd.URL)
		}
		var t DebridTorrent
		if err == nil {
			t, err = a.sendMagnetToDebrid(magnet, cmd.Dir)
		}
		if err != nil {
			a.uiNotice("error", "Could not send the torrent to the debrid account: "+err.Error())
			return
		}
		a.uiNotice("ok", "Sent the torrent "+t.Name+" to "+t.Service.Name())
	}()
}
