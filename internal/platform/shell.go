package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidPath is returned for paths that are not absolute, existing and
// free of characters that could break out of a command line.
var ErrInvalidPath = errors.New("invalid path")

// OpenPath opens a file or folder with the user's default application.
func OpenPath(path string) error {
	if err := checkPath(path); err != nil {
		return err
	}
	return openPath(path)
}

// RevealPath shows a file selected in the system file manager (or opens its
// folder where selection is not supported).
func RevealPath(path string) error {
	if err := checkPath(path); err != nil {
		return err
	}
	return revealPath(path)
}

func checkPath(path string) error {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\"\x00\r\n") {
		return fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	return nil
}
