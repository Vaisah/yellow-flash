// Ported from Mister Download Manager, crates/app/src/store.rs and crates/engine/src/state.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package jsonstore keeps the download list in a JSON file, as Mister
// Download Manager does, instead of a database.
//
//   - downloads.json holds the downloads, settings, queues and learned host
//     limits. It is written atomically (temp file, fsync, rename) by a
//     background saver that coalesces changes: the newest state waiting
//     replaces an older one not yet written.
//   - Generations: downloads.json.bak is the list before the last save;
//     .1.bak and .2.bak are older, rotated at most every 10 minutes and only
//     when the list changed. Loading takes the newest file that parses. A
//     damaged list is kept as .corrupt-<unix time>.bak, and a list that
//     exists but cannot be read is never overwritten.
//   - resume/<id>.json holds each download's byte ranges, written
//     synchronously and fsynced: it is the durable record of what is on
//     disk (engine rule 2). The list's byte counts may lag behind it, which
//     is safe because recovery clamps progress to the .part file.
//
// Credential headers are never written.
package jsonstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
)

// formatVersion is the list format written now.
const formatVersion = 1

// generationEvery is how often an older generation is kept.
const generationEvery = 10 * time.Minute

// ErrClosed is returned after Close.
var ErrClosed = errors.New("jsonstore: store is closed")

// ErrBlocked is returned when the list existed but could not be read at
// start: it is never saved over.
var ErrBlocked = errors.New("jsonstore: the download list could not be read at start; not overwriting it")

// listFile is downloads.json.
type listFile struct {
	Version    int                `json:"version"`
	Downloads  []record           `json:"downloads"`
	Settings   map[string]string  `json:"settings,omitempty"`
	Queues     []queue.Queue      `json:"queues,omitempty"`
	HostLimits []domain.HostLimit `json:"hostLimits,omitempty"`
}

// record is a download as written: the domain JSON plus its non-secret
// headers (domain.Download keeps headers out of its own JSON).
type record struct {
	domain.Download
	Headers map[string]string `json:"headers,omitempty"`
}

// Store implements domain.Store and domain.HostLimitStore over JSON files.
type Store struct {
	path      string
	resumeDir string
	blocked   bool
	now       func() time.Time

	mu         sync.Mutex
	downloads  map[string]*domain.Download
	segments   map[string]map[int]domain.Segment // loaded lazily from resume/
	settings   map[string]string
	queues     []queue.Queue
	hostLimits map[string]domain.HostLimit
	closed     bool
	version    uint64        // bumped by every change
	writtenV   uint64        // the version last written
	written    chan struct{} // closed and replaced after each write
	lastErr    error         // the last failed write, until one succeeds

	wake chan struct{}
	done chan struct{}
}

var (
	_ domain.Store          = (*Store)(nil)
	_ domain.HostLimitStore = (*Store)(nil)
)

// Open loads (or starts) the list at path, typically
// <data folder>/downloads.json. Resume files go in resume/ beside it.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{
		path: path, resumeDir: filepath.Join(dir, "resume"), now: time.Now,
		downloads: map[string]*domain.Download{}, segments: map[string]map[int]domain.Segment{},
		settings: map[string]string{}, hostLimits: map[string]domain.HostLimit{},
		wake: make(chan struct{}, 1), written: make(chan struct{}), done: make(chan struct{}),
	}
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, r := range f.Downloads {
		d := r.Download
		if !d.Status.Valid() {
			return nil, fmt.Errorf("download %s has unknown status %q", d.ID, d.Status)
		}
		d.Headers = nil
		for k, v := range r.Headers {
			if !domain.IsSensitiveHeader(k) {
				if d.Headers == nil {
					d.Headers = map[string]string{}
				}
				d.Headers[k] = v
			}
		}
		s.downloads[d.ID] = d.Clone()
	}
	for k, v := range f.Settings {
		s.settings[k] = v
	}
	s.queues = append(s.queues, f.Queues...)
	for _, l := range f.HostLimits {
		s.hostLimits[l.Host] = l
	}
	go s.saver()
	return s, nil
}

/* ---------- loading ---------- */

// load reads the list, falling back through its generations.
func (s *Store) load() (listFile, error) {
	raw, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if f, ok := parse(raw); ok {
			return f, nil
		}
		// Damaged: keep it aside (never over an earlier one) and use a generation.
		aside := fmt.Sprintf("%s.corrupt-%d.bak", s.path, s.now().Unix())
		if err := os.Rename(s.path, aside); err != nil {
			return listFile{}, fmt.Errorf("set damaged list aside: %w", err)
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		// There but unreadable: work from a generation, never save over it.
		s.blocked = true
	}
	for _, g := range s.generations() {
		if raw, err := os.ReadFile(g); err == nil {
			if f, ok := parse(raw); ok {
				return f, nil
			}
		}
	}
	return listFile{Version: formatVersion}, nil
}

// parse reads a list, tolerating a UTF-8 byte-order mark (editors and
// PowerShell add one).
func parse(raw []byte) (listFile, bool) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var f listFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return listFile{}, false
	}
	return f, true
}

func (s *Store) backupPath() string          { return s.path + ".bak" }
func (s *Store) generationPath(n int) string { return fmt.Sprintf("%s.%d.bak", s.path, n) }
func (s *Store) generations() []string {
	return []string{s.backupPath(), s.generationPath(1), s.generationPath(2)}
}

/* ---------- saving ---------- */

// changedLocked marks the list dirty and wakes the saver. Call with mu held.
func (s *Store) changedLocked() {
	s.version++
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// saver writes the newest list whenever it changes, off the callers'
// goroutines: the write ends with an fsync, which waits for whatever a big
// download wrote meanwhile.
func (s *Store) saver() {
	defer close(s.done)
	for range s.wake {
		s.mu.Lock()
		closed, v, already := s.closed, s.version, s.writtenV
		data, err := s.encodeLocked()
		s.mu.Unlock()
		if v != already {
			if err == nil {
				err = s.write(data)
			}
			s.mu.Lock()
			s.lastErr, s.writtenV = err, v
			close(s.written)
			s.written = make(chan struct{})
			s.mu.Unlock()
		}
		if closed {
			return
		}
	}
}

func (s *Store) encodeLocked() ([]byte, error) {
	if s.blocked {
		return nil, ErrBlocked
	}
	f := listFile{Version: formatVersion, Settings: s.settings, Queues: s.queues, Downloads: []record{}}
	for _, d := range s.downloads {
		r := record{Download: *d}
		for k, v := range d.Headers {
			if !domain.IsSensitiveHeader(k) {
				if r.Headers == nil {
					r.Headers = map[string]string{}
				}
				r.Headers[k] = v
			}
		}
		f.Downloads = append(f.Downloads, r)
	}
	sort.Slice(f.Downloads, func(i, j int) bool {
		a, b := f.Downloads[i], f.Downloads[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	for _, l := range s.hostLimits {
		f.HostLimits = append(f.HostLimits, l)
	}
	sort.Slice(f.HostLimits, func(i, j int) bool { return f.HostLimits[i].Host < f.HostLimits[j].Host })
	return json.MarshalIndent(f, "", "  ")
}

// write keeps the generations, then writes the list atomically.
func (s *Store) write(data []byte) error {
	s.keepGenerations()
	return atomicWrite(s.path, data)
}

// keepGenerations runs before a save: the current list becomes .bak, and
// every generationEvery (when it changed) also .1.bak, the old .1.bak
// moving to .2.bak. Only a list that parses is kept: a damaged file never
// replaces a good generation.
func (s *Store) keepGenerations() {
	current, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	if _, ok := parse(current); !ok {
		return
	}
	_ = os.WriteFile(s.backupPath(), current, 0o600)
	g1, g2 := s.generationPath(1), s.generationPath(2)
	due := true
	if info, err := os.Stat(g1); err == nil {
		old, _ := os.ReadFile(g1)
		due = s.now().Sub(info.ModTime()) >= generationEvery && !bytes.Equal(old, current)
	}
	if due {
		if _, err := os.Stat(g1); err == nil {
			_ = os.Rename(g1, g2)
		}
		_ = os.WriteFile(g1, current, 0o600)
	}
}

// atomicWrite writes data to path through a temp file in the same folder,
// fsynced before the rename, readable by its owner only.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op once renamed
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Flush waits until every change made so far is written, and returns the
// write's error.
func (s *Store) Flush(ctx context.Context) error {
	for {
		s.mu.Lock()
		want, got, ch, err, closed := s.version, s.writtenV, s.written, s.lastErr, s.closed
		if got < want && !closed {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
		if got >= want || closed {
			return err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close writes what is pending and stops the saver. Later calls fail with
// ErrClosed.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.changedLocked() // a last pass, written even when nothing else changed
	s.mu.Unlock()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// mutate applies fn under the lock and schedules a write. It returns the
// last write's error, so a failing disk shows up at the callers.
func (s *Store) mutate(fn func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	fn()
	s.changedLocked()
	return s.lastErr
}

/* ---------- downloads ---------- */

// Save upserts a copy of d.
func (s *Store) Save(_ context.Context, d *domain.Download) error {
	c := d.Clone()
	return s.mutate(func() { s.downloads[c.ID] = c })
}

// Get returns a copy of the download or an error wrapping ErrNotFound.
func (s *Store) Get(_ context.Context, id string) (*domain.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	d, ok := s.downloads[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, id)
	}
	return d.Clone(), nil
}

// List returns all downloads, newest first.
func (s *Store) List(_ context.Context) ([]domain.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	out := make([]domain.Download, 0, len(s.downloads))
	for _, d := range s.downloads {
		out = append(out, *d.Clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Delete removes a download and its resume file.
func (s *Store) Delete(_ context.Context, id string) error {
	err := s.mutate(func() {
		delete(s.downloads, id)
		delete(s.segments, id)
	})
	if errors.Is(err, ErrClosed) {
		return err
	}
	if rerr := os.Remove(s.resumePath(id)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return rerr
	}
	return err
}

/* ---------- segments (resume files) ---------- */

func (s *Store) resumePath(id string) string {
	return filepath.Join(s.resumeDir, filepath.Base(id)+".json")
}

// segmentsLocked returns the cached segments of id, reading its resume file
// the first time.
func (s *Store) segmentsLocked(id string) (map[int]domain.Segment, error) {
	if m, ok := s.segments[id]; ok {
		return m, nil
	}
	m := map[int]domain.Segment{}
	raw, err := os.ReadFile(s.resumePath(id))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var list []domain.Segment
		if err = json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &list); err != nil {
			return nil, fmt.Errorf("resume file of %s: %w", id, err)
		}
		for _, seg := range list {
			m[seg.Index] = seg
		}
	}
	s.segments[id] = m
	return m, nil
}

// SaveSegments upserts segments by index and writes the download's resume
// file before returning: a checkpoint is either fully recorded or not.
func (s *Store) SaveSegments(_ context.Context, downloadID string, segments []domain.Segment) error {
	if len(segments) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.downloads[downloadID]; !ok {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, downloadID)
	}
	cur, err := s.segmentsLocked(downloadID)
	if err != nil {
		return err
	}
	next := make(map[int]domain.Segment, len(cur)+len(segments))
	for k, v := range cur {
		next[k] = v
	}
	for _, seg := range segments {
		if seg.DownloadID != downloadID {
			return fmt.Errorf("segment %s belongs to %s, not %s", seg.ID, seg.DownloadID, downloadID)
		}
		next[seg.Index] = seg
	}
	data, err := json.Marshal(sortedSegments(next))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.resumeDir, 0o700); err != nil {
		return err
	}
	if err = atomicWrite(s.resumePath(downloadID), data); err != nil {
		return err
	}
	s.segments[downloadID] = next
	return nil
}

// ListSegments returns segments ordered by index.
func (s *Store) ListSegments(_ context.Context, downloadID string) ([]domain.Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	m, err := s.segmentsLocked(downloadID)
	if err != nil {
		return nil, err
	}
	return sortedSegments(m), nil
}

// DeleteSegments removes a download's resume file.
func (s *Store) DeleteSegments(_ context.Context, downloadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.segments[downloadID] = map[int]domain.Segment{}
	if err := os.Remove(s.resumePath(downloadID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func sortedSegments(m map[int]domain.Segment) []domain.Segment {
	out := make([]domain.Segment, 0, len(m))
	for _, seg := range m {
		out = append(out, seg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

/* ---------- host limits ---------- */

// LoadHostLimits returns every saved limit, sorted by host.
func (s *Store) LoadHostLimits(context.Context) ([]domain.HostLimit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.HostLimit, 0, len(s.hostLimits))
	for _, l := range s.hostLimits {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// SaveHostLimit upserts one limit.
func (s *Store) SaveHostLimit(_ context.Context, l domain.HostLimit) error {
	return s.mutate(func() { s.hostLimits[l.Host] = l })
}

/* ---------- settings and queues ---------- */

// Settings returns the key/value settings.
func (s *Store) Settings() *Settings { return &Settings{s: s} }

// Queues returns the queue list.
func (s *Store) Queues() *Queues { return &Queues{s: s} }

// Settings stores preferences as key/value pairs in the list file.
type Settings struct{ s *Store }

// Set upserts a setting.
func (r *Settings) Set(_ context.Context, key, value string) error {
	return r.s.mutate(func() { r.s.settings[key] = value })
}

// Get returns a setting and whether it exists.
func (r *Settings) Get(_ context.Context, key string) (string, bool, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	v, ok := r.s.settings[key]
	return v, ok, nil
}

// All returns a copy of every setting.
func (r *Settings) All(context.Context) (map[string]string, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := make(map[string]string, len(r.s.settings))
	for k, v := range r.s.settings {
		out[k] = v
	}
	return out, nil
}

// Queues persists download queues in the list file.
type Queues struct{ s *Store }

// Create adds a queue.
func (r *Queues) Create(_ context.Context, q *queue.Queue) error {
	return r.s.mutate(func() { r.s.queues = append(r.s.queues, *q) })
}

// List returns queues in creation order.
func (r *Queues) List(context.Context) ([]queue.Queue, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	out := append([]queue.Queue{}, r.s.queues...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Delete removes a queue; its downloads stay, with no queue.
func (r *Queues) Delete(_ context.Context, id string) error {
	return r.s.mutate(func() {
		kept := r.s.queues[:0]
		for _, q := range r.s.queues {
			if q.ID != id {
				kept = append(kept, q)
			}
		}
		r.s.queues = kept
		for _, d := range r.s.downloads {
			if d.QueueID == id {
				d.QueueID = ""
			}
		}
	})
}
