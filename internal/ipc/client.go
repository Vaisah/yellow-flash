// Ported from Mister Download Manager, crates/ipc/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNotRunning: no app listens (no ipc.json, or nothing at its port).
	// Starting one may help.
	ErrNotRunning = errors.New("not running")
	// ErrRefused: an app answered but did not take the command ("denied",
	// "too-large", "gone" while it quits), or stopped answering. Starting
	// another cannot help.
	ErrRefused = errors.New("refused")
)

const (
	dialTimeout = 500 * time.Millisecond
	// clientDeadline covers the whole exchange: the server waits up to
	// replyTimeout for the app's answer, plus transfer time.
	clientDeadline = replyTimeout + 3*time.Second
)

// AppPID returns the running app's process id as ipc.json in dataDir says.
func AppPID(dataDir string) (int, bool) {
	ep, err := readEndpoint(dataDir)
	if err != nil || ep.PID == 0 {
		return 0, false
	}
	return ep.PID, true
}

// Send hands cmd to the app running for dataDir. The error wraps
// ErrNotRunning or ErrRefused.
func Send(dataDir string, cmd Command) error {
	ack, err := exchange(dataDir, cmd)
	if err != nil {
		return err
	}
	if ack == "ok" {
		return nil
	}
	if ack == "" {
		ack = "no answer"
	}
	return fmt.Errorf("%w: %s", ErrRefused, ack)
}

// SendWaiting is Send, retried every 100 ms for up to wait: the instance
// holding the lock may have just been launched and not be listening yet.
func SendWaiting(dataDir string, cmd Command, wait time.Duration) error {
	start := time.Now()
	for {
		err := Send(dataDir, cmd)
		if err == nil || time.Since(start) >= wait {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Request asks the running app something (automation API) and returns its
// one-line answer.
func Request(dataDir string, cmd Command) (string, error) { return exchange(dataDir, cmd) }

func readEndpoint(dataDir string) (endpoint, error) {
	var ep endpoint
	b, err := os.ReadFile(endpointPath(dataDir))
	if err != nil {
		return ep, err
	}
	err = json.Unmarshal(b, &ep)
	return ep, err
}

// exchange sends one request line and reads one answer line ("" when the
// server closed without answering).
func exchange(dataDir string, cmd Command) (string, error) {
	ep, err := readEndpoint(dataDir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	return exchangeAt(ep, cmd)
}

func exchangeAt(ep endpoint, cmd Command) (string, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(ep.Port))
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(clientDeadline))
	line, err := json.Marshal(envelope{Token: ep.Token, Command: cmd})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRefused, err)
	}
	if _, err = conn.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("%w: %v", ErrRefused, err)
	}
	ack, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: %v", ErrRefused, err)
	}
	return strings.TrimRight(ack, "\r\n"), nil
}
