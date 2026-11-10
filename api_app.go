// Ported from Mister Download Manager, crates/app/src/app.rs (answer, pair_answer) and crates/app/src/remote.rs (qr_text)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// The automation API: questions and actions from programs on this computer
// (yellowflash-cli, yellowflash-mcp, scripts) through the app's channel,
// and a paired device's (the remote control). Off by default for programs
// (`yellowflash-cli api on`); a paired device is answered while the remote
// control is on, and may list, add and act, nothing else.

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
	"github.com/vaisah/internal/remote"
	"github.com/vaisah/internal/torrent"
	"rsc.io/qr"
)

const keyAPI = "api.on"

// apiOn reports the automation API on.
func (a *App) apiOn() bool {
	if a.store == nil {
		return false
	}
	v, ok, _ := a.store.Settings().Get(context.Background(), keyAPI)
	return ok && v == "true"
}

func (a *App) setAPI(on bool) error {
	v := "false"
	if on {
		v = "true"
	}
	return a.store.Settings().Set(context.Background(), keyAPI, v)
}

// answer answers a command that asks for a line.
func (a *App) answer(ctx context.Context, in ipc.Incoming) {
	cmd := in.Cmd
	reply := func(v any) { in.Reply <- ipc.Line(v) }
	refuse := func(msg, code string) { reply(ipc.ErrorAnswer{Error: msg, Code: code}) }
	switch {
	case cmd.Remote:
		if a.remote == nil || a.remote.Status().State == remote.Off {
			refuse("the remote control is off", "remote-off")
			return
		}
		if cmd.Query == "" && cmd.Action == "" {
			a.remoteAdd(ctx, in)
			return
		}
	case cmd.Pair:
		reply(a.pairAnswer())
		return
	case cmd.API != nil:
		if err := a.setAPI(*cmd.API); err != nil {
			refuse(err.Error(), "failed")
			return
		}
		reply(ipc.APIAnswer{API: *cmd.API})
		return
	case !a.apiOn():
		refuse("the automation API is off (yellowflash-cli api on)", ipc.APIOff)
		return
	}
	if a.ready() != nil {
		refuse("Yellow Flash is not ready", "not-ready")
		return
	}
	switch {
	case cmd.Action == "start_queue" || cmd.Action == "stop_queue":
		r := a.queues.Load()
		id, ok := "", false
		if r != nil {
			id, ok = r.FindByName(cmd.Queue)
		}
		if !ok {
			refuse("no such queue", "unknown-queue")
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
		switch cmd.Action {
		case "pause", "start", "remove", "delete":
			reply(a.apiAct(ctx, cmd.Action, cmd.IDs))
		default:
			refuse("unknown action "+cmd.Action, "unknown-action")
		}
	case cmd.Query == "list":
		reply(a.apiList(ctx, cmd.Remote))
	default:
		refuse("unknown query "+cmd.Query, "unknown-query")
	}
}

// pairAnswer is a new pairing code for the remote control (yellowflash-cli
// pair): the page's addresses and a QR code for a terminal.
func (a *App) pairAnswer() any {
	if a.remote == nil || a.remote.Status().State != remote.Listening {
		return ipc.ErrorAnswer{Error: "the remote control isn't listening (Options → Remote)", Code: "remote-off"}
	}
	code := a.remote.StartPairing()
	urls := a.remote.URLs()
	a.remoteChanged()
	return ipc.PairAnswer{Code: code, Minutes: int(remote.CodeValid / time.Minute), URLs: urls,
		QR: qrText(urls[0] + "#pair=" + code)}
}

// qrText is a QR code of text for a terminal, two rows of modules per line
// (half blocks), with its quiet zone. The light modules are the printed
// ones: right on a dark terminal, inverted on a light one, which phones'
// scanners read too.
func qrText(text string) string {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	const quiet = 2
	light := func(x, y int) bool { return !c.Black(x-quiet, y-quiet) }
	n := c.Size + 2*quiet
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		for x := range n {
			top, bottom := light(x, y), y+1 < n && light(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// apiStatus is a download's status as the API says it.
func apiStatus(s domain.Status) string {
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

// withoutPassword is an address with any password taken out.
func withoutPassword(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, set := u.User.Password(); set {
		u.User = url.User(u.User.Username())
	}
	return u.String()
}

// apiList is every download and torrent, the total speed, the queues and
// the free space. A paired device (remote) sees no folder of this
// computer's, nor a password in an address.
func (a *App) apiList(ctx context.Context, forRemote bool) ipc.ListAnswer {
	ans := ipc.ListAnswer{Downloads: []ipc.DownloadInfo{}, Queues: []ipc.QueueInfo{}, Categories: []ipc.CategoryInfo{}}
	list, _ := a.service.List(ctx)
	var total float64
	for _, d := range list {
		info := ipc.DownloadInfo{ID: d.ID, File: d.Filename, URL: d.URL, Status: apiStatus(d.Status), Downloaded: d.Downloaded}
		if forRemote {
			info.URL = withoutPassword(d.URL)
		} else if d.Destination != "" {
			info.Path = filepath.Join(d.Destination, d.Filename)
		}
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
		if !forRemote {
			info.Path = t.Folder
		}
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

// apiAct does action to each of ids: what was done, and what was skipped
// and why.
func (a *App) apiAct(ctx context.Context, action string, ids []string) ipc.ActAnswer {
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
			default:
				err = a.RemoveTorrent(hash, action == "delete")
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
		default: // remove, delete
			if d.Status.IsActive() {
				skip(id, "running: pause it first")
				continue
			}
			err = a.DeleteDownload(id, action == "delete")
		}
		if err != nil {
			skip(id, err.Error())
			continue
		}
		ans.Done = append(ans.Done, id)
	}
	return ans
}
