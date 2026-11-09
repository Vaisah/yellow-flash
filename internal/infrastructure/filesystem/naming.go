// Ported from Mister Download Manager, crates/engine/src/naming.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package filesystem

import (
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

// isFormatChar reports Unicode's format characters (general category Cf):
// bidirectional marks, embeddings, overrides and isolates, zero-width
// spaces and joiners, the byte order mark, the soft hyphen, invisible
// operators, tags.
func isFormatChar(r rune) bool {
	switch {
	case r == 0xAD, r >= 0x600 && r <= 0x605, r == 0x61C, r == 0x6DD, r == 0x70F,
		r >= 0x890 && r <= 0x891, r == 0x8E2, r == 0x180E,
		r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x206F,
		r == 0xFEFF, r >= 0xFFF9 && r <= 0xFFFB, r == 0x110BD, r == 0x110CD,
		r >= 0x13430 && r <= 0x1343F, r >= 0x1BCA0 && r <= 0x1BCA3,
		r >= 0x1D173 && r <= 0x1D17A, r == 0xE0001, r >= 0xE0020 && r <= 0xE007F:
		return true
	}
	return false
}

// isReservedDeviceName reports Windows' device names, with or without an
// extension: CON, PRN, AUX, NUL, CONIN$, CONOUT$, COM0-9 and LPT0-9 (and
// their superscript forms COM¹ … LPT³).
func isReservedDeviceName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(stem)
	if windowsReserved[stem] {
		return true
	}
	rs := []rune(stem)
	if len(rs) != 4 || (!strings.HasPrefix(stem, "COM") && !strings.HasPrefix(stem, "LPT")) {
		return false
	}
	d := rs[3]
	return d >= '0' && d <= '9' || d == '¹' || d == '²' || d == '³'
}

const (
	// maxPathWindows is the longest full path created on Windows, in UTF-16
	// units: MAX_PATH (260) without the terminating NUL, so Explorer and
	// other programs can still open the file.
	maxPathWindows = 259
	maxPathOther   = 4095
	// duplicateUnits is what a duplicate's number adds: " (NNN)".
	duplicateUnits = 6
	// minNameUnits is the shortest name worth making when a deep folder
	// leaves little room.
	minNameUnits = 20
)

func maxPathUnits() int {
	if runtime.GOOS == "windows" {
		return maxPathWindows
	}
	return maxPathOther
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// PathFits reports whether a download to dir/name, with its .part file and
// a duplicate's number, stays within the path limit.
func PathFits(dir, name string) bool {
	return utf16Len(filepath.Join(dir, name))+len(TempSuffix)+duplicateUnits <= maxPathUnits()
}

// FitName shortens name, keeping its extension, so that dir/name, its .part
// file and a duplicate's number stay within the path limit. A name from a
// long address (a token hundreds of characters long) then no longer fails
// the download. A folder too deep for any name keeps the name whole; the
// download then reports the path as too long.
func FitName(dir, name string) string {
	room := maxPathUnits() - utf16Len(dir) - 1 - len(TempSuffix) - duplicateUnits
	if room < minNameUnits || utf16Len(name) <= room {
		return name
	}
	ext := filepath.Ext(name)
	if ext == name || utf16Len(ext) > 16 {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	budget := room - utf16Len(ext)
	units, cut := 0, 0
	for i, r := range stem {
		n := 1
		if r >= 0x10000 {
			n = 2
		}
		if units+n > budget {
			break
		}
		units += n
		cut = i + 1
	}
	return strings.TrimRight(string(stem[:cut]), ". ") + ext
}
