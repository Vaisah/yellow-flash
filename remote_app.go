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

// remoteAdd answers a paired device's address, magnet link or .torrent
// (its bytes: a path on this computer is never taken).
func (a *App) remoteAdd(ctx context.Context, in ipc.Incoming) {
	cmd := in.Cmd
	reply := func(v any) { in.Reply <- ipc.Line(v) }
	if a.ready() != nil {
		reply(ipc.ErrorAnswer{Error: "Yellow Flash is not ready", Code: "not-ready"})
		return
	}
	switch {
	case len(cmd.TorrentFile) > 0 || torrent.IsMagnet(cmd.URL):
		// Looked up in the background (a magnet asks its peers first).
		a.torrentCommand(ctx, ipc.Command{URL: cmd.URL, TorrentFile: cmd.TorrentFile, Silent: true, Later: cmd.Later})
		reply(ipc.AddAnswer{Pending: true})
	case cmd.URL != "":
		d, err := a.remoteAddURL(ctx, cmd)
		if err != nil {
			reply(ipc.ErrorAnswer{Error: err.Error(), Code: "unsupported"})
			return
		}
		reply(ipc.AddAnswer{ID: d.ID})
	default:
		reply(ipc.ErrorAnswer{Error: "nothing to do", Code: "unknown"})
	}
}

// remoteAddURL adds a web or FTP address to the download folder: started,
// or waiting in the queue named (the main queue by default).
func (a *App) remoteAddURL(ctx context.Context, cmd ipc.Command) (*domain.Download, error) {
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
