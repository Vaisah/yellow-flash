package platform

import (
	"errors"
	"os"
)

// ErrLocked is returned by LockFile when another process holds the lock.
var ErrLocked = errors.New("file is locked by another process")

// LockFile opens (creating if needed) path and takes an exclusive OS lock on
// it without waiting. The lock lasts until the returned file is closed or the
// process exits, so a crashed holder never leaves a stale lock behind.
// It returns ErrLocked when another process holds the lock.
func LockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err = tryLock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// AllowForeground lets process pid bring its window to the front. Windows
// only lets the foreground process do that, so a second launch grants the
// running instance the right before handing it a command. Elsewhere it is a
// no-op.
func AllowForeground(pid int) { allowForeground(pid) }
