// Ported from Mister Download Manager, crates/engine/src/sparse.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func isSparse(t *testing.T, path string) bool {
	t.Helper()
	p, _ := windows.UTF16PtrFromString(path)
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		t.Fatal(err)
	}
	return attrs&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0
}

func TestASparsePartFileIsWrittenFarInWithoutAFill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.bin")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !SetSparse(f, true) {
		t.Skip("the temp folder's file system cannot make sparse files")
	}
	if !isSparse(t, path) {
		t.Fatal("not marked sparse")
	}
	if err = f.Truncate(4 << 30); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(make([]byte, 1<<20), 2<<30); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	// Measured on the drive, not timed: a file that is not sparse takes its
	// whole 4 GiB; this one holds only what was written.
	p, _ := windows.UTF16PtrFromString(path)
	var high uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")
	low, _, callErr := proc.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xFFFFFFFF && callErr != windows.ERROR_SUCCESS {
		t.Fatal(callErr)
	}
	if used := uint64(high)<<32 | uint64(uint32(low)); used < 1<<20 || used >= 64<<20 {
		t.Fatalf("%d bytes on the drive", used)
	}
	// Clearing allocates any holes left, so only a fully written file is
	// cleared: shrink this mostly empty one first.
	if err = f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if !SetSparse(f, false) || isSparse(t, path) {
		t.Fatal("not cleared")
	}
}
