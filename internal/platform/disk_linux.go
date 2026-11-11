// Ported from Mister Download Manager, crates/engine/src/disk.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package platform

import (
	"os"

	"golang.org/x/sys/unix"
)

func volumeOf(dir string) (Volume, bool) {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return Volume{}, false
	}
	block := uint64(st.Frsize)
	if block == 0 {
		block = uint64(st.Bsize)
	}
	// The blocks a program may use, without the reserve kept for root.
	return Volume{Free: st.Bavail * block, Total: st.Blocks * block, FileSystem: linuxFileSystems[int64(st.Type)]}, true
}

// linuxFileSystems names statfs magic numbers: the FAT check needs vfat's,
// the others are for messages.
var linuxFileSystems = map[int64]string{
	0x4d44: "vfat", 0x2011bab0: "exfat", 0xef53: "ext4", 0x9123683e: "btrfs", 0x58465342: "xfs",
	0x2fc12fc1: "zfs", 0xf2f52010: "f2fs", 0x01021994: "tmpfs", 0x794c7630: "overlay", 0x6969: "nfs",
	0xff534d42: "cifs", 0xfe534d42: "smb2", 0x5346544e: "ntfs", 0x7366746e: "ntfs3", 0x65735546: "fuse",
}

// Truncation already makes sparse files on Linux file systems.
func setSparse(*os.File, bool) bool { return false }
