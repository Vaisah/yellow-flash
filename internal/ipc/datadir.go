// Ported from Mister Download Manager, crates/ipc/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package ipc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/vaisah/internal/platform"
)

// AppName names the data folder.
const AppName = "Yellow Flash"

// AppVersion is the app's version; the native host, which ships with it,
// tells the extension the same.
const AppVersion = "0.2.0"

// DataDirEnv overrides the data folder: a separate instance with its own
// list and channel (tests, screenshots).
const DataDirEnv = "YELLOWFLASH_DATA_DIR"

// legacyName is the data folder of builds before the Yellow Flash name.
const legacyName = "Download Manager"

var (
	dataDirMu       sync.Mutex
	dataDirOverride string
)

// SetDataDir makes DataDir return dir from now on (over DataDirEnv).
func SetDataDir(dir string) {
	dataDirMu.Lock()
	defer dataDirMu.Unlock()
	dataDirOverride = dir
}

// DataDir returns the app's data folder: the list, settings and command
// channel. In order: SetDataDir's folder, $YELLOWFLASH_DATA_DIR, portable
// mode (a writable "data" folder next to the program), else
// os.UserConfigDir()/Yellow Flash. The legacy "Download Manager" folder is
// moved there once. The folder is not created.
func DataDir() (string, error) {
	dataDirMu.Lock()
	override := dataDirOverride
	dataDirMu.Unlock()
	if override != "" {
		return override, nil
	}
	if dir := os.Getenv(DataDirEnv); dir != "" {
		return dir, nil
	}
	if dir, ok := PortableDir(); ok {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, AppName)
	legacy := filepath.Join(base, legacyName)
	if _, err = os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if info, legacyErr := os.Stat(legacy); legacyErr == nil && info.IsDir() {
			_ = os.Rename(legacy, dir)
		}
	}
	return dir, nil
}

// PortableDir returns the "data" folder next to the program when it exists
// and can be written.
func PortableDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(filepath.Dir(exe), "data")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	probe := filepath.Join(dir, ".write-test")
	f, err := os.Create(probe)
	if err != nil {
		return "", false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return dir, true
}

// PrivateDir creates dir, readable by its owner only on Linux and macOS (it
// holds the channel's token; a home folder is often open to other users).
func PrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// PrivateWrite writes data to path atomically (temp file, fsync, rename),
// readable by its owner only (mode 0600; Windows: the profile's ACLs).
func PrivateWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op once renamed
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ErrAlreadyRunning is returned by Lock when another instance holds the
// lock (or is starting).
var ErrAlreadyRunning = errors.New("another Yellow Flash instance is running")

// Lock makes this process the one running instance for dataDir: an OS lock
// on instance.lock, held until the returned Closer is closed or the process
// exits. It returns ErrAlreadyRunning when another process holds it.
func Lock(dataDir string) (io.Closer, error) {
	if err := PrivateDir(dataDir); err != nil {
		return nil, err
	}
	f, err := platform.LockFile(filepath.Join(dataDir, "instance.lock"))
	if errors.Is(err, platform.ErrLocked) {
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("instance lock: %w", err)
	}
	return f, nil
}

func endpointPath(dataDir string) string { return filepath.Join(dataDir, "ipc.json") }
