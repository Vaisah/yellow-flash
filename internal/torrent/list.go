// Ported from Mister Download Manager, crates/app/src/torrents.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

// The app's torrents, kept between runs in the state folder: their list
// (torrents.json), a copy of each .torrent (files/<hash>.torrent, so a
// torrent is added again without asking the swarm), and the blocklist, as
// converted (blocklist.p2p) with the address it came from
// (blocklist.source).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Record is a torrent in the list.
type Record struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	// Dir is the folder it was saved in (its own folder, for several
	// files, is inside).
	Dir string `json:"dir"`
	// Files are the indexes chosen in its Meta.Files (nil: all).
	Files []int `json:"files,omitempty"`
	// Paused: stopped by the person (or by a sharing limit, once
	// finished).
	Paused bool `json:"paused"`
	// Finished: everything chosen was there once.
	Finished bool      `json:"finished"`
	Added    time.Time `json:"added"`
	// Uploaded is what it sent, run after run; SeededSecs how long it
	// shared once finished.
	Uploaded   int64 `json:"uploaded"`
	SeededSecs int64 `json:"seededSecs"`
}

// List is the torrents kept in a state folder.
type List struct {
	dir  string
	mu   sync.Mutex
	recs []Record
}

func (l *List) path() string { return filepath.Join(l.dir, "torrents.json") }

// TorrentPath is where the .torrent for hash is kept.
func (l *List) TorrentPath(hash string) string {
	return filepath.Join(l.dir, "files", strings.ToLower(hash)+".torrent")
}

// OpenList reads the list kept in dir (none yet: an empty list).
func OpenList(dir string) (*List, error) {
	l := &List{dir: dir}
	b, err := os.ReadFile(l.path())
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &l.recs); err != nil {
		return nil, err
	}
	// Only records with a hash and their .torrent are torrents.
	l.recs = slices.DeleteFunc(l.recs, func(r Record) bool {
		_, err := os.Stat(l.TorrentPath(r.Hash))
		return r.Hash == "" || err != nil
	})
	return l, nil
}

// All are the torrents, oldest first.
func (l *List) All() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := slices.Clone(l.recs)
	slices.SortStableFunc(out, func(a, b Record) int { return a.Added.Compare(b.Added) })
	return out
}

// Get is a torrent's record.
func (l *List) Get(hash string) (Record, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.index(hash)
	if i < 0 {
		return Record{}, false
	}
	return l.recs[i], true
}

func (l *List) index(hash string) int {
	return slices.IndexFunc(l.recs, func(r Record) bool { return strings.EqualFold(r.Hash, hash) })
}

// Add keeps a torrent: its record and its .torrent.
func (l *List) Add(r Record, torrent []byte) error {
	r.Hash = strings.ToLower(r.Hash)
	if err := writeAtomic(l.TorrentPath(r.Hash), torrent); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if i := l.index(r.Hash); i >= 0 {
		l.recs[i] = r
	} else {
		l.recs = append(l.recs, r)
	}
	return l.saveLocked()
}

// Update changes a torrent's record (false: not in the list).
func (l *List) Update(hash string, change func(*Record)) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.index(hash)
	if i < 0 {
		return false, nil
	}
	change(&l.recs[i])
	return true, l.saveLocked()
}

// Note changes a torrent's record in memory only (counters that change
// every second), kept at the next Save, Update, Add or Delete.
func (l *List) Note(hash string, change func(*Record)) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.index(hash)
	if i < 0 {
		return false
	}
	change(&l.recs[i])
	return true
}

// Save keeps the list as it is now.
func (l *List) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.saveLocked()
}

// Delete forgets a torrent, and its .torrent.
func (l *List) Delete(hash string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i := l.index(hash); i >= 0 {
		l.recs = slices.Delete(l.recs, i, i+1)
	}
	if err := os.Remove(l.TorrentPath(hash)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return l.saveLocked()
}

// Torrent is the .torrent kept for hash.
func (l *List) Torrent(hash string) ([]byte, error) {
	return os.ReadFile(l.TorrentPath(hash))
}

func (l *List) saveLocked() error {
	b, err := json.MarshalIndent(l.recs, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(l.path(), b)
}

// writeAtomic writes through a temporary file: a half-written list or
// .torrent would lose torrents.
func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// SharingLimits are when a finished torrent stops sharing (zero: no such
// limit).
type SharingLimits struct {
	// Ratio is sent ÷ what is there.
	Ratio float64
	// Secs is how long it shares once finished.
	Secs int64
}

// Reached reports a limit reached by a torrent that sent uploaded, has
// done bytes there and shared seeded seconds (the ratio first).
func (l SharingLimits) Reached(uploaded, done, seeded int64) bool {
	if l.Ratio > 0 && done > 0 && float64(uploaded)/float64(done) >= l.Ratio {
		return true
	}
	return l.Secs > 0 && seeded >= l.Secs
}

// Accountant adds up what each torrent sent, run after run: a torrent's
// count starts at 0 again each time it resumes, so a smaller count than the
// last one seen is all new.
type Accountant struct{ seen map[string]int64 }

// Sent is what hash sent since it was last seen; live is its count now.
func (a *Accountant) Sent(hash string, live int64) int64 {
	if a.seen == nil {
		a.seen = map[string]int64{}
	}
	last, ok := a.seen[hash]
	a.seen[hash] = live
	if ok && live >= last {
		return live - last
	}
	return live
}

// Forget drops hash (it stopped, or left the list).
func (a *Accountant) Forget(hash string) { delete(a.seen, hash) }

const (
	blocklistFile   = "blocklist.p2p"
	blocklistSource = "blocklist.source"
	// BlocklistDays is how often a blocklist is fetched again.
	BlocklistDays = 7
	// MaxBlocklist is the most a fetched blocklist may be (the largest
	// public lists, gzipped, are a few tens of MB).
	MaxBlocklist = 64 << 20
)

// BlocklistFresh reports the blocklist at url converted in dir less than
// BlocklistDays ago.
func BlocklistFresh(dir, url string, now time.Time) bool {
	src, err := os.ReadFile(filepath.Join(dir, blocklistSource))
	if err != nil || strings.TrimSpace(string(src)) != strings.TrimSpace(url) {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, blocklistFile))
	return err == nil && now.Sub(fi.ModTime()) < BlocklistDays*24*time.Hour
}

// StoreBlocklist converts a list fetched from url and keeps it in dir; one
// that isn't a list keeps the last good one (false).
func StoreBlocklist(dir, url string, raw []byte) (Blocklist, bool, error) {
	list, ok := ConvertBlocklist(raw)
	if !ok {
		return Blocklist{}, false, nil
	}
	if err := writeAtomic(filepath.Join(dir, blocklistFile), []byte(list.Text)); err != nil {
		return Blocklist{}, false, err
	}
	return list, true, writeAtomic(filepath.Join(dir, blocklistSource), []byte(strings.TrimSpace(url)))
}

// LoadBlocklist is the blocklist kept in dir for url (nil: none, or one
// from another address).
func LoadBlocklist(dir, url string) *Blocklist {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(dir, blocklistSource))
	if err != nil || strings.TrimSpace(string(src)) != strings.TrimSpace(url) {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, blocklistFile))
	if err != nil {
		return nil
	}
	list, ok := ConvertBlocklist(b)
	if !ok {
		return nil
	}
	return &list
}
