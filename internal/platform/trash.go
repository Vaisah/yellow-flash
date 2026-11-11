package platform

import (
	"errors"
	"os"
	"path/filepath"
)

// TrashDirEnv, for tests only, sends what Trash is given into that folder
// instead, so a test never fills the real (per-user, shared) Recycle Bin or
// Trash. Never set for users.
const TrashDirEnv = "YELLOWFLASH_TEST_TRASH_DIR"

// Trash moves files to the Recycle Bin (Windows) or the Trash (Linux:
// gio trash), from where the user can take them back.
func Trash(files []string) error {
	if dir := os.Getenv(TrashDirEnv); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		var first error
		for _, f := range files {
			if err := os.Rename(f, filepath.Join(dir, filepath.Base(f))); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	if len(files) == 0 {
		return nil
	}
	return trash(files)
}

// errNoTrash: this system has no Trash Yellow Flash can use.
var errNoTrash = errors.New("there is no Trash to move it to")
