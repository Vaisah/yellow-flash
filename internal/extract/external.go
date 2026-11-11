// Ported from Mister Download Manager, crates/extract/src/external.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package extract

// 7z, RAR and the zips Go can't open through 7-Zip, run as its own
// program: UnRAR's licence forbids using its code in a GPL program, so
// Yellow Flash never links it. 7-Zip writes into the staging folder; what
// it wrote is then checked as our own entries are (checkTree) before
// anything is moved into place.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// FindSevenZip is 7-Zip's program: in its usual install folders on Windows,
// else 7zz, 7z or 7za on the PATH (Linux and macOS packages name it so); ""
// when there is none.
func FindSevenZip() string {
	if runtime.GOOS == "windows" {
		for _, v := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if dir := os.Getenv(v); dir != "" {
				p := filepath.Join(dir, "7-Zip", "7z.exe")
				if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
					return p
				}
			}
		}
	}
	names := []string{"7zz", "7z", "7za"}
	if runtime.GOOS == "windows" {
		names = []string{"7z.exe", "7za.exe"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func extractExternal(tool, archive string, w *writer, password string) error {
	// An empty password when none is given: 7-Zip then fails at once on an
	// encrypted archive instead of asking on a console nobody sees.
	cmd := exec.Command(tool, "x", "-y", "-bso0", "-bsp0", "-p"+password, "-o"+w.root, "--", archive)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	hideWindow(cmd)
	// The password is never logged: the command isn't either.
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		said := strings.ToLower(stderr.String())
		switch {
		case strings.Contains(said, "wrong password") || strings.Contains(said, "encrypted"):
			if password != "" {
				return ErrWrongPassword
			}
			return ErrNeedsPassword
		case strings.Contains(said, "missing volume") || strings.Contains(said, "unexpected end of archive"):
			return &MissingVolumeError{}
		}
		for _, l := range strings.Split(said, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				return damaged(l)
			}
		}
		return damaged("7-Zip failed")
	}
	left, err := checkTree(w.root, w.limits.Depth)
	if err != nil {
		return err
	}
	for _, s := range left {
		w.skip(s.Name, s.Why)
	}
	return nil
}
