// Ported from Mister Download Manager, crates/torrent/src/names.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

// Whether a torrent's file names are safe to write. Every name is checked
// before a torrent is listed or added, so only plain names reach the disk.
// The files are written under the torrent's own names, so they can't be
// renamed: an unsafe name refuses the torrent, saying which.
//
// Everywhere: no empty part, ".", "..", separator or control character. On
// Windows also none of :<>"|?*, no trailing dot or space (Windows drops
// them, so two names become one), and no device name (CON, NUL.txt,
// COM1…). Where names ignore letter case (Windows, macOS), no two files
// whose paths differ only by case.

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// UnsafeName is the first unsafe name: its path (parts joined by /) and
// why.
type UnsafeName struct {
	Path, Why string
}

func (e *UnsafeName) Error() string {
	return fmt.Sprintf("the torrent has a file named %q: %s", e.Path, e.Why)
}

// CheckNames checks every file's path, given as its parts, for this system.
func CheckNames(files [][]string) error {
	return checkNamesFor(files, runtime.GOOS == "windows", runtime.GOOS == "windows" || runtime.GOOS == "darwin")
}

// checkNamesFor checks for Windows' rules (windows) and a file system that
// ignores letter case (caseBlind), so the tests can try every system's
// rules on any of them.
func checkNamesFor(files [][]string, windows, caseBlind bool) error {
	seen := map[string]bool{}
	for _, parts := range files {
		path := strings.Join(parts, "/")
		if len(parts) == 0 {
			return &UnsafeName{path, "a file without a name"}
		}
		for _, part := range parts {
			if why := unsafePart(part, windows); why != "" {
				return &UnsafeName{path, why}
			}
		}
		key := path
		if caseBlind {
			key = strings.ToLower(path)
		}
		if seen[key] {
			why := "two files with the same name"
			if caseBlind {
				why = "two files whose names differ only by letter case"
			}
			return &UnsafeName{path, why}
		}
		seen[key] = true
	}
	return nil
}

// unsafePart says why part (one folder or file name) can't be written, if
// it can't.
func unsafePart(part string, windows bool) string {
	if part == "" || part == "." || part == ".." {
		return "an empty, `.` or `..` name"
	}
	if !utf8.ValidString(part) || strings.ContainsFunc(part, func(c rune) bool {
		return c == '/' || c == '\\' || unicode.IsControl(c)
	}) {
		return "a separator or control character in a name"
	}
	if !windows {
		return ""
	}
	if strings.ContainsAny(part, `:<>"|?*`) {
		return "a character Windows doesn't allow in a name"
	}
	if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
		return "a name ending with a dot or a space"
	}
	// A device name, with or without an extension (NUL.txt, com1.tar.gz;
	// superscript digits count too).
	stem, _, _ := strings.Cut(part, ".")
	stem = strings.ToUpper(strings.TrimRightFunc(stem, unicode.IsSpace))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return "a Windows device name (CON, NUL, COM1…)"
	}
	r := []rune(stem)
	if len(r) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) &&
		(r[3] >= '0' && r[3] <= '9' || r[3] == '¹' || r[3] == '²' || r[3] == '³') {
		return "a Windows device name (CON, NUL, COM1…)"
	}
	return ""
}
