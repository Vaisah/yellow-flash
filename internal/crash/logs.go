// Ported from Mister Download Manager, crates/crash/src/logs.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package crash

// The log Yellow Flash keeps in release builds too: <data>/logs/
// yellowflash.log, rolled over at a size into yellowflash.1.log …
// yellowflash.3.log. Each line is written through at once (a crash loses
// nothing that was logged), and a report takes the last lines.

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxLogBytes is how big a log file grows before it rolls over; KeepLogs
// is how many rolled-over files are kept (.1 newest).
const (
	MaxLogBytes = 2 << 20
	KeepLogs    = 3
	logName     = "yellowflash"
)

// LogDir is the log's folder in a data folder.
func LogDir(data string) string { return filepath.Join(data, "logs") }

func currentLog(dir string) string { return filepath.Join(dir, logName+".log") }

func rolledLog(dir string, n int) string {
	return filepath.Join(dir, fmt.Sprintf("%s.%d.log", logName, n))
}

// Rolling appends to yellowflash.log, rolling it over past MaxLogBytes.
type Rolling struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	size int64
}

// NewRolling opens the log in dir.
func NewRolling(dir string) (*Rolling, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(currentLog(dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	var size int64
	if fi, err := f.Stat(); err == nil {
		size = fi.Size()
	}
	return &Rolling{dir: dir, file: f, size: size}, nil
}

func (r *Rolling) roll() {
	if r.file != nil {
		r.file.Close()
		r.file = nil
	}
	for n := KeepLogs - 1; n >= 1; n-- {
		_ = os.Rename(rolledLog(r.dir, n), rolledLog(r.dir, n+1))
	}
	_ = os.Rename(currentLog(r.dir), rolledLog(r.dir, 1))
	r.file, _ = os.OpenFile(currentLog(r.dir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	r.size = 0
}

// Write appends p (a line), rolling over first when it wouldn't fit.
func (r *Rolling) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > MaxLogBytes {
		r.roll()
	}
	if r.file == nil {
		return len(p), nil // the folder went away: the app runs on without a log
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the log.
func (r *Rolling) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// NewLogger is a logger into <data>/logs at info (YELLOWFLASH_LOG=debug for
// everything), and its file; nil when the folder can't be written (the app
// runs on without a log).
func NewLogger(data string) (*slog.Logger, *Rolling) {
	r, err := NewRolling(LogDir(data))
	if err != nil {
		return nil, nil
	}
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("YELLOWFLASH_LOG"), "debug") {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(r, &slog.HandlerOptions{Level: level})), r
}

// Tail is the last n lines of the log (across the roll-over: the end of
// yellowflash.1.log when yellowflash.log is short).
func Tail(data string, n int) []string {
	dir := LogDir(data)
	var lines []string
	for _, p := range []string{currentLog(dir), rolledLog(dir, 1)} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		if text != "" {
			lines = append(strings.Split(text, "\n"), lines...)
		}
		if len(lines) >= n {
			break
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
