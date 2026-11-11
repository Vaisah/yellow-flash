// Ported from Mister Download Manager, crates/extract/src/safe.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package extract

// The rules every entry passes, whatever the format:
//   - a path is relative, has no "..", no drive or device prefix and no ':'
//     (an NTFS stream name would let a.txt:Zone.Identifier overwrite the
//     file's mark), and each part is made a safe file name; it can't leave
//     the staging folder;
//   - links, devices and pipes are left out, and nothing is written through
//     a link;
//   - limits on what is written (a bomb), the number of entries and the
//     depth;
//   - written into an empty staging folder, then moved into place without
//     overwriting anything.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/vaisah/internal/infrastructure/filesystem"
)

// Limits is what an extraction may write.
type Limits struct {
	// Total is the bytes in all.
	Total   int64
	Entries int64
	// Depth is folders inside folders.
	Depth int
}

// ForArchive is the limits for an archive of size bytes: at most 200 times
// its size or 20 GiB, whichever is more (real archives compress well, bombs
// a thousandfold), 100 000 entries, 64 levels.
func ForArchive(size int64) Limits {
	total := int64(20 << 30)
	if size > 0 && size < (1<<62)/200 && size*200 > total {
		total = size * 200
	}
	return Limits{Total: total, Entries: 100_000, Depth: 64}
}

// DefaultLimits is ForArchive(0).
func DefaultLimits() Limits { return ForArchive(0) }

// RelPath is an entry's name inside an archive as a safe path under the
// extraction folder, or why it can't be one. Both '/' and '\' separate
// (Windows tools write '\').
func RelPath(name string, depth int) (string, error) {
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", errors.New("absolute path")
	}
	var parts []string
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "." {
			continue
		}
		if part == ".." {
			return "", errors.New("goes outside the folder")
		}
		// A drive (C:), a device or a stream name: never part of a file name here.
		if strings.Contains(part, ":") {
			return "", errors.New("drive or stream name")
		}
		safe, err := filesystem.SanitizeFilename(part)
		if err != nil || safe == "" {
			continue
		}
		parts = append(parts, safe)
		if len(parts) > depth {
			return "", errors.New("folders too deep")
		}
	}
	if len(parts) == 0 {
		return "", errors.New("empty name")
	}
	out := filepath.Join(parts...)
	// Belt and braces: nothing but plain names went in.
	if !filepath.IsLocal(out) {
		return "", errors.New("not a plain path")
	}
	return out, nil
}

// throughLink reports whether a folder on the way from root to path (or
// path itself) is a link: nothing is written through one (a junction or
// symlink made by an archive, or by anyone).
func throughLink(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
		return true
	}
	at := root
	for _, c := range strings.Split(rel, string(filepath.Separator)) {
		if c == "." || c == "" {
			continue
		}
		at = filepath.Join(at, c)
		fi, err := os.Lstat(at)
		if err != nil {
			break
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return true
		}
	}
	return false
}

// writer writes entries into the staging folder, counting what is written
// against the limits.
type writer struct {
	root    string
	limits  Limits
	cancel  *atomic.Bool
	written int64
	entries int64
	skipped []Skipped
}

func newWriter(root string, limits Limits, cancel *atomic.Bool) *writer {
	return &writer{root: root, limits: limits, cancel: cancel}
}

func (w *writer) cancelled() bool { return w.cancel != nil && w.cancel.Load() }

// skip leaves an entry out, with why (kept for the download's log).
func (w *writer) skip(name, why string) { w.skipped = append(w.skipped, Skipped{Name: name, Why: why}) }

func (w *writer) count() error {
	w.entries++
	if w.entries > w.limits.Entries {
		return ErrTooMany
	}
	if w.cancelled() {
		return ErrCancelled
	}
	return nil
}

// dir is a folder entry.
func (w *writer) dir(name string) error {
	if err := w.count(); err != nil {
		return err
	}
	rel, err := RelPath(name, w.limits.Depth)
	if err != nil {
		w.skip(name, err.Error())
		return nil
	}
	path := filepath.Join(w.root, rel)
	if throughLink(w.root, path) {
		w.skip(name, "through a link")
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

// file is a file entry: its bytes read from data (at most size and a
// little more, when the archive says how big it is: size < 0 is unknown).
// mode < 0 is none.
func (w *writer) file(name string, size int64, data io.Reader, mode int64) error {
	if err := w.count(); err != nil {
		return err
	}
	rel, err := RelPath(name, w.limits.Depth)
	if err != nil {
		w.skip(name, err.Error())
		_, err = io.Copy(io.Discard, data) // a stream format's next entry follows
		return readError(err)
	}
	path := filepath.Join(w.root, rel)
	parent := filepath.Dir(path)
	if throughLink(w.root, parent) {
		w.skip(name, "through a link")
		_, err = io.Copy(io.Discard, data)
		return readError(err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	// Two entries of one name (an archive can hold that): the second is numbered.
	if _, err := os.Lstat(path); err == nil {
		path = Numbered(path)
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	entryCap := int64(-1)
	if size >= 0 {
		entryCap = size + 1<<20
	}
	buf := make([]byte, 256<<10)
	var this int64
	for {
		n, rerr := data.Read(buf)
		if n > 0 {
			this += int64(n)
			w.written += int64(n)
			if entryCap >= 0 && this > entryCap {
				return damaged(name + " is bigger than it says")
			}
			if w.written > w.limits.Total {
				return &TooBigError{Would: w.written, Limit: w.limits.Total}
			}
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			if w.cancelled() {
				return ErrCancelled
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return readError(rerr)
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	setMode(path, mode)
	return nil
}

// readError is a decoder's read error: bad data (a CRC, a wrong password)
// is damage; a file system's error stays itself.
func readError(err error) error {
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return err
	}
	return damaged(err.Error())
}

// setMode keeps only the execute bits on Unix (never setuid, setgid or
// sticky). Windows has no permission bits for an archive to carry.
func setMode(path string, mode int64) {
	if runtime.GOOS == "windows" || mode < 0 {
		return
	}
	keep := os.FileMode(0o644)
	if mode&0o111 != 0 {
		keep = 0o755
	}
	_ = os.Chmod(path, keep)
}

// Numbered is "name (1).ext", "name (2).ext"…: the first free one beside
// path.
func Numbered(path string) string {
	dir, file := filepath.Dir(path), filepath.Base(path)
	stem, ext := file, ""
	if i := strings.LastIndex(file, "."); i > 0 {
		stem, ext = file[:i], file[i:]
	}
	for n := 1; ; n++ {
		p := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
		if _, err := os.Lstat(p); err != nil {
			return p
		}
	}
}

// staging is an empty folder beside the destination that the archive is
// extracted into; removed with whatever is left in it (after a failure,
// everything).
type staging struct {
	dest, path string
}

var nextStaging atomic.Int64

// stagingPrefix starts every staging folder's name.
const stagingPrefix = ".yf-extract-"

func newStaging(dest string) (*staging, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dest, fmt.Sprintf("%s%d-%d", stagingPrefix, os.Getpid(), nextStaging.Add(1)))
	if _, err := os.Lstat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return nil, err
		}
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return nil, err
	}
	return &staging{dest: dest, path: path}, nil
}

func (s *staging) remove() {
	if _, err := os.Lstat(s.path); err == nil {
		_ = os.RemoveAll(s.path)
	}
}

// place moves what was extracted into place, as subfolder says; every file
// put there is told to onFile. It returns the report's folder and renamed
// count, and the files placed.
func (s *staging) place(base string, subfolder Subfolder, onFile func(string)) (Report, int64, error) {
	entries, err := os.ReadDir(s.path)
	if err != nil {
		return Report{}, 0, err
	}
	together := subfolder == Always || (subfolder == Smart && len(entries) > 1)
	var report Report
	if together {
		name, err := filesystem.SanitizeFilename(base)
		if err != nil {
			name = "extracted"
		}
		folder := filepath.Join(s.dest, name)
		if _, err := os.Lstat(folder); err == nil {
			folder = Numbered(folder)
			report.Renamed++
		}
		if err := os.Rename(s.path, folder); err != nil {
			return Report{}, 0, err
		}
		n, err := markAll(folder, onFile)
		report.Folder = folder
		return report, n, err
	}
	var placed int64
	for _, e := range entries {
		from := filepath.Join(s.path, e.Name())
		to := filepath.Join(s.dest, e.Name())
		if _, err := os.Lstat(to); err == nil {
			to = Numbered(to)
			report.Renamed++
		}
		if err := os.Rename(from, to); err != nil {
			return Report{}, 0, err
		}
		if e.IsDir() {
			n, err := markAll(to, onFile)
			if err != nil {
				return Report{}, 0, err
			}
			placed += n
		} else {
			if onFile != nil {
				onFile(to)
			}
			placed++
		}
	}
	report.Folder = s.dest
	return report, placed, nil
}

// markAll tells onFile every file under dir; links are never followed.
// Iterative (no recursion limit to hit on a deep tree).
func markAll(dir string, onFile func(string)) (int64, error) {
	var n int64
	todo := []string{dir}
	for len(todo) > 0 {
		d := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		entries, err := os.ReadDir(d)
		if err != nil {
			return n, err
		}
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			switch {
			case e.Type()&os.ModeSymlink != 0:
			case e.IsDir():
				todo = append(todo, p)
			default:
				if onFile != nil {
					onFile(p)
				}
				n++
			}
		}
	}
	return n, nil
}

// checkTree checks an external tool's output in the staging folder as our
// own entries are: anything that is a link, or not a plain file or folder,
// is removed before anything is moved into place, and so is a name that
// isn't safe. It returns what was left out.
func checkTree(root string, depth int) ([]Skipped, error) {
	var out []Skipped
	todo := []string{root}
	for len(todo) > 0 {
		d := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		entries, err := os.ReadDir(d)
		if err != nil {
			return out, err
		}
		for _, e := range entries {
			path := filepath.Join(d, e.Name())
			fi, err := os.Lstat(path)
			if err != nil {
				return out, err
			}
			rel, _ := filepath.Rel(root, path)
			why := ""
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				why = "link"
			case !fi.IsDir() && !fi.Mode().IsRegular():
				why = "not a file"
			default:
				if safe, err := RelPath(filepath.ToSlash(rel), depth); err != nil {
					why = err.Error()
				} else if safe != rel {
					why = "unsafe name"
				}
			}
			switch {
			case why != "":
				out = append(out, Skipped{Name: filepath.ToSlash(rel), Why: why})
				if fi.IsDir() {
					_ = os.RemoveAll(path)
				} else {
					_ = os.Remove(path)
				}
			case fi.IsDir():
				todo = append(todo, path)
			}
		}
	}
	return out, nil
}
