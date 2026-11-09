package platform

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLockFileIsExclusiveUntilClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock")
	first, err := LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// flock and LockFileEx both refuse a second handle, even in the same process.
	if _, err = LockFile(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: got %v, want ErrLocked", err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockFile(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again.Close()
}
