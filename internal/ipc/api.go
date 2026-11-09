// Ported from Mister Download Manager, crates/ipc/src/api.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package ipc

import (
	"encoding/json"
	"fmt"
)

// The automation API's answers, shared by the app (which writes them) and
// the programs that read them (a command line, an MCP server), so both
// sides keep one shape. Yellow Flash download IDs are UUID strings.

// ListAnswer answers {"query": "list"}: every download and the total speed.
type ListAnswer struct {
	Downloads []DownloadInfo `json:"downloads"`
	// Speed is the total download speed in bytes per second.
	Speed int64 `json:"speed"`
	// Queues in the scheduler's order; the first is the main queue.
	Queues []QueueInfo `json:"queues"`
	// Categories a new download can go to (General first).
	Categories []CategoryInfo `json:"categories"`
	// Free is the free space where downloads go, in bytes; nil when unknown.
	Free *int64 `json:"free"`
}

// QueueInfo is one queue of the list.
type QueueInfo struct {
	// Name is what Command.Queue takes.
	Name    string   `json:"name"`
	Running bool     `json:"running"`
	IDs     []string `json:"ids"`
}

// CategoryInfo is one category of the list.
type CategoryInfo struct {
	// Key is what Command.Category takes and DownloadInfo.Category says.
	Key string `json:"key"`
	// Name is as the user sees it.
	Name string `json:"name"`
}

// AddAnswer answers an added URL: the download's id, or Pending while a
// torrent's files are looked up.
type AddAnswer struct {
	ID      string `json:"id,omitempty"`
	Pending bool   `json:"pending"`
}

// DownloadInfo is one download of the list.
type DownloadInfo struct {
	// ID is what actions take; never reused.
	ID   string `json:"id"`
	File string `json:"file"`
	URL  string `json:"url"`
	// Path is where it is saved.
	Path string `json:"path"`
	// Status is one of Statuses.
	Status string `json:"status"`
	// Size in bytes; nil while unknown.
	Size       *int64 `json:"size"`
	Downloaded int64  `json:"downloaded"`
	// Speed in bytes per second; 0 when not running.
	Speed int64 `json:"speed"`
	// Added is when it was added (RFC 3339); nil when unknown.
	Added    *string `json:"added"`
	Category string  `json:"category"`
	// Error is why it failed, for a download in "Error".
	Error string `json:"error,omitempty"`
}

// Statuses are the values DownloadInfo.Status takes.
var Statuses = [...]string{"Queued", "Downloading", "Stopped", "Complete", "Error"}

// ActAnswer answers {"action": …, "ids": […]}: what was done, and what was
// skipped and why.
type ActAnswer struct {
	Done    []string  `json:"done"`
	Skipped []Skipped `json:"skipped"`
}

// Skipped is one download an action left alone.
type Skipped struct {
	ID string `json:"id"`
	// Why: "no such download", "not running", "already running",
	// "complete" or "running: pause it first".
	Why string `json:"why"`
}

// PairAnswer answers {"pair": true}: a new pairing code for the remote
// control, while it is on.
type PairAnswer struct {
	// Code is the 8-digit code; it works once, for Minutes.
	Code    string   `json:"code"`
	Minutes int      `json:"minutes"`
	URLs    []string `json:"urls"`
	// QR is a QR code of the first URL with the code, drawn for a terminal.
	QR string `json:"qr"`
}

// APIAnswer answers {"api": true|false}: the API's new state.
type APIAnswer struct {
	API bool `json:"api"`
}

// ErrorAnswer answers an unknown question or action, or any question while
// the API is off.
type ErrorAnswer struct {
	// Error is for people; its wording may change.
	Error string `json:"error"`
	// Code is for programs: APIOff while the API is off.
	Code string `json:"code,omitempty"`
}

// APIOff is ErrorAnswer.Code while the automation API is off.
const APIOff = "api-off"

// Line renders an answer as its line.
func Line(answer any) string {
	b, err := json.Marshal(answer)
	if err != nil {
		return ""
	}
	return string(b)
}

// ParseAnswer reads an answer line back: an *ErrorAnswer when the app
// answered with one, else the answer asked for.
func ParseAnswer[T any](line string) (T, *ErrorAnswer, error) {
	var zero T
	var probe struct {
		Error *string `json:"error"`
		Code  string  `json:"code"`
	}
	if json.Unmarshal([]byte(line), &probe) == nil && probe.Error != nil {
		return zero, &ErrorAnswer{Error: *probe.Error, Code: probe.Code}, nil
	}
	var v T
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return zero, nil, fmt.Errorf("unexpected answer (%v): %s", err, line)
	}
	return v, nil, nil
}
