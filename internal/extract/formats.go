// Ported from Mister Download Manager, crates/extract/src/{zip_fmt,tar_fmt,single}.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package extract

// The formats read in process. Every entry is written by us through the
// writer (never a library's own "extract all"), links left out.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"cmp"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ulikunitz/xz"
)

/* ---------- zip ---------- */

// extractZip writes a zip's entries. Overlapping entries (a non-recursive
// bomb) are refused before anything is written. Zips Go's reader can't
// open (encrypted, Deflate64, others) go to 7-Zip, as 7z and RAR do.
func extractZip(archive string, w *writer, p Policy) error {
	var (
		ra   io.ReaderAt
		size int64
	)
	if v, ok := VolumeKey(filepath.Base(archive)); ok && v.Kind == Split {
		mf, err := OpenSplit(archive)
		if err != nil {
			return err
		}
		defer mf.Close()
		ra, size = mf, mf.Size()
	} else {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		ra, size = f, fi.Size()
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return damaged(err.Error())
	}
	type span struct{ start, end int64 }
	var spans []span
	external, locked := false, false
	for _, f := range zr.File {
		if f.Flags&0x1 != 0 {
			external, locked = true, true
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			external = true
		}
		off, err := f.DataOffset()
		if err != nil {
			return damaged(err.Error())
		}
		spans = append(spans, span{off, off + int64(f.CompressedSize64)})
	}
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.start, b.start) })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return damaged("entries overlap")
		}
	}
	if external {
		switch {
		case locked && p.Password == "":
			return ErrNeedsPassword
		case p.Tool == "":
			return ErrNoTool
		}
		return extractExternal(p.Tool, archive, w, p.Password)
	}
	for _, f := range zr.File {
		mode := f.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			w.skip(f.Name, "link")
			continue
		case f.FileInfo().IsDir():
			if err := w.dir(f.Name); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			w.skip(f.Name, "link or device")
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return damaged(err.Error())
		}
		unixMode := int64(-1)
		if f.CreatorVersion>>8 == 3 { // made on Unix: the entry has a mode
			unixMode = int64(mode.Perm())
		}
		err = w.file(f.Name, int64(f.UncompressedSize64), rc, unixMode)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

/* ---------- tar ---------- */

// extractTar reads a tar, plain or in gzip or xz, entry by entry; links,
// hard links, devices and pipes are left out.
func extractTar(archive string, format Format, w *writer) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := decompressor(bufio.NewReader(f), format)
	if err != nil {
		return err
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return damaged(err.Error())
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = w.dir(h.Name)
		case tar.TypeReg, tar.TypeCont, '\x00': // '\x00': old tars' regular file
			err = w.file(h.Name, h.Size, tr, h.Mode)
		case tar.TypeXGlobalHeader:
			// Read by archive/tar for the entries it describes.
		default:
			w.skip(h.Name, "link or device")
		}
		if err != nil {
			return err
		}
	}
}

func decompressor(r io.Reader, format Format) (io.Reader, error) {
	switch format {
	case TarGz, Gz:
		g, err := gzip.NewReader(r) // every member (multistream)
		if err != nil {
			return nil, damaged(err.Error())
		}
		return g, nil
	case TarXz, Xz:
		x, err := xz.NewReader(r)
		if err != nil {
			return nil, damaged(err.Error())
		}
		return x, nil
	}
	return r, nil
}

/* ---------- one compressed file ---------- */

// extractSingle is one file compressed with gzip or xz: report.csv.gz →
// report.csv.
func extractSingle(archive string, format Format, w *writer) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	name := filepath.Base(archive)
	lower := strings.ToLower(name)
	inner := name + ".out"
	for _, e := range []string{".gz", ".xz"} {
		if strings.HasSuffix(lower, e) && len(lower) > len(e) {
			inner = name[:len(name)-len(e)]
		}
	}
	r, err := decompressor(bufio.NewReader(f), format)
	if err != nil {
		return err
	}
	return w.file(inner, -1, r, -1)
}
