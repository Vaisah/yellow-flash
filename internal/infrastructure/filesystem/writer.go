package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// TempFileMode keeps in-progress data private to the user.
	TempFileMode fs.FileMode = 0o600
	// FinalFileMode is applied to published files (umask still applies).
	FinalFileMode fs.FileMode = 0o644
	// maxCandidates bounds the " (n)" search.
	maxCandidates = 1000
)

// ErrNoAvailableName is returned when every candidate name is taken.
var ErrNoAvailableName = errors.New("no available destination filename")

// OpenTemp opens (creating if needed) a temporary file for random-access
// writes. It never truncates existing data, so partial downloads survive.
func OpenTemp(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, TempFileMode)
}

// Preallocate sizes f to exactly size bytes. Most filesystems create a
// sparse file, so this reserves the length without writing data.
func Preallocate(f *os.File, size int64) error {
	if size < 0 {
		return nil
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == size {
		return nil
	}
	return f.Truncate(size)
}

// Publisher moves a finished temporary file to its final name without ever
// overwriting an existing file.
type Publisher struct {
	link   func(oldname, newname string) error
	rename func(oldname, newname string) error
}

// NewPublisher returns a Publisher using hard links, with an exclusive
// create plus rename fallback for filesystems without hard links (FAT,
// exFAT, some network shares).
func NewPublisher() *Publisher { return &Publisher{link: os.Link, rename: os.Rename} }

// Publish moves tempPath into dir as name, or "name (n).ext" when taken, and
// returns the filename used. Publishing the same temporary file again (for
// example after a crash between linking and cleanup) is recognised and
// completed instead of creating a duplicate.
func (p *Publisher) Publish(tempPath, dir, name string) (string, error) {
	if err := os.Chmod(tempPath, FinalFileMode); err != nil {
		return "", err
	}
	for n := 0; n < maxCandidates; n++ {
		candidate := Candidate(name, n)
		target := filepath.Join(dir, candidate)
		err := p.link(tempPath, target)
		if err == nil {
			if err = os.Remove(tempPath); err != nil {
				return candidate, fmt.Errorf("published %s but could not remove temporary file: %w", candidate, err)
			}
			return candidate, nil
		}
		if errors.Is(err, fs.ErrExist) {
			if sameFile(tempPath, target) {
				return candidate, os.Remove(tempPath)
			}
			continue
		}
		// Hard links unsupported: reserve the name exclusively, then
		// replace our own empty placeholder.
		placeholder, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, FinalFileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err = placeholder.Close(); err != nil {
			_ = os.Remove(target)
			return "", err
		}
		if err = p.rename(tempPath, target); err != nil {
			_ = os.Remove(target)
			return "", err
		}
		return candidate, nil
	}
	return "", ErrNoAvailableName
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}
