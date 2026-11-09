// Ported from Mister Download Manager, crates/engine/src/disk.rs and crates/engine/src/sparse.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"os"
	"path/filepath"
)

// Volume describes the volume holding a path.
type Volume struct {
	// Free is the bytes a new file may take, as the system counts them for
	// this user: Windows GetDiskFreeSpaceEx (quotas included), Unix statfs's
	// blocks available to non-root programs.
	Free uint64
	// FileSystem is the system's name for it ("NTFS", "FAT32", "exFAT";
	// "ext4", "vfat"), or "" when it cannot tell.
	FileSystem string
}

// VolumeOf returns the volume path is on, or will be once created: a
// missing folder is looked up through the nearest existing folder above it.
// ok is false when no folder of the path exists (a removed drive) or the
// system cannot tell; the caller should then skip its check.
func VolumeOf(path string) (v Volume, ok bool) {
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Volume{}, false
	}
	for dir := abs; ; {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return volumeOf(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Volume{}, false
		}
		dir = parent
	}
}

// SparseMinSize is the smallest file worth making sparse: below it the
// zero-fill a sparse file avoids is short.
const SparseMinSize = 5 << 20

// SetSparse marks f sparse (call before extending it) or, once every byte
// is written, normal again. On NTFS, extending a file and then writing far
// into it makes Windows zero-fill everything before the write,
// synchronously: one 1 MiB write at 2 GiB took 4.3 s in MDM's measurements.
// A sparse file has no such fill. It reports false where the file system
// cannot (FAT, exFAT) and on systems where truncation already makes sparse
// files; the download then works as before.
func SetSparse(f *os.File, sparse bool) bool { return setSparse(f, sparse) }
