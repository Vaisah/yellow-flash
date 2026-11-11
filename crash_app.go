// Ported from Mister Download Manager, crates/app/src/crashui.rs (the crash report window's backend)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Crash reports in the app: the last run's crash, made a report at this
// start (main.go), is offered once (LastCrashReport); every report kept
// can be read, sent and removed. Sending opens GitHub's Crash report form,
// filled in, in the browser: the user reads it and submits it there.
// Nothing is sent by itself; looking for an issue filed already sends only
// the problem's signature, and only when asked.

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/vaisah/internal/crash"
	"github.com/vaisah/internal/platform"
)

// ListCrashReports is the crash reports kept, newest first.
func (a *App) ListCrashReports() []crash.Report { return crash.List(a.dataDir) }

// LastCrashReport is the report this start made of the last run's crash
// (nil: it ended well, or the window was told already).
func (a *App) LastCrashReport() *crash.Report {
	a.crashMu.Lock()
	defer a.crashMu.Unlock()
	r := a.lastCrash
	a.lastCrash = nil
	return r
}

func (a *App) crashReport(id string) (crash.Report, error) {
	for _, r := range crash.List(a.dataDir) {
		if r.ID == id {
			return r, nil
		}
	}
	return crash.Report{}, errors.New("no such crash report")
}

// CrashIssueURL is GitHub's Crash report form filled in from a report (and
// what the user says they were doing), for the browser.
func (a *App) CrashIssueURL(id, what string) (string, error) {
	r, err := a.crashReport(id)
	if err != nil {
		return "", err
	}
	title := r.Signature
	if len(title) > 120 {
		title = title[:120]
	}
	fields := []crash.Field{
		{ID: "signature", Value: r.Signature},
		{ID: "kind", Value: string(r.Kind)},
		{ID: "version", Value: AppVersion},
		{ID: "os", Value: runtime.GOOS + " " + runtime.GOARCH},
		{ID: "what", Value: strings.TrimSpace(what)},
	}
	return crash.NewIssueURL(title, fields, r.Text,
		"(The report was cut to fit: drag its file, "+r.ID+".txt, into this box for the whole of it.)"), nil
}

// FindCrashIssue looks on GitHub for the issue filed already for a
// report's problem (nil: none). Only its signature is sent.
func (a *App) FindCrashIssue(id string) (*crash.Found, error) {
	r, err := a.crashReport(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return crash.FindIssue(ctx, &http.Client{Timeout: 20 * time.Second}, r.Signature)
}

// DeleteCrashReport removes a report.
func (a *App) DeleteCrashReport(id string) error { return crash.Remove(a.dataDir, id) }

// ShowCrashReports opens the reports' folder.
func (a *App) ShowCrashReports() error { return platform.OpenPath(crash.Dir(a.dataDir)) }
