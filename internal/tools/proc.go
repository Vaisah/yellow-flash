// Ported from Mister Download Manager, crates/tools/src/proc.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/vaisah/internal/platform"
)

// ErrTimeout: a helper didn't finish in time and was stopped.
var ErrTimeout = errors.New("the helper did not finish in time")

// output runs exe with args to the end within timeout, without a console
// window, and returns its standard output (killed on timeout).
func output(ctx context.Context, exe string, args []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	platform.HideConsole(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%w: %s", ErrTimeout, exe)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", exe, err, tail(stderr.Bytes(), 512))
	}
	return stdout.Bytes(), nil
}

// tail is the last n bytes of b, for a message.
func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(bytes.TrimSpace(b))
}
