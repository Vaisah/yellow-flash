// Ported from Mister Download Manager, crates/tools/src/chosen.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// The yt-dlp, ffmpeg and gallery-dl the person chose instead of our own
// copies (one copy of each for every program that uses them).
// <tools>/programs.json holds them; YtDlp, Ffmpeg and GalleryDl read it
// first, and our own updates leave a chosen program alone.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Helper is one of the helper programs.
type Helper string

const (
	HelperYtDlp     Helper = "yt-dlp"
	HelperFfmpeg    Helper = "ffmpeg"
	HelperGalleryDl Helper = "gallery-dl"
)

const programsFile = "programs.json"

// GalleryDlOldest is the oldest gallery-dl release Yellow Flash can drive.
const GalleryDlOldest = "1.20.0"

func readPrograms(dir string) map[Helper]Installed {
	p := map[Helper]Installed{}
	raw, err := os.ReadFile(filepath.Join(dir, programsFile))
	if err != nil {
		return p
	}
	// A file edited by hand may start with a UTF-8 byte-order mark
	// (Notepad, PowerShell).
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if json.Unmarshal(raw, &p) != nil {
		return map[Helper]Installed{}
	}
	return p
}

func writePrograms(dir string, p map[Helper]Installed) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, programsFile), raw, 0o644)
}

// Chosen is the program the person chose for h, if any and still there.
func Chosen(dir string, h Helper) (Installed, bool) {
	i, ok := readPrograms(dir)[h]
	if !ok || !isFile(i.Exe) {
		return Installed{}, false
	}
	return i, true
}

// Choose uses exe for h from now on, once it has answered as that program
// (--version) in a release Yellow Flash can drive.
func Choose(ctx context.Context, dir string, h Helper, exe string) (Installed, error) {
	i, err := Ask(ctx, h, exe)
	if err != nil {
		return Installed{}, err
	}
	if h == HelperGalleryDl && galleryTooOld(i.Version) {
		return Installed{}, fmt.Errorf("%w: gallery-dl %s needs %s or newer", ErrTooOld, i.Version, GalleryDlOldest)
	}
	p := readPrograms(dir)
	p[h] = i
	return i, writePrograms(dir, p)
}

// Forget goes back to our own copy of h.
func Forget(dir string, h Helper) error {
	p := readPrograms(dir)
	if _, ok := p[h]; !ok {
		return nil
	}
	delete(p, h)
	return writePrograms(dir, p)
}

// Ask is exe as h, with the version it says (--version), not chosen: for a
// check.
func Ask(ctx context.Context, h Helper, exe string) (Installed, error) {
	arg := "--version"
	if h == HelperFfmpeg {
		arg = "-version"
	}
	out, err := output(ctx, exe, []string{arg}, 20*time.Second)
	if err != nil {
		return Installed{}, fmt.Errorf("%w: %s", ErrNotTheProgram, h)
	}
	v, ok := parseVersion(h, string(out))
	if !ok {
		return Installed{}, fmt.Errorf("%w: %s", ErrNotTheProgram, h)
	}
	return Installed{Version: v, Exe: exe}, nil
}

// parseVersion reads a version as each program prints it: yt-dlp and
// gallery-dl alone (2026.10.26, 1.32.19; gallery-dl 1.30.0 after an empty
// line); ffmpeg starts with "ffmpeg version 8.0 …".
func parseVersion(h Helper, out string) (string, bool) {
	first := ""
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			first = l
			break
		}
	}
	v := first
	if h == HelperFfmpeg {
		rest, ok := strings.CutPrefix(first, "ffmpeg version ")
		fields := strings.Fields(rest)
		if !ok || len(fields) == 0 {
			return "", false
		}
		v = fields[0]
	}
	if v == "" || len(v) > 64 || !isAlnum(v[0]) {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		if !isAlnum(v[i]) && !strings.ContainsRune("._-+~", rune(v[i])) {
			return "", false
		}
	}
	return v, true
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// galleryRelease reads major.minor[.patch] (a leading v allowed); false
// when it can't be read.
func galleryRelease(v string) ([3]int, bool) {
	var r [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		digits := parts[i]
		for j := 0; j < len(digits); j++ {
			if digits[j] < '0' || digits[j] > '9' {
				digits = digits[:j]
				break
			}
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			if i < 2 {
				return r, false
			}
			n = 0
		}
		r[i] = n
	}
	return r, len(parts) >= 2
}

// galleryTooOld reports a gallery-dl older than GalleryDlOldest (one whose
// version can't be read isn't).
func galleryTooOld(v string) bool {
	have, ok := galleryRelease(v)
	oldest, _ := galleryRelease(GalleryDlOldest)
	if !ok {
		return false
	}
	for i := range have {
		if have[i] != oldest[i] {
			return have[i] < oldest[i]
		}
	}
	return false
}
