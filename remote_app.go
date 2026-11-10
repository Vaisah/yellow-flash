// Ported from Mister Download Manager, crates/app/src/remote.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// The remote control for the UI (Options → Remote: on or off, where it
// listens, pairing, paired devices), and the app's answers to a paired
// device's commands. A paired device lists, adds, pauses, starts and
// removes downloads and torrents, and starts and stops queues; it can't
// change settings, open windows, or see folders (its list has no paths),
// and a .torrent reaches the app as its bytes, never a path on this
// computer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
	"github.com/vaisah/internal/remote"
	"github.com/vaisah/internal/torrent"
	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

const keyRemote = "remote.settings"

// torrentIDPrefix marks a torrent in the remote's list ("torrent:<hash>").
const torrentIDPrefix = "torrent:"

// GetRemoteSettings returns Options → Remote (off by default).
func (a *App) GetRemoteSettings() remote.Config {
	c := remote.Config{Port: remote.Port}
	if a.store == nil {
		return c
	}
	if raw, ok, _ := a.store.Settings().Get(context.Background(), keyRemote); ok {
		_ = json.Unmarshal([]byte(raw), &c)
	}
	return c
}

// SaveRemoteSettings validates and saves Options → Remote, and listens as
// they say.
func (a *App) SaveRemoteSettings(c remote.Config) (remote.Config, error) {
	if c.Port < 1 || c.Port > 65535 {
		return remote.Config{}, errors.New("the port must be between 1 and 65535")
	}
	c.Listen = strings.TrimSpace(c.Listen)
	var hosts []string
	for _, h := range c.Hosts {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, " /:@?#") || len(h) > 253 {
			return remote.Config{}, fmt.Errorf("%q is not a host name", h)
		}
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) > 20 {
		return remote.Config{}, errors.New("at most 20 names")
	}
	c.Hosts = hosts
	raw, _ := json.Marshal(c)
	if err := a.store.Settings().Set(context.Background(), keyRemote, string(raw)); err != nil {
		return remote.Config{}, err
	}
	if a.remote != nil {
		a.remote.Apply(c)
		a.remoteChanged()
	}
	return c, nil
}

// RemoteInfo is the remote control's state for Options → Remote.
type RemoteInfo struct {
	Status remote.Status `json:"status"`
	// URLs are the page's addresses, the likeliest first.
	URLs []string `json:"urls"`
	// Fingerprint is the certificate's, to compare with the one the
	// phone's browser shows.
	Fingerprint string          `json:"fingerprint"`
	Devices     []remote.Device `json:"devices"`
}

// GetRemoteInfo returns the remote control's state.
func (a *App) GetRemoteInfo() RemoteInfo {
	if a.remote == nil {
		return RemoteInfo{Status: remote.Status{State: remote.Off}}
	}
	return RemoteInfo{Status: a.remote.Status(), URLs: a.remote.URLs(), Fingerprint: a.remote.Fingerprint(),
		Devices: a.remote.Devices()}
}

// RemoveRemoteDevice unpairs a device: refused from its next request on.
func (a *App) RemoveRemoteDevice(id string) error {
	if a.remote == nil || !a.remote.RemoveDevice(id) {
		return errors.New("no such device")
	}
	a.remoteChanged()
	return nil
}

// RemotePairing is the pairing window's state.
type RemotePairing struct {
	// Code is the 8-digit code shown ("": none, or it ran out).
	Code        string `json:"code"`
	SecondsLeft int    `json:"secondsLeft"`
	// Paired is the device the code paired, once it has.
	Paired string   `json:"paired"`
	URLs   []string `json:"urls"`
}

// StartRemotePairing shows a new pairing code (the remote control must be
// listening).
func (a *App) StartRemotePairing() (RemotePairing, error) {
	if a.remote == nil || a.remote.Status().State != remote.Listening {
		return RemotePairing{}, errors.New("turn the remote control on first")
	}
	a.remote.StartPairing()
	return a.GetRemotePairing(), nil
}

// GetRemotePairing is the code shown, its time left, and the device it
// paired.
func (a *App) GetRemotePairing() RemotePairing {
	if a.remote == nil {
		return RemotePairing{}
	}
	code, left, paired := a.remote.PairingState()
	return RemotePairing{Code: code, SecondsLeft: int(left / time.Second), Paired: paired, URLs: a.remote.URLs()}
}

// StopRemotePairing: the pairing window closed, its code is void.
func (a *App) StopRemotePairing() {
	if a.remote != nil {
		a.remote.StopPairing()
	}
}

func (a *App) remoteChanged() {
	if a.ctx != nil {
		wails.EventsEmit(a.ctx, "remote:changed")
	}
}

// startRemote makes the remote control and listens as the settings say.
func (a *App) startRemote() {
	r := remote.New(filepath.Join(a.dataDir, "remote"), ipc.NewSink(a.handleCommand), nil, a.remoteChanged, a.logf)
	a.remote = r
	r.Apply(a.GetRemoteSettings())
}

// speeds are the downloads' last speeds (bytes/s), from their progress.
type speeds struct {
	mu sync.Mutex
	m  map[string]float64
}

func (s *speeds) set(id string, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]float64{}
	}
	s.m[id] = v
}

func (s *speeds) get(id string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[id]
}

// answerRemote answers a paired device's command (one line, as the
// automation API does).
func (a *App) answerRemote(ctx context.Context, in ipc.Incoming) {
	cmd := in.Cmd
	reply := func(v any) { in.Reply <- ipc.Line(v) }
	refuse := func(msg, code string) { reply(ipc.ErrorAnswer{Error: msg, Code: code}) }
	if a.ready() != nil {
		refuse("Yellow Flash is not ready", "not-ready")
		return
	}
	switch {
	case cmd.Query == "list":
		reply(a.remoteList(ctx))
	case cmd.Query != "":
		refuse("unknown question", "unknown")
	case cmd.Action == "start_queue" || cmd.Action == "stop_queue":
		r := a.queues.Load()
		id, ok := "", false
		if r != nil {
			id, ok = r.FindByName(cmd.Queue)
		}
		if !ok {
			refuse("no such queue", "no-queue")
			return
		}
		var err error
		if cmd.Action == "start_queue" {
			err = r.Start(ctx, id)
		} else {
			err = r.Stop(ctx, id)
		}
		if err != nil {
			refuse(err.Error(), "failed")
			return
		}
		reply(ipc.ActAnswer{Done: []string{cmd.Queue}, Skipped: []ipc.Skipped{}})
	case cmd.Action != "":
		reply(a.remoteAct(ctx, cmd.Action, cmd.IDs))
	case len(cmd.TorrentFile) > 0 || torrent.IsMagnet(cmd.URL):
		// Looked up in the background (a magnet asks its peers first).
		a.torrentCommand(ctx, ipc.Command{URL: cmd.URL, TorrentFile: cmd.TorrentFile, Silent: true, Later: cmd.Later})
		reply(ipc.AddAnswer{Pending: true})
	case cmd.URL != "":
		d, err := a.remoteAdd(ctx, cmd)
		if err != nil {
			refuse(err.Error(), "unsupported")
			return
		}
		reply(ipc.AddAnswer{ID: d.ID})
	default:
		refuse("nothing to do", "unknown")
	}
}

// remoteAdd adds a web or FTP address to the download folder: started, or
// waiting in the queue named (the main queue by default).
func (a *App) remoteAdd(ctx context.Context, cmd ipc.Command) (*domain.Download, error) {
	u, err := network.ValidateURL(strings.TrimSpace(cmd.URL))
	if err != nil {
		return nil, err
	}
	s := a.GetSettings()
	req := domain.CreateRequest{URL: u.String(), Directory: s.DownloadDirectory, Connections: s.Connections}
	if !cmd.Later {
		d, err := a.service.Add(ctx, req)
		if err == nil {
			a.uiNotice("ok", "Downloading "+d.Filename+" (from a paired device)")
		}
		return d, err
	}
	d, err := a.service.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	if r := a.queues.Load(); r != nil {
		if err := r.Add(ctx, a.queueID(cmd.Queue), d.ID); err != nil {
			return d, err
		}
	}
	a.uiNotice("ok", "Added "+d.Filename+" to the queue (from a paired device)")
	return d, nil
}

// uiNotice shows a toast once the window is there.
func (a *App) uiNotice(kind, text string) {
	if a.ctx != nil {
		a.notice(a.ctx, kind, text)
	}
}

// remoteStatus is a download's status as the API says it.
func remoteStatus(s domain.Status) string {
	switch {
	case s == domain.Queued:
		return "Queued"
	case s.IsActive():
		return "Downloading"
	case s == domain.Completed:
		return "Complete"
	case s == domain.Failed:
		return "Error"
	}
	return "Stopped"
}

// remoteList is the list for a paired device: downloads and torrents, no
// paths.
func (a *App) remoteList(ctx context.Context) ipc.ListAnswer {
	ans := ipc.ListAnswer{Downloads: []ipc.DownloadInfo{}, Queues: []ipc.QueueInfo{}, Categories: []ipc.CategoryInfo{}}
	list, _ := a.service.List(ctx)
	var total float64
	for _, d := range list {
		info := ipc.DownloadInfo{ID: d.ID, File: d.Filename, URL: d.URL, Status: remoteStatus(d.Status),
			Downloaded: d.Downloaded}
		if info.Status == "Error" {
			info.Error = d.Error
		}
		if d.Size > 0 {
			size := d.Size
			info.Size = &size
		}
		if !d.CreatedAt.IsZero() {
			added := d.CreatedAt.UTC().Format(time.RFC3339)
			info.Added = &added
		}
		if d.Status.IsActive() {
			sp := a.speeds.get(d.ID)
			info.Speed = int64(sp)
			total += sp
		}
		ans.Downloads = append(ans.Downloads, info)
	}
	for _, t := range a.torrentRows() {
		size, added := t.Stats.Total, t.Added.UTC().Format(time.RFC3339)
		info := ipc.DownloadInfo{ID: torrentIDPrefix + t.Hash, File: t.Name, URL: "magnet:?xt=urn:btih:" + t.Hash,
			Downloaded: t.Stats.Done, Size: &size, Added: &added}
		switch {
		case t.Finished || t.Stats.Phase == torrent.Finished:
			info.Status = "Complete"
		case t.Stats.Phase == torrent.Paused:
			info.Status = "Stopped"
		default:
			info.Status = "Downloading"
			info.Speed = int64(t.Stats.DownSpeed)
			total += t.Stats.DownSpeed
		}
		ans.Downloads = append(ans.Downloads, info)
	}
	ans.Speed = int64(total)
	if r := a.queues.Load(); r != nil {
		for _, q := range r.Queues() {
			ids := q.Items
			if ids == nil {
				ids = []string{}
			}
			ans.Queues = append(ans.Queues, ipc.QueueInfo{Name: q.Name, Running: q.Running, IDs: ids})
		}
	}
	if v, ok := platform.VolumeOf(a.GetSettings().DownloadDirectory); ok {
		free := int64(min(v.Free, uint64(1<<62)))
		ans.Free = &free
	}
	return ans
}

// remoteAct does action to each of ids: what was done, and what was
// skipped and why.
func (a *App) remoteAct(ctx context.Context, action string, ids []string) ipc.ActAnswer {
	ans := ipc.ActAnswer{Done: []string{}, Skipped: []ipc.Skipped{}}
	skip := func(id, why string) { ans.Skipped = append(ans.Skipped, ipc.Skipped{ID: id, Why: why}) }
	for _, id := range ids {
		if hash, ok := strings.CutPrefix(id, torrentIDPrefix); ok {
			var err error
			switch action {
			case "pause":
				err = a.PauseTorrent(hash)
			case "start":
				err = a.ResumeTorrent(hash)
			case "remove", "delete":
				err = a.RemoveTorrent(hash, action == "delete")
			default:
				err = errors.New("unknown action")
			}
			switch {
			case errors.Is(err, torrent.ErrUnknown):
				skip(id, "no such download")
			case err != nil:
				skip(id, err.Error())
			default:
				ans.Done = append(ans.Done, id)
			}
			continue
		}
		d, err := a.service.Get(ctx, id)
		if err != nil {
			skip(id, "no such download")
			continue
		}
		switch action {
		case "pause":
			if !d.Status.IsActive() {
				skip(id, "not running")
				continue
			}
			err = a.service.Pause(ctx, id)
		case "start":
			switch {
			case d.Status == domain.Completed:
				skip(id, "complete")
				continue
			case d.Status.IsActive():
				skip(id, "already running")
				continue
			case d.Status == domain.Failed:
				err = a.service.Retry(ctx, id)
			default:
				err = a.service.Start(ctx, id)
			}
		case "remove", "delete":
			if d.Status.IsActive() {
				skip(id, "running: pause it first")
				continue
			}
			err = a.DeleteDownload(id, action == "delete")
		default:
			skip(id, "unknown action")
			continue
		}
		if err != nil {
			skip(id, err.Error())
			continue
		}
		ans.Done = append(ans.Done, id)
	}
	return ans
}
