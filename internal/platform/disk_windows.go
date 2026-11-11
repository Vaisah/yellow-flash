// Ported from Mister Download Manager, crates/engine/src/disk.rs and crates/engine/src/sparse.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package platform

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func volumeOf(dir string) (Volume, bool) {
	// A share's root (\\server\share\) needs its trailing backslash.
	root := dir
	if !strings.HasSuffix(root, `\`) && !strings.HasSuffix(root, "/") {
		root += `\`
	}
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Volume{}, false
	}
	var free, total uint64
	if windows.GetDiskFreeSpaceEx(p, &free, &total, nil) != nil {
		return Volume{}, false
	}
	return Volume{Free: free, Total: total, FileSystem: fileSystem(dir)}, true
}

// fileSystem names the file system the folder really is on (through
// junctions and mounted folders), from a handle that asks for no access.
func fileSystem(dir string) string {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return ""
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var name [windows.MAX_PATH + 1]uint16
	if windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &name[0], uint32(len(name))) != nil {
		return ""
	}
	return windows.UTF16ToString(name[:])
}

// fsctlSetSparse is FSCTL_SET_SPARSE from winioctl.h.
const fsctlSetSparse = 0x000900c4

func setSparse(f *os.File, sparse bool) bool {
	// FILE_SET_SPARSE_BUFFER is a single BOOLEAN.
	var in byte
	if sparse {
		in = 1
	}
	var returned uint32
	err := windows.DeviceIoControl(windows.Handle(f.Fd()), fsctlSetSparse,
		(*byte)(unsafe.Pointer(&in)), 1, nil, 0, &returned, nil)
	return err == nil
}
