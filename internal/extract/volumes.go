// Ported from Mister Download Manager, crates/extract/src/volumes.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package extract

// Archives in several files: which file is which volume of what, and
// reading a byte-split archive (x.7z.001, .002…) as one file.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// VolumeKind is how an archive is split.
type VolumeKind int

// The kinds.
const (
	// RarPart is x.part1.rar, x.part2.rar… (RAR 3 and later).
	RarPart VolumeKind = iota + 1
	// RarOld is x.rar, x.r00, x.r01… (older RAR): x.rar is volume 1.
	RarOld
	// Split is x.7z.001, x.zip.001…: the archive cut in pieces, byte for byte.
	Split
)

// Volume is a file's place in a multi-volume archive.
type Volume struct {
	// Base is the name without its volume suffix and archive extension
	// (movie for movie.part2.rar): volumes of one archive share it.
	Base  string
	Kind  VolumeKind
	Index int // 1-based
}

// First reports the volume extraction starts from.
func (v Volume) First() bool { return v.Index == 1 }

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

// VolumeKey is the volume name is, if it's one (case doesn't matter). A
// lone x.rar is volume 1 of an old-style set, which may have no other
// volume.
func VolumeKey(name string) (Volume, bool) {
	lower := strings.ToLower(name)
	// x.partN.rar, and x.rar
	if stem, ok := strings.CutSuffix(lower, ".rar"); ok {
		if i := strings.LastIndex(stem, ".part"); i >= 0 && digits(stem[i+5:]) {
			if n, err := strconv.Atoi(stem[i+5:]); err == nil {
				return Volume{Base: name[:i], Kind: RarPart, Index: n}, true
			}
		}
		return Volume{Base: name[:len(stem)], Kind: RarOld, Index: 1}, true
	}
	// x.r00 → 2
	if i := strings.LastIndex(lower, ".r"); i >= 0 && len(lower)-i == 4 && digits(lower[i+2:]) {
		n, _ := strconv.Atoi(lower[i+2:])
		return Volume{Base: name[:i], Kind: RarOld, Index: n + 2}, true
	}
	// x.7z.001, x.zip.001
	for _, ext := range []string{".7z.", ".zip."} {
		if i := strings.LastIndex(lower, ext); i >= 0 {
			if num := lower[i+len(ext):]; len(num) == 3 && digits(num) {
				n, _ := strconv.Atoi(num)
				return Volume{Base: name[:i], Kind: Split, Index: n}, true
			}
		}
	}
	return Volume{}, false
}

// SplitParts is x.7z.001's parts on disk: .001, .002… while they exist, and
// any later one found after a gap (so a missing middle volume is reported,
// not taken for the end).
func SplitParts(first string) []string {
	name := filepath.Base(first)
	if len(name) < 3 {
		return []string{first}
	}
	stem, dir := name[:len(name)-3], filepath.Dir(first)
	path := func(n int) string { return filepath.Join(dir, fmt.Sprintf("%s%03d", stem, n)) }
	last := 1
	for n := 2; n <= 999; n++ {
		if _, err := os.Stat(path(n)); err == nil {
			last = n
		} else if n > last+10 {
			break
		}
	}
	out := make([]string, 0, last)
	for n := 1; n <= last; n++ {
		out = append(out, path(n))
	}
	return out
}

// MultiFile is a byte-split archive's parts read as one file (an
// io.ReaderAt for archive/zip).
type MultiFile struct {
	parts []*os.File
	sizes []int64
	total int64
}

// OpenSplit opens first's parts; a missing one is a *MissingVolumeError.
func OpenSplit(first string) (*MultiFile, error) {
	m := &MultiFile{}
	for n, p := range SplitParts(first) {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			m.Close()
			return nil, &MissingVolumeError{N: n + 1}
		}
		if err != nil {
			m.Close()
			return nil, err
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			m.Close()
			return nil, err
		}
		m.parts, m.sizes = append(m.parts, f), append(m.sizes, fi.Size())
		m.total += fi.Size()
	}
	return m, nil
}

// Size is the parts' bytes in all.
func (m *MultiFile) Size() int64 { return m.total }

// ReadAt reads across the parts.
func (m *MultiFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	read := 0
	start := int64(0)
	for i, f := range m.parts {
		end := start + m.sizes[i]
		for read < len(p) && off >= start && off < end {
			want := min(int64(len(p)-read), end-off)
			n, err := f.ReadAt(p[read:read+int(want)], off-start)
			read += n
			off += int64(n)
			if err != nil && err != io.EOF {
				return read, err
			}
			if n == 0 {
				break
			}
		}
		start = end
	}
	if read < len(p) {
		return read, io.EOF
	}
	return read, nil
}

// Close closes every part.
func (m *MultiFile) Close() {
	for _, f := range m.parts {
		f.Close()
	}
}
