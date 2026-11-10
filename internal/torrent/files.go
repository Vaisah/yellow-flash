// Yellow Flash, a download manager. Copyright (C) 2026 the Yellow Flash contributors.
// SPDX-License-Identifier: GPL-3.0-or-later

package torrent

// Where a torrent's pieces are written: its files, in place, under their own
// names. anacrolix's file storage maps each file into memory and keeps it
// mapped for as long as the program runs, so on Windows a paused or removed
// torrent's files stay locked (they can't be moved or deleted). This one
// reads and writes through the torrent's open files, closed when the
// torrent stops. Padding files (BEP 47) are never written.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// fileStore writes torrents in dir; their checked pieces are kept in pc.
type fileStore struct {
	dir string
	pc  storage.PieceCompletion
}

// span is one file's place in the torrent.
type span struct {
	path    string // "" for padding
	offset  int64
	length  int64
	padding bool
}

type openTorrent struct {
	files []span
	pc    storage.PieceCompletion
	mu    sync.Mutex
	open  map[string]*os.File
}

func (s fileStore) OpenTorrent(_ context.Context, info *metainfo.Info, hash metainfo.Hash) (storage.TorrentImpl, error) {
	t := &openTorrent{pc: s.pc, open: map[string]*os.File{}}
	name := info.BestName()
	var names [][]string
	var at int64
	for _, f := range info.UpvertedFiles() {
		sp := span{offset: at, length: f.Length, padding: strings.Contains(f.Attr, "p")}
		at += f.Length
		if !sp.padding {
			parts := []string{name}
			if len(info.Files) > 0 {
				parts = append(parts, f.BestPath()...)
			}
			names = append(names, parts)
			sp.path = filepath.Join(append([]string{s.dir}, parts...)...)
		}
		t.files = append(t.files, sp)
	}
	// Checked before it was added; checked again, as these names come from
	// the info the peers sent.
	if err := CheckNames(names); err != nil {
		return storage.TorrentImpl{}, err
	}
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl {
			return piece{t, p.Offset(), p.Length(), metainfo.PieceKey{InfoHash: hash, Index: p.Index()}}
		},
		Close: t.close,
	}, nil
}

func (t *openTorrent) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var first error
	for p, f := range t.open {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
		delete(t.open, p)
	}
	return first
}

// file is path opened for reading and writing (created when write), kept
// open until the torrent stops.
func (t *openTorrent) file(path string, write bool) (*os.File, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.open[path]; ok {
		return f, nil
	}
	flag := os.O_RDWR
	if write {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		flag |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return nil, err
	}
	t.open[path] = f
	return f, nil
}

// each calls fn for every file holding part of [off, off+n) of the
// torrent: the file, where in it, and which bytes of the range [from, to).
func (t *openTorrent) each(off, n int64, fn func(sp span, at, from, to int64) error) error {
	end := off + n
	for _, sp := range t.files {
		lo, hi := max(off, sp.offset), min(end, sp.offset+sp.length)
		if lo >= hi {
			continue
		}
		if err := fn(sp, lo-sp.offset, lo-off, hi-off); err != nil {
			return err
		}
	}
	return nil
}

type piece struct {
	t      *openTorrent
	offset int64
	length int64
	key    metainfo.PieceKey
}

func (p piece) ReadAt(b []byte, off int64) (int, error) {
	if off >= p.length {
		return 0, io.EOF
	}
	b = b[:min(int64(len(b)), p.length-off)]
	err := p.t.each(p.offset+off, int64(len(b)), func(sp span, at, from, to int64) error {
		if sp.padding {
			clear(b[from:to])
			return nil
		}
		f, err := p.t.file(sp.path, false)
		if err != nil {
			return err
		}
		_, err = f.ReadAt(b[from:to], at)
		return err
	})
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, os.ErrNotExist) {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	return len(b), nil
}

func (p piece) WriteAt(b []byte, off int64) (int, error) {
	if off >= p.length {
		return 0, io.ErrShortWrite
	}
	b = b[:min(int64(len(b)), p.length-off)]
	err := p.t.each(p.offset+off, int64(len(b)), func(sp span, at, from, to int64) error {
		if sp.padding {
			return nil
		}
		f, err := p.t.file(sp.path, true)
		if err != nil {
			return err
		}
		_, err = f.WriteAt(b[from:to], at)
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p piece) MarkComplete() error    { return p.t.pc.Set(p.key, true) }
func (p piece) MarkNotComplete() error { return p.t.pc.Set(p.key, false) }

// Completion is not complete when a file the piece is in is missing or ends
// before the piece's part of it (not written yet, or deleted or truncated
// since: nothing to hash); otherwise the store's word (unknown: hashed).
func (p piece) Completion() storage.Completion {
	there := true
	_ = p.t.each(p.offset, p.length, func(sp span, at, from, to int64) error {
		if !sp.padding {
			if fi, err := os.Stat(sp.path); err != nil || fi.Size() < at+(to-from) {
				there = false
			}
		}
		return nil
	})
	if !there {
		return storage.Completion{Ok: true, Complete: false}
	}
	c, err := p.t.pc.Get(p.key)
	if err != nil {
		return storage.Completion{Err: err}
	}
	return c
}
