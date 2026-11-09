// Ported from Mister Download Manager, crates/ipc/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package ipc is Yellow Flash's single-instance command channel, shared by
// the app, a second launch of it (an IDM-style command line) and, later, the
// browser's native messaging host.
//
// The running app holds an OS lock on instance.lock in the data folder,
// listens on a random 127.0.0.1 port and writes {port, token, pid} to
// ipc.json there (readable by its owner only). A client sends one JSON line
// carrying the token and gets one line back. Only loopback connections with
// the right token are accepted.
//
// The package has no UI, engine or SQLite dependency: the app supplies a
// Handler that turns commands into service calls.
package ipc

import (
	"encoding/json"
	"time"
)

// Command is a request to the running app. Every field is optional.
type Command struct {
	// URL is the address to download (/d), or a magnet link or .torrent path.
	URL string `json:"url,omitempty"`
	// Dir is the save folder (/p); Name is the file name (/f).
	Dir  string `json:"dir,omitempty"`
	Name string `json:"name,omitempty"`
	// Later (/a) adds the download without starting it.
	Later bool `json:"later,omitempty"`
	// Silent (/n) adds it without showing the Add URL dialog.
	Silent bool `json:"silent,omitempty"`
	// StartQueue (/s) starts the main queue.
	StartQueue bool `json:"start_queue,omitempty"`

	// Browser: the native host sends this command for a web page (through
	// the extension). Its addresses are untrusted: they may not reach this
	// computer or its network.
	Browser bool `json:"browser,omitempty"`
	// From the browser: the request context to reuse.
	Referer string `json:"referer,omitempty"`
	Cookies string `json:"cookies,omitempty"`
	// CookieJar holds the same cookies with the domain, path and flags the
	// browser keeps for each (for yt-dlp's cookie file). An older extension
	// sends Cookies only.
	CookieJar []BrowserCookie `json:"cookie_jar,omitempty"`
	UserAgent string          `json:"user_agent,omitempty"`
	// FinalURL is where the browser's redirects from URL ended. URL stays
	// the address asked for, so an expired signed link can be renewed.
	FinalURL string `json:"final_url,omitempty"`
	// Size and Mime are what the browser's own download reported.
	Size *int64 `json:"size,omitempty"`
	Mime string `json:"mime,omitempty"`
	// Post and PostType re-send a form's download as a POST with this body.
	Post     string `json:"post,omitempty"`
	PostType string `json:"post_type,omitempty"`
	// Private marks a download from a private window: it is never saved to
	// the list.
	Private bool `json:"private,omitempty"`
	// Proxy is the proxy the browser uses for this address
	// ("http://host:port", "socks5h://…"); ProxyPAC is its auto-config
	// script's text. ProxyLogin: that proxy asked the browser for a login.
	Proxy      string `json:"proxy,omitempty"`
	ProxyPAC   string `json:"proxy_pac,omitempty"`
	ProxyLogin bool   `json:"proxy_login,omitempty"`

	// Automation API (off by default). Query "list" asks for every download.
	Query string `json:"query,omitempty"`
	// API turns the automation API on or off.
	API *bool `json:"api,omitempty"`
	// Action is "pause", "start", "remove" (record only) or "delete" (with
	// its file) for IDs, or "start_queue"/"stop_queue" for Queue.
	Action string   `json:"action,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	// Category is a category key for URL; empty: by the file's type.
	Category string `json:"category,omitempty"`
	// Queue names where URL waits (with Later), or the queue Action targets.
	Queue string `json:"queue,omitempty"`
	// Pair asks for a new remote-control pairing code.
	Pair bool `json:"pair,omitempty"`

	// Exit (/exit) makes the running app save and quit. It starts nothing
	// when no app runs.
	Exit bool `json:"exit,omitempty"`
	// Startup (/startup) marks a launch at session start; a running app
	// ignores it.
	Startup bool `json:"startup,omitempty"`
	// Show (/show <window>) opens one of the app's windows by name.
	Show string `json:"show,omitempty"`

	// From the browser's button over a video: open the video window for
	// this page.
	VideoPage string `json:"video_page,omitempty"`
	// From that button's menu: never show it on this host again.
	PanelHideSite string `json:"panel_hide_site,omitempty"`
	// From the browser's context menu: links to pick from.
	Links []string `json:"links,omitempty"`
	// From the browser's toolbar button: leave this host's downloads to the
	// browser from now on.
	ExcludeSite string `json:"exclude_site,omitempty"`

	// Remote is set by the in-process remote control for a paired device;
	// it is never read from a request line.
	Remote bool `json:"-"`
	// TorrentFile is an uploaded .torrent's bytes (remote control only).
	TorrentFile []byte `json:"-"`
}

// WantsReply reports whether the command asks for an answer line rather
// than "ok".
func (c Command) WantsReply() bool {
	return c.Query != "" || c.API != nil || c.Action != "" || c.Pair
}

// BrowserCookie is one cookie as the browser keeps it (cookies.getAll's
// fields). The extension's camelCase names hostOnly and expirationDate are
// accepted too.
type BrowserCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Domain is ".example.com" (sent to subdomains too) or a host name.
	Domain string `json:"domain"`
	// HostOnly: sent to Domain itself only, never its subdomains.
	HostOnly bool   `json:"host_only"`
	Path     string `json:"path"`
	// Secure: sent over HTTPS only.
	Secure bool `json:"secure"`
	// Expires is seconds since 1970; nil for a session cookie.
	Expires *float64 `json:"expires,omitempty"`
}

// UnmarshalJSON accepts both the snake_case and the extension's camelCase
// field names.
func (c *BrowserCookie) UnmarshalJSON(b []byte) error {
	type plain BrowserCookie
	var aux struct {
		plain
		HostOnlyCamel  *bool    `json:"hostOnly"`
		ExpirationDate *float64 `json:"expirationDate"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*c = BrowserCookie(aux.plain)
	if aux.HostOnlyCamel != nil {
		c.HostOnly = *aux.HostOnlyCamel
	}
	if c.Expires == nil && aux.ExpirationDate != nil {
		c.Expires = aux.ExpirationDate
	}
	return nil
}

// Incoming is a command as the app receives it. Reply is non-nil (buffered,
// capacity 1) when the command wants an answer; the app sends exactly one
// line to it.
type Incoming struct {
	Cmd   Command
	Reply chan<- string
}

// Handler hands a command to the app. It must not block for long; it
// returns false when the app is quitting and the command would be lost
// (the client is then told "gone").
type Handler func(Incoming) bool

// Sink is the app's command channel from inside its own process (the
// remote control).
type Sink struct{ handle Handler }

// NewSink returns a Sink that delivers through h.
func NewSink(h Handler) Sink { return Sink{handle: h} }

// Ask hands cmd to the app and waits up to wait for its one-line answer.
// ok is false when the app is quitting or did not answer in time.
func (s Sink) Ask(cmd Command, wait time.Duration) (answer string, ok bool) {
	reply := make(chan string, 1)
	if !s.handle(Incoming{Cmd: cmd, Reply: reply}) {
		return "", false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case answer = <-reply:
		return answer, true
	case <-timer.C:
		return "", false
	}
}

// endpoint is ipc.json's content.
type endpoint struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	// PID is the app's process id, so a client may let it take the
	// foreground (Windows: AllowSetForegroundWindow). 0: unknown.
	PID int `json:"pid,omitempty"`
}

// envelope is one request line: the token and the command's fields.
type envelope struct {
	Token string `json:"token"`
	Command
}
