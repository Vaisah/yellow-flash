// Ported from Mister Download Manager, crates/engine/src/disk.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"fmt"
	"strings"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/platform"
)

const (
	// diskMargin is room kept free beyond the file (the OS, the database,
	// other programs).
	diskMargin = 128 << 20
	// fat32Max is FAT32's largest file: 4 GiB − 1.
	fat32Max = 4<<30 - 1
	// probeTimeout skips the check when the volume probe hangs (a stale
	// network drive can).
	probeTimeout = 5 * time.Second
)

// probeVolume runs platform.VolumeOf off the caller's goroutine and gives
// up after probeTimeout.
var probeVolume = func(path string) (platform.Volume, bool) {
	type answer struct {
		v  platform.Volume
		ok bool
	}
	ch := make(chan answer, 1)
	go func() {
		v, ok := platform.VolumeOf(path)
		ch <- answer{v, ok}
	}()
	t := time.NewTimer(probeTimeout)
	defer t.Stop()
	select {
	case a := <-ch:
		return a.v, a.ok
	case <-t.C:
		return platform.Volume{}, false
	}
}

// checkVolume reports why still more bytes, for a file whose final size is
// size, cannot go on v: FAT32 cannot hold 4 GiB files, and the volume must
// keep diskMargin free afterwards.
func checkVolume(v platform.Volume, size, still int64) error {
	switch strings.ToUpper(v.FileSystem) {
	case "FAT32", "FAT", "VFAT", "MSDOS": // Windows', Linux' and macOS's names
		if size > fat32Max {
			return fmt.Errorf("%w: %s on %s (largest file 4 GB)", domain.ErrTooBigForFileSystem, humanBytes(size), v.FileSystem)
		}
	}
	if still <= 0 {
		return nil
	}
	if uint64(still)+diskMargin > v.Free {
		free := uint64(0)
		if v.Free > diskMargin {
			free = v.Free - diskMargin
		}
		return fmt.Errorf("%w: needs %s, %s free", domain.ErrNoSpace, humanBytes(still), humanBytes(int64(free)))
	}
	return nil
}

// checkSpace checks the destination before a transfer writes still more
// bytes of a size-byte file. An unknown size, or a volume the system cannot
// describe, skips the check.
func checkSpace(dir string, size, still int64) error {
	if size < 0 {
		return nil
	}
	v, ok := probeVolume(dir)
	if !ok {
		return nil
	}
	return checkVolume(v, size, still)
}

// humanBytes formats a size as IDM does: "512 bytes", "1.50 KB",
// "150 MB", "4.00 GB" (binary units).
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v, unit := float64(n)/1024, 0
	for v >= 1024 && unit < len(units)-1 {
		v /= 1024
		unit++
	}
	switch {
	case v < 10:
		return fmt.Sprintf("%.2f %s", v, units[unit])
	case v < 100:
		return fmt.Sprintf("%.1f %s", v, units[unit])
	}
	return fmt.Sprintf("%.0f %s", v, units[unit])
}
