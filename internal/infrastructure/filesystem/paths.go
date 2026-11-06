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

// knownExtensions maps common download media types to their usual
// extension. Generic types such as application/octet-stream are absent on
// purpose: guessing would be worse than no extension.
var knownExtensions = map[string]string{
	"video/mp4": ".mp4", "video/webm": ".webm", "video/x-matroska": ".mkv", "video/quicktime": ".mov",
	"video/x-msvideo": ".avi", "video/mp2t": ".ts", "video/x-flv": ".flv", "video/3gpp": ".3gp",
	"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/aac": ".aac", "audio/ogg": ".ogg", "audio/opus": ".opus",
	"audio/flac": ".flac", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/webm": ".weba",
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp", "image/svg+xml": ".svg",
	"application/pdf": ".pdf", "application/zip": ".zip", "application/x-7z-compressed": ".7z",
	"application/vnd.rar": ".rar", "application/x-rar-compressed": ".rar", "application/gzip": ".gz",
	"application/x-gzip": ".gz", "application/x-tar": ".tar", "application/x-bzip2": ".bz2", "application/x-xz": ".xz",
	"application/x-msdownload": ".exe", "application/x-msi": ".msi", "application/vnd.debian.binary-package": ".deb",
	"application/x-iso9660-image": ".iso", "application/vnd.android.package-archive": ".apk",
	"application/json": ".json", "application/xml": ".xml", "text/plain": ".txt", "text/csv": ".csv",
	"text/html": ".html", "application/epub+zip": ".epub", "application/msword": ".doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
}

// ExtensionForMIME returns the usual extension for a Content-Type value
// (parameters ignored), or "" when unknown or generic.
func ExtensionForMIME(contentType string) string {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return knownExtensions[strings.ToLower(strings.TrimSpace(mediaType))]
}

// WithExtension appends ext when name has no extension of its own.
func WithExtension(name, ext string) string {
	if ext == "" || filepath.Ext(name) != "" {
		return name
	}
	return name + ext
}

// IsHTMLName reports whether name looks like a deliberately requested page.
func IsHTMLName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".html" || ext == ".htm" || ext == ".xhtml"
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
