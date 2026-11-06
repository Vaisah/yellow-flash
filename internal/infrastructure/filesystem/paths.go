// Package filesystem contains cross-platform filename handling, temporary
// file management and no-overwrite publication of completed downloads.
package filesystem

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ErrInvalidFilename is returned when no safe filename can be derived.
var ErrInvalidFilename = errors.New("invalid filename")

// TempSuffix is appended to the final filename while downloading.
const TempSuffix = ".part"

// maxNameBytes leaves room for " (999)" and ".part" under the common
// 255-byte component limit of NTFS, ext4 and friends.
const maxNameBytes = 200

var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

// SanitizeFilename turns untrusted input (user text, URL paths, or
// Content-Disposition values) into a single safe path component that is
// valid on both Windows and Linux. Directory parts are discarded, so the
// result can never traverse outside the destination directory.
func SanitizeFilename(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name = path.Base(name); name == "/" {
		return "", ErrInvalidFilename
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	if name == "" || name == "." || name == ".." {
		return "", ErrInvalidFilename
	}
	if base := strings.ToUpper(strings.SplitN(name, ".", 2)[0]); windowsReserved[base] ||
		(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		name = "_" + name
	}
	name = strings.TrimRight(truncateKeepingExtension(name, maxNameBytes), ". ")
	if name == "" {
		return "", ErrInvalidFilename
	}
	return name, nil
}

func truncateKeepingExtension(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	ext := filepath.Ext(name)
	if len(ext) > 16 || len(ext) >= limit {
		ext = ""
	}
	stem := name[:len(name)-len(ext)]
	budget := limit - len(ext)
	for len(stem) > budget || !utf8.ValidString(stem) {
		_, size := utf8.DecodeLastRuneInString(stem)
		if size == 0 {
			break
		}
		stem = stem[:len(stem)-size]
	}
	return stem + ext
}

// FilenameFromURL derives a filename from the last URL path element.
func FilenameFromURL(u *url.URL) string {
	if u == nil {
		return "download"
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		return "download"
	}
	return name
}

// TempPath returns the predictable temporary path, e.g. movie.mp4.part.
func TempPath(dir, filename string) string { return filepath.Join(dir, filename+TempSuffix) }

// Candidate returns the n-th conflict-free name: n=0 keeps the name,
// n=1 gives "name (1).ext", and so on.
func Candidate(name string, n int) string {
	if n == 0 {
		return name
	}
	ext := filepath.Ext(name)
	if ext == name {
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}
