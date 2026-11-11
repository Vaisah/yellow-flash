// Ported from Mister Download Manager, crates/extract/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package extract is safe archive extraction: zip (stored and Deflate),
// tar and its gz/xz forms and a single .gz/.xz file in process; 7z (split
// .7z.001 too), RAR, and zips Go's reader can't open (encrypted, Deflate64)
// through an installed 7-Zip, run as its own program (UnRAR's licence
// forbids linking it into a GPL program).
//
// Every archive is extracted into an empty staging folder beside the
// destination, each entry's path checked (safe.go), then moved into place:
// a folder named after the archive when it holds several things at its top
// (or always / never, as asked), a file that exists already kept and the
// new one numbered. One path for every format and for the external tool,
// and a failure leaves nothing behind but the archive.
package extract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Format is what an archive is, by its first bytes (and, for a compressed
// tar, its name).
type Format int

// The formats.
const (
	Unknown Format = iota
	Zip
	SevenZip
	Rar
	Tar
	TarGz
	TarXz
	// Gz is one file compressed with gzip (not a tar).
	Gz
	// Xz is one file compressed with xz (not a tar).
	Xz
)

// Detect is the format of the archive at path (its first volume for a
// split one); Unknown when it isn't one Yellow Flash extracts.
func Detect(path string) (Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return Unknown, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Unknown, err
	}
	return fromHead(head[:n], strings.ToLower(filepath.Base(path))), nil
}

func fromHead(head []byte, name string) Format {
	tarNamed := func(exts ...string) bool {
		for _, e := range exts {
			if strings.HasSuffix(name, e) {
				return true
			}
		}
		return false
	}
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return Zip
	case bytes.HasPrefix(head, []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}):
		return SevenZip
	case bytes.HasPrefix(head, []byte("Rar!\x1A\x07")):
		return Rar
	case bytes.HasPrefix(head, []byte{0x1F, 0x8B}):
		if tarNamed(".tar.gz", ".tgz") {
			return TarGz
		}
		return Gz
	case bytes.HasPrefix(head, []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}):
		if tarNamed(".tar.xz", ".txz") {
			return TarXz
		}
		return Xz
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return Tar
	}
	return Unknown
}

// Subfolder is where the archive's contents go.
type Subfolder string

// The choices.
const (
	// Smart: a folder named after the archive when it holds more than one
	// thing at its top (KDE Ark, 7-Zip's "smart"); one folder or one file
	// goes straight into the destination.
	Smart  Subfolder = "smart"
	Always Subfolder = "always"
	Never  Subfolder = "never"
)

// Policy is how to extract one archive.
type Policy struct {
	// Dest is the folder the contents go into (usually the archive's own).
	Dest      string
	Subfolder Subfolder
	// Password is the archive's ("": none given).
	Password string
	Limits   Limits
	// Tool is 7-Zip's program, for 7z, RAR and the zips Go can't open
	// (FindSevenZip finds it); "": not installed, and those archives fail
	// with ErrNoTool.
	Tool string
	// OnFile is told every file put in place (the app marks it as
	// downloaded from the internet, as the archive is). May be nil.
	OnFile func(path string)
	// Cancel, set from elsewhere, stops it (the download was deleted, the
	// app is closing). May be nil.
	Cancel *atomic.Bool
}

// Skipped is an entry left out, and why ("link", "goes outside the
// folder"…).
type Skipped struct {
	Name string `json:"name"`
	Why  string `json:"why"`
}

// Report is what an extraction did.
type Report struct {
	// Folder is where the contents went (the destination, or the folder
	// made for them).
	Folder  string    `json:"folder"`
	Files   int64     `json:"files"`
	Bytes   int64     `json:"bytes"`
	Skipped []Skipped `json:"skipped,omitempty"`
	// Renamed counts the files put in place under another name, one of that
	// name being there already.
	Renamed int64 `json:"renamed"`
}

// Why an archive wasn't extracted.
var (
	// ErrNeedsPassword: it's encrypted and no password was given.
	ErrNeedsPassword = errors.New("needs a password")
	ErrWrongPassword = errors.New("wrong password")
	// ErrTooMany: more entries or deeper folders than allowed.
	ErrTooMany = errors.New("too many entries or folders too deep")
	// ErrNoTool: 7z, RAR or an encrypted zip, and no 7-Zip was found.
	ErrNoTool = errors.New("7z and RAR archives (and encrypted zips) need 7-Zip, and none was found")
	// ErrUnsupported: not an archive Yellow Flash extracts.
	ErrUnsupported = errors.New("not an archive Yellow Flash extracts")
	ErrCancelled   = errors.New("cancelled")
)

// MissingVolumeError is a split archive's missing volume (1-based; 0 when
// the tool didn't say which).
type MissingVolumeError struct{ N int }

func (e *MissingVolumeError) Error() string {
	if e.N == 0 {
		return "a volume is missing"
	}
	return fmt.Sprintf("volume %d is missing", e.N)
}

// DamagedError is a damaged archive, or not what its name says; the
// decoder's words, for the log.
type DamagedError struct{ Why string }

func (e *DamagedError) Error() string { return "damaged: " + e.Why }

// TooBigError is an archive that would write more than the limit (a bomb,
// or not enough free space).
type TooBigError struct{ Would, Limit int64 }

func (e *TooBigError) Error() string {
	return fmt.Sprintf("would write %d bytes (limit %d)", e.Would, e.Limit)
}

func damaged(why string) error { return &DamagedError{Why: why} }

// BaseName is the archive's name without its archive extensions
// (movie.part1.rar → movie, photos.tar.gz → photos, x.7z.001 → x): the
// folder Subfolder makes.
func BaseName(archive string) string {
	name := filepath.Base(archive)
	if v, ok := VolumeKey(name); ok {
		return v.Base
	}
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tar.xz", ".tgz", ".txz", ".zip", ".7z", ".rar", ".tar", ".gz", ".xz"} {
		if strings.HasSuffix(lower, ext) && len(lower) > len(ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

// ArchiveName reports whether a file name is an archive's (or one volume of
// one), by its name only: the first bytes decide once it is extracted.
func ArchiveName(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := VolumeKey(lower); ok {
		return true
	}
	for _, e := range []string{".zip", ".7z", ".rar", ".tar", ".tgz", ".tar.gz", ".txz", ".tar.xz", ".gz", ".xz"} {
		if strings.HasSuffix(lower, e) && len(lower) > len(e) {
			return true
		}
	}
	return false
}

// Extract extracts archive (a split archive's first volume) as p says.
func Extract(archive string, p Policy) (Report, error) {
	format, err := Detect(archive)
	if err != nil {
		return Report{}, err
	}
	if format == Unknown {
		return Report{}, ErrUnsupported
	}
	if p.Limits == (Limits{}) {
		p.Limits = DefaultLimits()
	}
	if p.Subfolder == "" {
		p.Subfolder = Smart
	}
	staging, err := newStaging(p.Dest)
	if err != nil {
		return Report{}, err
	}
	defer staging.remove()
	w := newWriter(staging.path, p.Limits, p.Cancel)
	switch format {
	case Zip:
		err = extractZip(archive, w, p)
	case Tar, TarGz, TarXz:
		err = extractTar(archive, format, w)
	case Gz, Xz:
		err = extractSingle(archive, format, w)
	case SevenZip, Rar:
		if p.Tool == "" {
			return Report{}, ErrNoTool
		}
		// A split 7z's volumes are all there before 7-Zip is asked (it
		// would only say "unexpected end"): the first one missing is named.
		if v, ok := VolumeKey(filepath.Base(archive)); ok && v.Kind == Split {
			mf, err := OpenSplit(archive)
			if err != nil {
				return Report{}, err
			}
			mf.Close()
		}
		err = extractExternal(p.Tool, archive, w, p.Password)
	}
	if err != nil {
		return Report{}, err
	}
	report, placed, err := staging.place(BaseName(archive), p.Subfolder, p.OnFile)
	if err != nil {
		return Report{}, err
	}
	report.Skipped, report.Files, report.Bytes = w.skipped, placed, w.written
	return report, nil
}
