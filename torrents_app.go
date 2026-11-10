// Ported from Mister Download Manager, crates/app/src/torrents.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Torrents for the UI: a magnet link or a .torrent file is looked up (the
// files to choose from), added, watched ("torrents:progress"), paused,
// resumed and removed. The list is kept in <data>/torrents
// (torrent.List); the session starts with the first torrent, or at
// startup when the list holds one to run. A finished torrent stops sharing
// unless Keep sharing is on (MDM's D-018), and then at the sharing limits.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
	"github.com/vaisah/internal/torrent"
	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

// TorrentSettings are Options → Torrents. Torrents always go through the
// connection settings' SOCKS5 proxy, so peers never see this computer's
// address (there is no listening port, UPnP or DHT to set: each would show
// it). The blocklist applies when the session starts; a running one starts
// again when it changes.
type TorrentSettings struct {
	// Blocklist is a blocklist's address ("": none), fetched again every
	// torrent.BlocklistDays.
	Blocklist string `json:"blocklist"`
	// KeepSharing: finished torrents keep sharing (until a limit below).
	KeepSharing bool `json:"keepSharing"`
	// "Limit uploading to UploadLimitKB KBytes/sec", all torrents
	// together.
	UploadLimitOn bool  `json:"uploadLimitOn"`
	UploadLimitKB int64 `json:"uploadLimitKB"`
	// Stop sharing at a share ratio (in hundredths: 200 is 2.0), or after
	// StopMinutes of sharing once finished.
	StopRatioOn   bool  `json:"stopRatioOn"`
	StopRatio     int64 `json:"stopRatio"`
	StopMinutesOn bool  `json:"stopMinutesOn"`
	StopMinutes   int64 `json:"stopMinutes"`
}

const keyTorrents = "torrent.settings"

func defaultTorrentSettings() TorrentSettings {
	return TorrentSettings{UploadLimitKB: 100, StopRatio: 200, StopMinutes: 1440}
}

func (s TorrentSettings) limits() torrent.SharingLimits {
	var l torrent.SharingLimits
	if s.StopRatioOn {
		l.Ratio = float64(max(s.StopRatio, 1)) / 100
	}
	if s.StopMinutesOn {
		l.Secs = max(s.StopMinutes, 1) * 60
	}
	return l
}

func (s TorrentSettings) uploadLimit() int64 {
	if !s.UploadLimitOn {
		return 0
	}
	return max(s.UploadLimitKB, 1) << 10
}

// GetTorrentSettings returns Options → Torrents.
func (a *App) GetTorrentSettings() TorrentSettings {
	s := defaultTorrentSettings()
	if a.store == nil {
		return s
	}
	if raw, ok, _ := a.store.Settings().Get(context.Background(), keyTorrents); ok {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

// SaveTorrentSettings validates and saves Options → Torrents.
func (a *App) SaveTorrentSettings(s TorrentSettings) (TorrentSettings, error) {
	if s.UploadLimitKB < 0 || s.StopRatio < 0 || s.StopMinutes < 0 {
		return TorrentSettings{}, errors.New("limits must not be negative")
	}
	s.Blocklist = strings.TrimSpace(s.Blocklist)
	if s.Blocklist != "" {
		if _, err := webPageURL(s.Blocklist); err != nil {
			return TorrentSettings{}, fmt.Errorf("the blocklist's address: %w", err)
		}
	}
	old := a.GetTorrentSettings()
	raw, _ := json.Marshal(s)
	if err := a.store.Settings().Set(context.Background(), keyTorrents, string(raw)); err != nil {
		return TorrentSettings{}, err
	}
	if session := a.runningTorrents(); session != nil {
		session.SetUploadLimit(s.uploadLimit())
		if old.Blocklist != s.Blocklist {
			if err := a.restartTorrents(); err != nil {
				return s, fmt.Errorf("saved, but torrents could not start again: %w", err)
			}
		}
	}
	a.refreshBlocklist(s.Blocklist)
	return s, nil
}

// TorrentPreview is what a magnet link or .torrent holds, for the window
// that chooses its files and folder.
type TorrentPreview struct {
	Hash  string              `json:"hash"`
	Name  string              `json:"name"`
	Files []torrent.FileEntry `json:"files"`
	Total int64               `json:"total"`
	// Directory is the download folder; Existing are its files already
	// there; InList: the torrent is in the list already.
	Directory string   `json:"directory"`
	Existing  []string `json:"existing"`
	InList    bool     `json:"inList"`
}

// AddTorrentRequest is what that window submits.
type AddTorrentRequest struct {
	Hash      string `json:"hash"`
	Directory string `json:"directory"`
	// Files are indexes in TorrentPreview.Files (nil: all).
	Files  []int `json:"files"`
	Paused bool  `json:"paused"`
}

// TorrentRow is a torrent in the list.
type TorrentRow struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
	// Folder is where its files are (its own folder, for several).
	Folder   string        `json:"folder"`
	Added    time.Time     `json:"added"`
	Finished bool          `json:"finished"`
	Uploaded int64         `json:"uploaded"`
	Stats    torrent.Stats `json:"stats"`
}

// TorrentFilesView is a torrent's files and how much of each is there.
type TorrentFilesView struct {
	Files    []torrent.FileEntry `json:"files"`
	Chosen   []int               `json:"chosen"`
	FileDone []int64             `json:"fileDone"`
}

// torrentHost holds the session and the list.
type torrentHost struct {
	mu      sync.Mutex
	s       *torrent.Torrents
	list    *torrent.List
	pending map[string]torrent.Meta // looked up, not added yet
	stop    chan struct{}
	done    chan struct{}
	// blocklistBusy: a blocklist is being fetched.
	blocklistBusy atomic.Bool
}

func (a *App) torrentDir() string { return filepath.Join(a.dataDir, "torrents") }

// torrentList is the list, read once.
func (a *App) torrentList() (*torrent.List, error) {
	a.torrents.mu.Lock()
	defer a.torrents.mu.Unlock()
	return a.torrentListLocked()
}

func (a *App) torrentListLocked() (*torrent.List, error) {
	h := &a.torrents
	if h.list == nil {
		l, err := torrent.OpenList(a.torrentDir())
		if err != nil {
			return nil, err
		}
		h.list = l
	}
	return h.list, nil
}

// runningTorrents is the session if it runs (nil: it doesn't).
func (a *App) runningTorrents() *torrent.Torrents {
	a.torrents.mu.Lock()
	defer a.torrents.mu.Unlock()
	return a.torrents.s
}

// torrentSession is the session, started (with the list's torrents) when
// it isn't.
func (a *App) torrentSession() (*torrent.Torrents, error) {
	h := &a.torrents
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.s != nil {
		return h.s, nil
	}
	list, err := a.torrentListLocked()
	if err != nil {
		return nil, err
	}
	ts := a.GetTorrentSettings()
	// Only the connection settings' fixed proxy (SOCKS5) carries torrents:
	// without one, torrent.ErrNoProxy.
	cfg := torrent.Config{StateDir: a.torrentDir(), Proxy: a.torrentProxy(),
		Blocklist: torrent.LoadBlocklist(a.torrentDir(), ts.Blocklist)}
	s, err := torrent.Start(cfg)
	if err != nil {
		return nil, err
	}
	s.SetUploadLimit(ts.uploadLimit())
	for _, r := range list.All() {
		b, err := list.Torrent(r.Hash)
		if err == nil {
			_, err = s.Add(b, r.Dir, r.Files, r.Paused)
		}
		if err != nil {
			a.logf("torrent %s: %v", r.Name, err)
		}
	}
	h.s, h.stop, h.done = s, make(chan struct{}), make(chan struct{})
	go a.watchTorrents(s, h.stop, h.done)
	return s, nil
}

// torrentProxy is the proxy torrents go through: the connection settings'
// fixed one ("": none).
func (a *App) torrentProxy() string {
	if n := a.GetNetworkSettings(); n.ProxyMode == "fixed" {
		return n.ProxyURL
	}
	return ""
}

// restartTorrents starts a running session again (new settings, a new
// proxy); one that can't start stays stopped.
func (a *App) restartTorrents() error {
	if a.runningTorrents() == nil {
		return nil
	}
	a.stopTorrents()
	_, err := a.torrentSession()
	if err != nil && a.ctx != nil {
		a.notice(a.ctx, "error", "Torrents stopped: "+err.Error())
	}
	a.torrentsChanged()
	return err
}

// stopTorrents ends the session (the list stays).
func (a *App) stopTorrents() {
	h := &a.torrents
	h.mu.Lock()
	s, stop, done, list := h.s, h.stop, h.done, h.list
	h.s = nil
	h.mu.Unlock()
	if s == nil {
		return
	}
	close(stop)
	<-done
	s.Stop()
	if list != nil {
		if err := list.Save(); err != nil {
			a.logf("torrents: %v", err)
		}
	}
}

// resumeTorrents starts the session at startup when a torrent in the list
// is to run, and refreshes the blocklist.
func (a *App) resumeTorrents() {
	l, err := a.torrentList()
	if err != nil {
		a.logf("torrents: %v", err)
		return
	}
	if slices.ContainsFunc(l.All(), func(r torrent.Record) bool { return !r.Paused }) {
		if _, err := a.torrentSession(); err != nil {
			a.logf("torrents: %v", err)
		}
	}
	a.refreshBlocklist(a.GetTorrentSettings().Blocklist)
}

// watchTorrents tells the torrents' progress each second, adds up what
// they send, and stops finished torrents as the settings ask.
func (a *App) watchTorrents(s *torrent.Torrents, stop, done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var acct torrent.Accountant
	saved := time.Now()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		l, err := a.torrentList()
		if err != nil {
			continue
		}
		if a.watchOnce(s, l, &acct) && a.ctx != nil {
			wails.EventsEmit(a.ctx, "torrents:changed")
		}
		if time.Since(saved) > 30*time.Second {
			_ = l.Save()
			saved = time.Now()
		}
		if a.ctx != nil {
			wails.EventsEmit(a.ctx, "torrents:progress", a.torrentRows())
		}
	}
}

// watchOnce is one second of watchTorrents; true when a torrent finished
// or stopped.
func (a *App) watchOnce(s *torrent.Torrents, l *torrent.List, acct *torrent.Accountant) bool {
	ts := a.GetTorrentSettings()
	changed := false
	for _, r := range l.All() {
		st, err := s.Stats(r.Hash)
		if err != nil || st.Phase == torrent.Paused {
			acct.Forget(r.Hash)
			continue
		}
		sent := acct.Sent(r.Hash, st.Uploaded)
		finished := st.Phase == torrent.Finished
		l.Note(r.Hash, func(r *torrent.Record) {
			r.Uploaded += sent
			if finished && r.Finished {
				r.SeededSecs++
			}
		})
		if finished && !r.Finished {
			changed = true
			_, _ = l.Update(r.Hash, func(r *torrent.Record) { r.Finished = true })
			if a.ctx != nil {
				a.notice(a.ctx, "ok", "Finished "+r.Name)
			}
		}
		r, _ = l.Get(r.Hash)
		// A finished torrent stops, as any download does, unless it keeps
		// sharing; then at a sharing limit.
		if finished && (!ts.KeepSharing || ts.limits().Reached(r.Uploaded, st.Done, r.SeededSecs)) {
			changed = true
			_, _ = l.Update(r.Hash, func(r *torrent.Record) { r.Paused = true })
			_ = s.Pause(r.Hash)
			acct.Forget(r.Hash)
		}
	}
	return changed
}

func (a *App) logf(format string, args ...any) {
	if a.ctx != nil {
		wails.LogErrorf(a.ctx, format, args...)
	}
}

// LookUpTorrent reads what a magnet link (asking its peers, up to a
// minute) or a .torrent file (an absolute path) holds.
func (a *App) LookUpTorrent(source string) (TorrentPreview, error) {
	source = strings.TrimSpace(source)
	var (
		meta torrent.Meta
		err  error
	)
	switch {
	case torrent.IsMagnet(source):
		var s *torrent.Torrents
		if s, err = a.torrentSession(); err != nil {
			return TorrentPreview{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		meta, err = s.Metadata(ctx, source)
	case isTorrentPath(source):
		var b []byte
		if b, err = torrent.ReadFile(source); err == nil {
			meta, err = torrent.MetaFromTorrent(b)
		}
	default:
		err = torrent.ErrNotATorrent
	}
	if err != nil {
		return TorrentPreview{}, err
	}
	return a.preview(meta)
}

func isTorrentPath(p string) bool {
	return filepath.IsAbs(p) && strings.EqualFold(filepath.Ext(p), ".torrent")
}

// preview keeps a looked-up torrent until it is added.
func (a *App) preview(meta torrent.Meta) (TorrentPreview, error) {
	l, err := a.torrentList()
	if err != nil {
		return TorrentPreview{}, err
	}
	h := &a.torrents
	h.mu.Lock()
	if h.pending == nil {
		h.pending = map[string]torrent.Meta{}
	}
	h.pending[meta.InfoHash] = meta
	h.mu.Unlock()
	dir := a.GetSettings().DownloadDirectory
	_, in := l.Get(meta.InfoHash)
	return TorrentPreview{Hash: meta.InfoHash, Name: meta.Name, Files: meta.Files, Total: meta.Total(),
		Directory: dir, Existing: meta.ExistingIn(dir), InList: in}, nil
}

// SelectTorrentFile shows a native picker for a .torrent file.
func (a *App) SelectTorrentFile() (string, error) {
	return wails.OpenFileDialog(a.ctx, wails.OpenDialogOptions{Title: "Open a torrent file",
		Filters: []wails.FileFilter{{DisplayName: "Torrent files (*.torrent)", Pattern: "*.torrent"}}})
}

// AddTorrent adds a looked-up torrent with the files and folder chosen.
func (a *App) AddTorrent(req AddTorrentRequest) (TorrentRow, error) {
	h := &a.torrents
	hash := strings.ToLower(strings.TrimSpace(req.Hash))
	h.mu.Lock()
	meta, ok := h.pending[hash]
	h.mu.Unlock()
	if !ok {
		return TorrentRow{}, errors.New("look the torrent up first")
	}
	l, err := a.torrentList()
	if err != nil {
		return TorrentRow{}, err
	}
	if _, in := l.Get(hash); in {
		return TorrentRow{}, errors.New("this torrent is in the list already")
	}
	dir := a.saveDir(req.Directory)
	if !filepath.IsAbs(dir) {
		return TorrentRow{}, errors.New("the folder must be an absolute path")
	}
	files, err := chosenFiles(meta, req.Files)
	if err != nil {
		return TorrentRow{}, err
	}
	s, err := a.torrentSession()
	if err != nil {
		return TorrentRow{}, err
	}
	if _, err := s.Add(meta.Torrent, dir, files, req.Paused); err != nil {
		return TorrentRow{}, err
	}
	rec := torrent.Record{Hash: hash, Name: meta.Name, Dir: dir, Files: files, Paused: req.Paused, Added: time.Now()}
	if err := l.Add(rec, meta.Torrent); err != nil {
		_ = s.Remove(hash, false)
		return TorrentRow{}, err
	}
	h.mu.Lock()
	delete(h.pending, hash)
	h.mu.Unlock()
	a.torrentsChanged()
	return a.torrentRow(rec, s), nil
}

// chosenFiles checks the indexes chosen: in range, not padding, at least
// one (nil: all).
func chosenFiles(meta torrent.Meta, chosen []int) ([]int, error) {
	if chosen == nil {
		return nil, nil
	}
	var out []int
	for _, i := range chosen {
		if i < 0 || i >= len(meta.Files) || meta.Files[i].Padding {
			return nil, fmt.Errorf("no file %d in the torrent", i)
		}
		if !slices.Contains(out, i) {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("choose at least one file")
	}
	slices.Sort(out)
	return out, nil
}

func (a *App) torrentsChanged() {
	if a.ctx != nil {
		wails.EventsEmit(a.ctx, "torrents:changed")
	}
}

// ListTorrents are the torrents, oldest first.
func (a *App) ListTorrents() ([]TorrentRow, error) {
	if _, err := a.torrentList(); err != nil {
		return nil, err
	}
	return a.torrentRows(), nil
}

func (a *App) torrentRows() []TorrentRow {
	l, err := a.torrentList()
	if err != nil {
		return nil
	}
	s := a.runningTorrents()
	out := []TorrentRow{}
	for _, r := range l.All() {
		out = append(out, a.torrentRow(r, s))
	}
	return out
}

func (a *App) torrentRow(r torrent.Record, s *torrent.Torrents) TorrentRow {
	row := TorrentRow{Hash: r.Hash, Name: r.Name, Dir: r.Dir, Folder: r.Dir, Added: r.Added, Finished: r.Finished, Uploaded: r.Uploaded,
		Stats: torrent.Stats{Phase: torrent.Paused}}
	if s == nil {
		return row
	}
	if m, err := s.Meta(r.Hash); err == nil {
		row.Folder = m.FolderIn(r.Dir)
	}
	if st, err := s.Stats(r.Hash); err == nil {
		row.Stats = st
	}
	return row
}

// torrentMeta is what a torrent in the list holds, from its .torrent.
func (a *App) torrentMeta(hash string) (torrent.Record, torrent.Meta, error) {
	l, err := a.torrentList()
	if err != nil {
		return torrent.Record{}, torrent.Meta{}, err
	}
	r, ok := l.Get(hash)
	if !ok {
		return torrent.Record{}, torrent.Meta{}, torrent.ErrUnknown
	}
	b, err := l.Torrent(r.Hash)
	if err != nil {
		return r, torrent.Meta{}, err
	}
	m, err := torrent.MetaFromTorrent(b)
	return r, m, err
}

// TorrentFiles are a torrent's files, those chosen, and how much of each
// is there.
func (a *App) TorrentFiles(hash string) (TorrentFilesView, error) {
	r, meta, err := a.torrentMeta(hash)
	if err != nil {
		return TorrentFilesView{}, err
	}
	v := TorrentFilesView{Files: meta.Files, Chosen: r.Files}
	if s := a.runningTorrents(); s != nil {
		if st, err := s.Stats(r.Hash); err == nil {
			v.FileDone = st.FileDone
		}
	}
	return v, nil
}

// TorrentPeers are a running torrent's peers.
func (a *App) TorrentPeers(hash string) []torrent.Peer {
	if s := a.runningTorrents(); s != nil {
		return s.Peers(hash)
	}
	return nil
}

// PauseTorrent stops a torrent (what is there stays).
func (a *App) PauseTorrent(hash string) error {
	l, err := a.torrentList()
	if err != nil {
		return err
	}
	if ok, err := l.Update(hash, func(r *torrent.Record) { r.Paused = true }); !ok || err != nil {
		return orUnknown(err)
	}
	if s := a.runningTorrents(); s != nil {
		if err := s.Pause(hash); err != nil {
			return err
		}
	}
	a.torrentsChanged()
	return nil
}

// ResumeTorrent starts a torrent again. A finished one checks its files,
// then shares if Keep sharing is on (else it stops again).
func (a *App) ResumeTorrent(hash string) error {
	l, err := a.torrentList()
	if err != nil {
		return err
	}
	if ok, err := l.Update(hash, func(r *torrent.Record) { r.Paused, r.SeededSecs = false, 0 }); !ok || err != nil {
		return orUnknown(err)
	}
	s, err := a.torrentSession()
	if err != nil {
		return err
	}
	if err := s.Resume(hash); err != nil {
		return err
	}
	a.torrentsChanged()
	return nil
}

// RemoveTorrent takes a torrent out of the list, and its files with it
// when deleteFiles.
func (a *App) RemoveTorrent(hash string, deleteFiles bool) error {
	r, meta, metaErr := a.torrentMeta(hash)
	if errors.Is(metaErr, torrent.ErrUnknown) {
		return metaErr
	}
	l, err := a.torrentList()
	if err != nil {
		return err
	}
	var removeErr error
	if s := a.runningTorrents(); s != nil && s.Has(r.Hash) {
		removeErr = s.Remove(r.Hash, false)
	}
	if deleteFiles && removeErr == nil {
		removeErr = metaErr
		if metaErr == nil {
			removeErr = torrent.RemoveFiles(meta, r.Dir)
		}
	}
	if err := l.Delete(r.Hash); err != nil {
		return err
	}
	a.torrentsChanged()
	if removeErr != nil {
		return fmt.Errorf("removed from the list, but its files could not all be deleted: %w", removeErr)
	}
	return nil
}

func orUnknown(err error) error {
	if err != nil {
		return err
	}
	return torrent.ErrUnknown
}

// TorrentMagnet is a magnet link for a torrent in the list, with its
// trackers.
func (a *App) TorrentMagnet(hash string) (string, error) {
	l, err := a.torrentList()
	if err != nil {
		return "", err
	}
	b, err := l.Torrent(hash)
	if err != nil {
		return "", torrent.ErrUnknown
	}
	link, ok := torrent.MagnetLink(b)
	if !ok {
		return "", torrent.ErrNotATorrent
	}
	return link, nil
}

// ShowTorrentFolder reveals a torrent's files in the file manager.
func (a *App) ShowTorrentFolder(hash string) error {
	r, meta, err := a.torrentMeta(hash)
	if err != nil {
		return err
	}
	target := r.Dir
	if p := filepath.Join(r.Dir, meta.Name); fileExists(p) {
		target = p
	}
	return platform.RevealPath(target)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// refreshBlocklist fetches the blocklist at url in the background when it
// was never converted, comes from another address now, or is older than
// torrent.BlocklistDays, one at a time. The session reads it when it next
// starts.
func (a *App) refreshBlocklist(url string) {
	h := &a.torrents
	if url == "" || torrent.BlocklistFresh(a.torrentDir(), url, time.Now()) || h.blocklistBusy.Swap(true) {
		return
	}
	go func() {
		defer h.blocklistBusy.Store(false)
		if err := a.fetchBlocklist(url); err != nil {
			a.logf("blocklist: %v", err)
		}
	}()
}

// fetchBlocklist fetches a blocklist (public addresses only, through the
// network settings' proxy) and keeps it converted.
func (a *App) fetchBlocklist(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "YellowFlash/"+ipc.AppVersion)
	resp, err := network.NewClient(a.helperNetwork()).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, torrent.MaxBlocklist+1))
	if err != nil {
		return err
	}
	if len(b) > torrent.MaxBlocklist {
		return fmt.Errorf("%s is larger than a blocklist can be", url)
	}
	_, ok, err := torrent.StoreBlocklist(a.torrentDir(), url, b)
	if err == nil && !ok {
		err = fmt.Errorf("%s is not a list Yellow Flash can read", url)
	}
	return err
}

// isTorrentCommand: a magnet link, a .torrent file's path, or a .torrent's
// bytes (remote control).
func isTorrentCommand(cmd ipc.Command) bool {
	u := strings.TrimSpace(cmd.URL)
	return len(cmd.TorrentFile) > 0 || torrent.IsMagnet(u) || isTorrentPath(u)
}

// torrentCommand handles a magnet link or a .torrent file sent to the app
// (the command line, the browser, remote control): looked up, then the
// window to choose its files ("app:add-torrent"), or (/n, /a) added whole
// to the folder named.
func (a *App) torrentCommand(ctx context.Context, cmd ipc.Command) {
	silent := cmd.Silent || cmd.Later
	if !silent {
		a.focusWindow(ctx)
	}
	go func() {
		var (
			p   TorrentPreview
			err error
		)
		if len(cmd.TorrentFile) > 0 {
			var meta torrent.Meta
			if meta, err = torrent.MetaFromTorrent(cmd.TorrentFile); err == nil {
				p, err = a.preview(meta)
			}
		} else {
			if torrent.IsMagnet(cmd.URL) && !silent {
				a.notice(ctx, "info", "Asking the torrent's peers for its files…")
			}
			p, err = a.LookUpTorrent(cmd.URL)
		}
		if err != nil {
			a.notice(ctx, "error", "Could not open the torrent: "+err.Error())
			return
		}
		if !silent {
			if cmd.Dir != "" {
				p.Directory, p.Existing = cmd.Dir, nil
			}
			wails.EventsEmit(ctx, "app:add-torrent", p)
			return
		}
		if _, err := a.AddTorrent(AddTorrentRequest{Hash: p.Hash, Directory: cmd.Dir, Paused: cmd.Later}); err != nil {
			a.notice(ctx, "error", "Could not add "+p.Name+": "+err.Error())
			return
		}
		a.notice(ctx, "ok", "Added the torrent "+p.Name)
	}()
}
