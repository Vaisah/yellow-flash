// Ported from Mister Download Manager, crates/nmhost/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Command nm-host is Yellow Flash's browser native messaging host (Chrome,
// Edge, Brave, Firefox and their kin). Build it as yellowflash-nmhost,
// beside the app.
//
//   - Started by the browser: reads native messages (u32 LE length + JSON)
//     from stdin, hands each download to the running app over ipc (starting
//     it if needed) and answers {"ok": …}. A download Options leaves to the
//     browser (file type, excluded site or address, browser, integration
//     off) is answered {"ok": false} without starting anything, and the
//     extension lets the browser have it. {"rules": true} asks for the file
//     types to offer.
//   - The download button over videos: {"panel": true, "url": page} asks
//     whether it may show; {"video": true, "url": page} opens the Download
//     video window; {"hideSite": host} keeps it off that site;
//     {"openOptions": true} opens Options where it is set up.
//   - yellowflash-nmhost --register / --unregister writes / removes the host
//     manifests. Windows: beside this exe, with the per-user (HKCU) registry
//     entries the browsers look up; with --all-users under HKLM. Linux and
//     macOS: in each installed browser's own host folder, or the
//     system-wide ones with --all-users.
//   - yellowflash-nmhost --package-manifests <root> (Linux packaging):
//     writes the system-wide manifests under root, naming the packaged
//     helper.
//
// Safari is not supported (MDM's --safari path needs its Safari extension).
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
)

// programName is this program's file name, without .exe.
const programName = "yellowflash-nmhost"

func main() {
	args := os.Args[1:]
	allUsers := slices.Contains(args, "--all-users")
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	var err error
	switch {
	case first == "--register":
		err = register(allUsers)
	case first == "--unregister":
		err = unregister(allUsers)
	case first == "--package-manifests":
		root := ""
		if len(args) > 1 {
			root = args[1]
		}
		err = packageManifests(root)
	// Browsers start the host naming the caller: serve only our own
	// extension (the manifests say the same; this is a second check).
	case ourCaller(args):
		err = serve()
	default:
		err = fmt.Errorf("refusing caller %q: not the Yellow Flash extension", args)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", programName, err)
		os.Exit(1)
	}
}

// ourCaller: Chromium passes the extension's origin first (unpacked or
// from a store); Firefox passes the manifest's path, then the add-on ID.
func ourCaller(args []string) bool {
	if len(args) > 0 && slices.Contains(browser.AllowedOrigins(), args[0]) {
		return true
	}
	return len(args) > 1 && args[1] == browser.FirefoxID
}

func serve() error {
	dataDir, err := ipc.DataDir()
	if err != nil {
		return err
	}
	h := &host{
		deliver: func(cmd ipc.Command) Reply { return delivered(dataDir, cmd) },
		log:     func(line string) { logLine(dataDir, line) },
	}
	return h.serve(os.Stdin, os.Stdout,
		func() browser.CaptureRules { return browser.LoadRules(dataDir) },
		func(b, v string) { _ = browser.MarkSeen(dataDir, b, v) })
}

// logLine appends a line to the app's log (<data>/logs/yellowflash.log),
// dated in UTC. The host runs apart from the app, which may not be
// running; a line is written whole, so it never splits another's.
func logLine(dataDir, message string) {
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "yellowflash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
	_, _ = f.WriteString(stamp + "  INFO nmhost: " + message + "\n")
}

// delivered hands cmd to the app: the reply saying so, or why not.
func delivered(dataDir string, cmd ipc.Command) Reply {
	err := deliver(dataDir, cmd)
	switch {
	case err == nil:
		return done()
	case errors.Is(err, ipc.ErrRefused):
		answer := strings.TrimPrefix(err.Error(), ipc.ErrRefused.Error()+": ")
		if strings.HasPrefix(answer, "too-large") {
			return failed("too-large", answer)
		}
		return failed("refused", answer)
	default:
		return failed("not-running", err.Error())
	}
}

// startWait is how long an app just started gets to open its channel (a
// first start, a slow disk or an antivirus scan can take a while); the
// browser's download waits meanwhile.
const startWait = 20 * time.Second

// deliver hands cmd to the app, starting it only if none is running. The
// error wraps ipc.ErrRefused when an app answered and said no.
func deliver(dataDir string, cmd ipc.Command) error {
	// Whatever it asks may open a window: let the app come to the front
	// (Windows' foreground lock; nothing elsewhere).
	if pid, ok := ipc.AppPID(dataDir); ok {
		platform.AllowForeground(pid)
	}
	err := ipc.Send(dataDir, cmd)
	if err == nil || errors.Is(err, ipc.ErrRefused) {
		return err
	}
	app, err := appPath()
	if err != nil {
		return err
	}
	// Started outside the browser's reach: browsers end a host's whole
	// process tree (on Windows a job object, elsewhere its process group)
	// once it has answered, which would end the app along with it. Its
	// standard streams are the null device, never the browser's pipes.
	child, err := startOutsideJob(app)
	if err != nil {
		return fmt.Errorf("cannot start %s: %w", app, err)
	}
	platform.AllowForeground(child.Process.Pid)
	exited := make(chan struct{})
	go func() { _ = child.Wait(); close(exited) }()
	started := time.Now()
	for time.Since(started) < startWait {
		time.Sleep(100 * time.Millisecond)
		err := ipc.Send(dataDir, cmd)
		if err == nil || errors.Is(err, ipc.ErrRefused) {
			return err
		}
		// It quit (another copy took over, or it failed to start): that one
		// may answer still, for a while.
		select {
		case <-exited:
			if time.Since(started) > startWait/4 {
				return errors.New("Yellow Flash did not start")
			}
		default:
		}
	}
	return errors.New("Yellow Flash did not start")
}

// appPath is the app beside this program, or the path written in
// "app-path" next to it (a copy of the host kept outside the app's folder).
func appPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	if raw, err := os.ReadFile(filepath.Join(dir, "app-path")); err == nil {
		if p := strings.TrimSpace(string(raw)); p != "" {
			return p, nil
		}
	}
	names := []string{"Yellow Flash", "yellow-flash"}
	if runtime.GOOS == "windows" {
		names = []string{"Yellow Flash.exe", "yellow-flash.exe"}
	}
	for _, n := range names {
		if p := filepath.Join(dir, n); isFile(p) {
			return p, nil
		}
	}
	return filepath.Join(dir, names[0]), nil
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
