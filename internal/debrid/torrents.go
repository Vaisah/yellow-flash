// Ported from Mister Download Manager, crates/debrid/src/torrents.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// Torrents through the service: a magnet link goes to the user's account,
// the service downloads the torrent on its own servers, and its files then
// come over HTTP through the engine (every connection, resume). Nothing
// connects to peers from this computer, so no peer ever sees its address.
// A .torrent file is sent as its magnet link (the app makes it).
//
// Each service from its public API documentation (read 2026-11-06 for MDM):
//   - Real-Debrid: torrents/addMagnet, torrents/info/{id},
//     torrents/selectFiles/{id} (files=all), torrents/delete/{id}; the
//     finished torrent's links are its own hoster links, unrestricted as any.
//   - AllDebrid: v4/magnet/upload (magnets[]), v4.1/magnet/status
//     (statusCode 0–3 working, 4 ready, 5 and up failed), v4/magnet/files
//     (id[], a tree of n/s/l/e), v4/magnet/delete; the files' links are
//     unlocked as any.
//   - Premiumize: transfer/create (src), transfer/list, folder/list (walked)
//     or item/details for a single file, transfer/delete; its files' links
//     are direct links to the user's own cloud.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vaisah/internal/engine"
	"github.com/vaisah/internal/infrastructure/filesystem"
)

// RemoteFile is one finished file at the service.
type RemoteFile struct {
	// Path is its path in the torrent, '/'-separated ("": the service names
	// it when it is unlocked).
	Path string
	// Size is 0 when unknown.
	Size int64
	// Link is the service's link to it: unlocked through the account
	// (Real-Debrid, AllDebrid) or a direct link to the user's cloud
	// (Premiumize). Never shown.
	Link string
}

// torrentStatus is how the service is doing with a torrent.
type torrentStatus struct {
	ready bool
	// failed is why it can't be had.
	failed error
	// progress is how far it is (0 to 1; -1 when it doesn't say), and what
	// its own word for it ("downloading").
	progress float64
	what     string
	// name and files, once ready.
	name  string
	files []RemoteFile
}

func working(progress float64, what string) torrentStatus {
	return torrentStatus{progress: progress, what: what}
}

/* ---------- each service's calls ---------- */

func (s *session) torrentAdd(ctx context.Context, magnet string) (string, error) {
	switch s.account.Service {
	case AllDebrid:
		key, _ := s.token(ctx, false)
		var u struct {
			Magnets []struct {
				ID    *int64       `json:"id"`
				Error *adErrorBody `json:"error"`
			} `json:"magnets"`
		}
		if err := adCall(ctx, s.client, http.MethodPost, "v4/magnet/upload", url.Values{"magnets[]": {magnet}}, key, &u); err != nil {
			return "", err
		}
		switch {
		case len(u.Magnets) > 0 && u.Magnets[0].ID != nil:
			return fmt.Sprint(*u.Magnets[0].ID), nil
		case len(u.Magnets) > 0 && u.Magnets[0].Error != nil:
			return "", adFault(u.Magnets[0].Error.Code, u.Magnets[0].Error.Message)
		}
		return "", fault(engine.FaultServiceDown)
	case Premiumize:
		key, _ := s.token(ctx, false)
		var c struct {
			ID json.RawMessage `json:"id"`
		}
		if err := pmCall(ctx, s.client, http.MethodPost, "transfer/create", url.Values{"src": {magnet}}, key, &c); err != nil {
			return "", err
		}
		if id := rawID(c.ID); id != "" {
			return id, nil
		}
		return "", fault(engine.FaultServiceDown)
	default:
		var a struct {
			ID string `json:"id"`
		}
		err := s.withToken(ctx, func(t string) error {
			return rdCall(ctx, s.client, http.MethodPost, s.client.services.RDRest+"torrents/addMagnet",
				url.Values{"magnet": {magnet}}, t, &a)
		})
		return a.ID, err
	}
}

func (s *session) torrentStatus(ctx context.Context, id string) (torrentStatus, error) {
	id, err := plainID(id)
	if err != nil {
		return torrentStatus{}, err
	}
	switch s.account.Service {
	case AllDebrid:
		key, _ := s.token(ctx, false)
		return adMagnetStatus(ctx, s.client, key, id)
	case Premiumize:
		key, _ := s.token(ctx, false)
		return pmTransferStatus(ctx, s.client, key, id)
	default:
		var st torrentStatus
		err := s.withToken(ctx, func(t string) error {
			var err error
			st, err = rdTorrentStatus(ctx, s.client, t, id)
			return err
		})
		return st, err
	}
}

func (s *session) torrentDelete(ctx context.Context, id string) error {
	id, err := plainID(id)
	if err != nil {
		return err
	}
	var ignored json.RawMessage
	switch s.account.Service {
	case AllDebrid:
		key, _ := s.token(ctx, false)
		return adCall(ctx, s.client, http.MethodPost, "v4/magnet/delete", url.Values{"id": {id}}, key, &ignored)
	case Premiumize:
		key, _ := s.token(ctx, false)
		return pmCall(ctx, s.client, http.MethodPost, "transfer/delete", url.Values{"id": {id}}, key, &ignored)
	default:
		return s.withToken(ctx, func(t string) error {
			return rdCall(ctx, s.client, http.MethodDelete, s.client.services.RDRest+"torrents/delete/"+id, nil, t, nil)
		})
	}
}

// rawID reads an id given as a string or a number.
func rawID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// rdInfo is Real-Debrid's torrents/info/{id}.
type rdInfo struct {
	Filename string  `json:"filename"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Files    []struct {
		Path     string `json:"path"`
		Bytes    int64  `json:"bytes"`
		Selected int    `json:"selected"`
	} `json:"files"`
	Links []string `json:"links"`
}

// rdTorrentStatus asks Real-Debrid; a torrent waiting for its files to be
// chosen gets all of them.
func rdTorrentStatus(ctx context.Context, c *Client, token, id string) (torrentStatus, error) {
	var info rdInfo
	if err := rdCall(ctx, c, http.MethodGet, c.services.RDRest+"torrents/info/"+id, nil, token, &info); err != nil {
		return torrentStatus{}, err
	}
	if info.Status == "waiting_files_selection" {
		err := rdCall(ctx, c, http.MethodPost, c.services.RDRest+"torrents/selectFiles/"+id,
			url.Values{"files": {"all"}}, token, nil)
		return working(0, "files chosen"), err
	}
	return rdStatusOf(info), nil
}

// rdStatusOf is what Real-Debrid's answer means.
func rdStatusOf(i rdInfo) torrentStatus {
	switch i.Status {
	case "downloaded":
		var chosen []RemoteFile
		for _, f := range i.Files {
			if f.Selected == 1 {
				chosen = append(chosen, RemoteFile{Path: f.Path, Size: f.Bytes})
			}
		}
		// One link per chosen file, in order; otherwise (packed in an
		// archive) the service names each link's file itself.
		files := make([]RemoteFile, 0, len(i.Links))
		for n, l := range i.Links {
			f := RemoteFile{Link: l}
			if len(chosen) == len(i.Links) {
				f.Path, f.Size = chosen[n].Path, chosen[n].Size
			}
			files = append(files, f)
		}
		return torrentStatus{ready: true, name: i.Filename, files: files}
	case "magnet_error", "dead":
		return torrentStatus{failed: fault(engine.FaultTorrentUnavailable)}
	case "virus":
		return torrentStatus{failed: fault(engine.FaultFileRefused)}
	case "error":
		return torrentStatus{failed: fault(engine.FaultServiceDown)}
	}
	return working(i.Progress/100, strings.ReplaceAll(i.Status, "_", " "))
}

// adMagnet is AllDebrid's magnet/status entry (v4.1).
type adMagnet struct {
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
	Downloaded int64  `json:"downloaded"`
}

// adStatusOf is what AllDebrid's statusCode means; false for ready (4),
// which magnet/files completes.
func adStatusOf(m adMagnet) (torrentStatus, bool) {
	switch {
	case m.StatusCode >= 0 && m.StatusCode <= 3:
		p := -1.0
		if m.Size > 0 {
			p = float64(m.Downloaded) / float64(m.Size)
		}
		return working(p, strings.ToLower(m.Status)), true
	case m.StatusCode == 4:
		return torrentStatus{}, false
	case m.StatusCode == 11:
		return torrentStatus{failed: fault(engine.FaultFileUnavailable)}, true
	case m.StatusCode == 15:
		return torrentStatus{failed: fault(engine.FaultTorrentUnavailable)}, true
	}
	words := m.Status
	if words == "" {
		words = fmt.Sprintf("error %d", m.StatusCode)
	}
	return torrentStatus{failed: other(words)}, true
}

// adFileNode is AllDebrid's magnet/files tree: a folder (n, e) or a file
// (n, s, l).
type adFileNode struct {
	N string       `json:"n"`
	S int64        `json:"s"`
	L *string      `json:"l"`
	E []adFileNode `json:"e"`
}

func adFlatten(nodes []adFileNode, prefix string, out *[]RemoteFile) {
	for _, n := range nodes {
		p := n.N
		if prefix != "" {
			p = prefix + "/" + n.N
		}
		if n.L != nil {
			*out = append(*out, RemoteFile{Path: p, Size: n.S, Link: *n.L})
		} else {
			adFlatten(n.E, p, out)
		}
	}
}

// adMagnetStatus asks v4.1/magnet/status, then, once ready, v4/magnet/files.
func adMagnetStatus(ctx context.Context, c *Client, key, id string) (torrentStatus, error) {
	var m struct {
		Magnets json.RawMessage `json:"magnets"`
	}
	if err := adCall(ctx, c, http.MethodPost, "v4.1/magnet/status", url.Values{"id": {id}}, key, &m); err != nil {
		return torrentStatus{}, err
	}
	// One magnet, or a list of them.
	var one adMagnet
	var many []adMagnet
	if json.Unmarshal(m.Magnets, &many) == nil {
		if len(many) == 0 {
			return torrentStatus{}, fault(engine.FaultFileUnavailable)
		}
		one = many[0]
	} else if json.Unmarshal(m.Magnets, &one) != nil {
		return torrentStatus{}, errUnreadable
	}
	if st, done := adStatusOf(one); done {
		return st, nil
	}
	var f struct {
		Magnets []struct {
			Files []adFileNode `json:"files"`
			Error *adErrorBody `json:"error"`
		} `json:"magnets"`
	}
	if err := adCall(ctx, c, http.MethodPost, "v4/magnet/files", url.Values{"id[]": {id}}, key, &f); err != nil {
		return torrentStatus{}, err
	}
	if len(f.Magnets) == 0 {
		return torrentStatus{}, fault(engine.FaultFileUnavailable)
	}
	if e := f.Magnets[0].Error; e != nil {
		return torrentStatus{}, adFault(e.Code, e.Message)
	}
	var files []RemoteFile
	adFlatten(f.Magnets[0].Files, "", &files)
	return torrentStatus{ready: true, name: one.Filename, files: files}, nil
}

// pmTransfer is Premiumize's transfer/list entry.
type pmTransfer struct {
	ID       json.RawMessage `json:"id"`
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Progress *float64        `json:"progress"`
	Message  *string         `json:"message"`
	FolderID *string         `json:"folder_id"`
	FileID   *string         `json:"file_id"`
}

// pmWorking is what a transfer's status means while not finished (false:
// finished).
func pmWorking(t pmTransfer) (torrentStatus, bool) {
	switch t.Status {
	case "finished", "seeding":
		return torrentStatus{}, false
	case "error", "deleted", "banned", "timeout":
		words := t.Status
		if t.Message != nil && *t.Message != "" {
			words = *t.Message
		}
		return torrentStatus{failed: other(words)}, true
	}
	p := -1.0
	if t.Progress != nil {
		p = *t.Progress
	}
	return working(p, t.Status), true
}

// pmItem is a file or folder in the user's cloud.
type pmItem struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Link string `json:"link"`
	ID   string `json:"id"`
	Type string `json:"type"`
}

// pmTransferStatus asks transfer/list, then for a finished one item/details
// (one file) or folder/list, walked (several).
func pmTransferStatus(ctx context.Context, c *Client, key, id string) (torrentStatus, error) {
	var l struct {
		Transfers []pmTransfer `json:"transfers"`
	}
	if err := pmCall(ctx, c, http.MethodGet, "transfer/list", nil, key, &l); err != nil {
		return torrentStatus{}, err
	}
	var t *pmTransfer
	for i := range l.Transfers {
		if rawID(l.Transfers[i].ID) == id {
			t = &l.Transfers[i]
		}
	}
	if t == nil {
		return torrentStatus{failed: fault(engine.FaultFileUnavailable)}, nil
	}
	if st, busy := pmWorking(*t); busy {
		return st, nil
	}
	var files []RemoteFile
	switch {
	case t.FileID != nil && *t.FileID != "":
		fid, err := plainID(*t.FileID)
		if err != nil {
			return torrentStatus{}, err
		}
		var it pmItem
		if err := pmCall(ctx, c, http.MethodGet, "item/details?"+url.Values{"id": {fid}}.Encode(), nil, key, &it); err != nil {
			return torrentStatus{}, err
		}
		files = append(files, RemoteFile{Path: it.Name, Size: it.Size, Link: it.Link})
	case t.FolderID != nil && *t.FolderID != "":
		// A folder's path before its files'; at most 64 folders.
		type todo struct{ id, prefix string }
		queue := []todo{{*t.FolderID, ""}}
		for seen := 0; len(queue) > 0 && seen < 64; seen++ {
			next := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			fid, err := plainID(next.id)
			if err != nil {
				return torrentStatus{}, err
			}
			var f struct {
				Content []pmItem `json:"content"`
			}
			if err := pmCall(ctx, c, http.MethodGet, "folder/list?"+url.Values{"id": {fid}}.Encode(), nil, key, &f); err != nil {
				return torrentStatus{}, err
			}
			for _, it := range f.Content {
				p := it.Name
				if next.prefix != "" {
					p = next.prefix + "/" + it.Name
				}
				if it.Type == "folder" {
					queue = append(queue, todo{it.ID, p})
				} else if it.Link != "" {
					files = append(files, RemoteFile{Path: p, Size: it.Size, Link: it.Link})
				}
			}
		}
	}
	return torrentStatus{ready: true, name: t.Name, files: files}, nil
}

/* ---------- a torrent through the service ---------- */

// SafePath is a path from the service, made safe to write under the
// torrent's folder: each part a file name Yellow Flash accepts, no "..", no
// root, nothing empty. false: nothing usable is left.
func SafePath(p string) (string, bool) {
	var parts []string
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", false
		}
		clean, err := filesystem.SanitizeFilename(part)
		if err != nil || clean == "" || clean == "." || clean == ".." {
			return "", false
		}
		parts = append(parts, clean)
	}
	if len(parts) == 0 {
		return "", false
	}
	return filepath.Join(parts...), true
}

// Fetcher downloads one of a torrent's files (the app's engine): link into
// dir, named name ("": the server's or the service's name for it), to its
// end. It returns the file's path and size.
type Fetcher func(ctx context.Context, link, dir, name string) (path string, size int64, err error)

// Order is what the app gives a torrent through the service.
type Order struct {
	// Account is the account it goes to.
	Account string
	// Magnet is the magnet link (also the list's address).
	Magnet string
	// Transfer is its id at the service when it was sent already (a resumed
	// torrent).
	Transfer string
	// Folder is where its files go, in their paths.
	Folder string
	// Poll is how often the service is asked while it works (default 5 s).
	Poll time.Duration
}

// Hooks tell the app how a torrent goes. Any may be nil.
type Hooks struct {
	// Sent gives the torrent's id at the service, to keep for a restart.
	Sent func(id string)
	// Note is a line for the event log.
	Note func(string)
	// AtService says how far the service is (0 to 1; -1 when it doesn't say).
	AtService func(progress float64)
	// Files says the files' bytes done so far, and in all (0: unknown),
	// before each file and after the last.
	Files func(done, total int64)
}

// Result is a torrent that came down: its folder (one file: that file) and
// its size.
type Result struct {
	Path string
	Size int64
}

// TorrentAccount is the account torrents go to: the first one (every
// service takes magnets).
func (d *Debrid) TorrentAccount() (id string, kind Kind, ok bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.sessions) == 0 {
		return "", "", false
	}
	return d.sessions[0].account.ID, d.sessions[0].account.Service, true
}

func (d *Debrid) session(id string) *session {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, s := range d.sessions {
		if s.account.ID == id {
			return s
		}
	}
	return nil
}

// pin makes link go through s whatever its host (a torrent's file links,
// such as Real-Debrid's own, may be on no host list); the returned func
// takes it back.
func (d *Debrid) pin(link string, s *session) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pinned == nil {
		d.pinned = map[string]*session{}
	}
	d.pinned[link] = s
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.pinned, link)
	}
}

// RunTorrent sends order's magnet to its account (or continues its
// transfer), waits for the service, downloads its files through fetch into
// the torrent's folder, then removes it from the account's list. It blocks
// until the end; cancelling ctx stops it and keeps the transfer at the
// service (Order.Transfer continues it).
func (d *Debrid) RunTorrent(ctx context.Context, order Order, fetch Fetcher, hooks Hooks) (Result, error) {
	s := d.session(order.Account)
	if s == nil {
		return Result{}, fault(engine.FaultSignIn)
	}
	res, err := d.runTorrent(ctx, s, order, fetch, hooks)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return res, named(s.account.Service, err)
}

func (d *Debrid) runTorrent(ctx context.Context, s *session, order Order, fetch Fetcher, hooks Hooks) (Result, error) {
	service := s.account.Service.Name()
	note := func(format string, a ...any) {
		if hooks.Note != nil {
			hooks.Note(fmt.Sprintf(format, a...))
		}
	}
	poll := order.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	id := order.Transfer
	if id == "" {
		var err error
		if id, err = s.torrentAdd(ctx, order.Magnet); err != nil {
			return Result{}, err
		}
		note("Sent to %s", service)
		if hooks.Sent != nil {
			hooks.Sent(id)
		}
	}
	// The service at work: asked every poll, the event log told when its
	// word or the percentage (by tens) changes.
	var st torrentStatus
	said := ""
	for {
		var err error
		if st, err = s.torrentStatus(ctx, id); err != nil {
			return Result{}, err
		}
		if st.failed != nil {
			return Result{}, st.failed
		}
		if st.ready {
			break
		}
		if hooks.AtService != nil {
			hooks.AtService(st.progress)
		}
		line := fmt.Sprintf("%s is downloading the torrent: %s", service, st.what)
		if st.progress >= 0 {
			line += fmt.Sprintf(", %d%%", int(math.Floor(min(st.progress, 1)*10))*10)
		}
		if line != said {
			note("%s", line)
			said = line
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return Result{}, ctx.Err()
		case <-t.C:
		}
	}
	files := st.files
	note("%s has the torrent: %d file(s), downloading them", service, len(files))
	var total int64
	for _, f := range files {
		total += max(f.Size, 0)
	}
	// As torrent programs do: one file goes into the folder itself, several
	// into a folder named after the torrent (a path that already starts
	// with that name isn't doubled).
	folder := order.Folder
	if len(files) != 1 {
		sub, ok := SafePath(st.name)
		if !ok {
			sub = "torrent"
		}
		folder = filepath.Join(order.Folder, sub)
	}
	within := func(p string) string {
		t := strings.TrimLeft(strings.ReplaceAll(p, `\`, "/"), "/")
		if first, rest, found := strings.Cut(t, "/"); found && len(files) > 1 && strings.TrimSpace(first) == strings.TrimSpace(st.name) {
			return rest
		}
		return t
	}
	// Every path and link is checked before any file is written.
	dests := make([]string, len(files))
	for i, f := range files {
		if !usable(f.Link) {
			return Result{}, other("the service gave a link Yellow Flash can't read")
		}
		rel, ok := SafePath(within(f.Path))
		switch {
		case ok:
			dests[i] = filepath.Join(folder, rel)
		case strings.TrimSpace(f.Path) == "":
			dests[i] = "" // the service's own name for it, in the folder
		default:
			return Result{}, &engine.ServiceError{Fault: engine.FaultUnsafeName, Detail: f.Path}
		}
	}
	// Real-Debrid's and AllDebrid's links are their hoster links: unlocked
	// through the account, as any. Premiumize's are direct.
	through := s.account.Service != Premiumize
	var done int64
	var result Result
	for i, f := range files {
		if hooks.Files != nil {
			hooks.Files(done, total)
		}
		dir, fname := folder, ""
		if dests[i] != "" {
			dir, fname = filepath.Dir(dests[i]), filepath.Base(dests[i])
			if fi, err := os.Stat(dests[i]); err == nil && fi.Mode().IsRegular() && !exists(filesystem.TempPath(dir, fname)) &&
				(f.Size <= 0 || fi.Size() == f.Size) {
				done += fi.Size() // done in an earlier run
				result = Result{Path: dests[i], Size: fi.Size()}
				continue
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, other(err.Error())
		}
		release := func() {}
		if through {
			release = d.pin(f.Link, s)
		}
		path, size, err := fetch(ctx, f.Link, dir, fname)
		release()
		if err != nil {
			return Result{}, err
		}
		done += size
		result = Result{Path: path, Size: size}
	}
	if hooks.Files != nil {
		hooks.Files(done, max(total, done))
	}
	// Everything is here: the torrent goes from the account's list
	// (Premiumize: the record only; the files stay in the user's cloud).
	if err := s.torrentDelete(context.WithoutCancel(ctx), id); err != nil {
		note("Not removed from %s's list: %v", service, named(s.account.Service, err))
	} else {
		note("Removed from %s's list", service)
	}
	if len(files) != 1 {
		result = Result{Path: folder, Size: done}
	}
	return result, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}
