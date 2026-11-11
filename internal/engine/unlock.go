// Ported from Mister Download Manager, crates/engine/src/unlock.rs (and its use in download.rs)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

// Downloads through a service account (debrid services such as
// Real-Debrid): a file host's link is turned, through the service's API and
// the user's own account, into a direct link the engine then downloads as
// any file (every connection, resume). The engine knows nothing of
// accounts: Config.Unlockers gives a download its Unlocker, asked before
// each attempt's first request (a start, a resume, a retry), so a direct
// link that died mid-download is replaced by today's.
//
// The direct link is never shown and never stored: the download's URL and
// FinalURL keep the host's link, and the log names only the direct link's
// server. The host's own headers (cookies, logins) are not sent to it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	domain "github.com/vaisah/internal/domain/download"
)

// Unlocker is a service that turns a link into the address the file
// actually comes from, with the user's account.
type Unlocker interface {
	// Name says who unlocks, for the log and errors: "Real-Debrid".
	Name() string
	// Unlock returns today's direct address for rawURL. (nil, nil): not for
	// this service after all (it doesn't support the host), and the engine
	// downloads rawURL itself. A refusal is a *ServiceError. note reports
	// waits ("preparing the link").
	Unlock(ctx context.Context, rawURL string, note func(string)) (*Unlocked, error)
}

// Unlockers picks the unlocker for a link: nil when no account takes it.
// It is asked on every attempt, so accounts may change while the app runs.
type Unlockers interface {
	UnlockerFor(rawURL string) Unlocker
}

// Unlocked is a service's direct link for a file.
type Unlocked struct {
	// URL is the direct link: never shown, never stored.
	URL string
	// FileName is the service's name for the file, used when the direct
	// link's server names none.
	FileName string
	// Size is the file's size when the service knows it (0: unknown).
	Size int64
	// MaxConnections is the most connections the service allows on it
	// (Real-Debrid's "chunks"); 0 is no cap.
	MaxConnections int
}

// Fault is why a service didn't give the file.
type Fault string

// The faults.
const (
	// FaultSignIn: the account's sign-in no longer works; sign in again.
	FaultSignIn Fault = "sign-in"
	// FaultNeedsApproval: the service wants this device allowed first (an email).
	FaultNeedsApproval Fault = "needs-approval"
	// FaultAccountBlocked: the account is locked, banned or not activated.
	FaultAccountBlocked Fault = "account-blocked"
	// FaultNotPremium: this needs a premium account (or premium time ended).
	FaultNotPremium Fault = "not-premium"
	// FaultHostDown: the service can't reach the file host now.
	FaultHostDown Fault = "host-down"
	// FaultLimitReached: the account's limit for this host (or its traffic) is used up for now.
	FaultLimitReached Fault = "limit-reached"
	// FaultTooManyActive: too many downloads at once through the account.
	FaultTooManyActive Fault = "too-many-active"
	// FaultIPRefused: the service refuses this computer's address (a VPN or a server).
	FaultIPRefused Fault = "ip-refused"
	// FaultFileUnavailable: the file is gone or unavailable on the host.
	FaultFileUnavailable Fault = "file-unavailable"
	// FaultFileRefused: the service won't give this file.
	FaultFileRefused Fault = "file-refused"
	// FaultPasswordNeeded: the file is password-protected.
	FaultPasswordNeeded Fault = "password-needed"
	// FaultTooManyRequests: slow down (ServiceError.Wait, when the service said).
	FaultTooManyRequests Fault = "too-many-requests"
	// FaultServiceDown: the service isn't answering or fails.
	FaultServiceDown Fault = "service-down"
	// FaultTorrentUnavailable: a torrent the service can't get (nobody
	// shares it, or its magnet link can't be read).
	FaultTorrentUnavailable Fault = "torrent-unavailable"
	// FaultUnsafeName: a torrent's file whose name can't be written safely
	// in its folder (ServiceError.Detail); nothing is written.
	FaultUnsafeName Fault = "unsafe-name"
	// FaultOther: anything else, in the service's own words (Detail).
	FaultOther Fault = "other"
)

// ServiceError is a service's refusal.
type ServiceError struct {
	// Service is who refused: "Real-Debrid".
	Service string
	Fault   Fault
	// Wait is how long the service asked to wait (FaultTooManyRequests;
	// 0: it didn't say).
	Wait time.Duration
	// Detail is FaultOther's words and FaultUnsafeName's name.
	Detail string
}

// Error says it in English, for the log and the command line.
func (e *ServiceError) Error() string {
	if e.Service == "" {
		return e.Fault.Describe(e.Detail)
	}
	return e.Service + ": " + e.Fault.Describe(e.Detail)
}

// RetryWait is how long to wait before trying again, when waiting may help
// (0: it won't).
func (e *ServiceError) RetryWait() time.Duration {
	switch e.Fault {
	case FaultTooManyRequests:
		if e.Wait > 0 {
			return e.Wait
		}
		return time.Minute
	case FaultHostDown, FaultServiceDown:
		return 5 * time.Minute
	case FaultTooManyActive:
		return 2 * time.Minute
	}
	return 0
}

// Describe says f in English; detail is FaultOther's words and
// FaultUnsafeName's name.
func (f Fault) Describe(detail string) string {
	switch f {
	case FaultSignIn:
		return "sign in again"
	case FaultNeedsApproval:
		return "allow this device first (the service sent an email)"
	case FaultAccountBlocked:
		return "the account is locked or not activated"
	case FaultNotPremium:
		return "this needs a premium account"
	case FaultHostDown:
		return "the file host can't be reached through it right now"
	case FaultLimitReached:
		return "the limit for this file host is used up for now"
	case FaultTooManyActive:
		return "too many downloads at once"
	case FaultIPRefused:
		return "it refuses this connection (a VPN or server address?)"
	case FaultFileUnavailable:
		return "the file is gone or unavailable on the file host"
	case FaultFileRefused:
		return "it won't give this file"
	case FaultPasswordNeeded:
		return "the file is password-protected"
	case FaultTooManyRequests:
		return "too many requests; slowing down"
	case FaultServiceDown:
		return "it isn't responding"
	case FaultTorrentUnavailable:
		return "the torrent can't be downloaded (nobody shares it, or its link can't be read)"
	case FaultUnsafeName:
		return "a file's name can't be written safely: " + detail
	}
	if detail != "" {
		return detail
	}
	return string(f)
}

// errLinkExpired: the service's direct link stopped working mid-download;
// the next attempt asks the service again and keeps the bytes.
var errLinkExpired = errors.New("the service's direct link stopped working")

// expiredStatus lists the answers of a direct link that has died.
var expiredStatus = map[int]bool{
	http.StatusUnauthorized: true, http.StatusForbidden: true, http.StatusNotFound: true, http.StatusGone: true,
}

// unlockerFor is the unlocker for an address, or nil.
func (m *Manager) unlockerFor(rawURL string) Unlocker {
	if m.cfg.Unlockers == nil {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	return m.cfg.Unlockers.UnlockerFor(rawURL)
}

// unlockedVia is what the engine keeps of an unlock: never the link.
type unlockedVia struct {
	service        string
	fileName       string
	maxConnections int
}

// source is where a request for rawURL goes: through its unlocker's direct
// link, without the host's headers, or rawURL itself with them. id names
// the download in the log ("" for a preview).
func (m *Manager) source(ctx context.Context, id, rawURL string, headers http.Header) (string, http.Header, *unlockedVia, error) {
	ul := m.unlockerFor(rawURL)
	if ul == nil {
		return rawURL, headers, nil, nil
	}
	name := ul.Name()
	note := func(s string) { m.cfg.Logger.Info("debrid", "id", id, "service", name, "note", s) }
	got, err := ul.Unlock(ctx, rawURL, note)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, nil, ctx.Err()
		}
		var se *ServiceError
		if !errors.As(err, &se) {
			se = &ServiceError{Fault: FaultOther, Detail: err.Error()}
		}
		if se.Service == "" {
			se = &ServiceError{Service: name, Fault: se.Fault, Wait: se.Wait, Detail: se.Detail}
		}
		return "", nil, nil, se
	}
	if got == nil {
		return rawURL, headers, nil, nil // not a host it takes
	}
	direct, err := url.Parse(got.URL)
	if err != nil || (direct.Scheme != "http" && direct.Scheme != "https") || direct.Host == "" {
		return "", nil, nil, &ServiceError{Service: name, Fault: FaultOther, Detail: "the service gave a link that can't be used"}
	}
	size := "size unknown"
	if got.Size > 0 {
		size = fmt.Sprintf("%d bytes", got.Size)
	}
	m.cfg.Logger.Info("unlocked through a service", "id", id, "service", name, "file", got.FileName, "size", size,
		"server", direct.Hostname())
	return got.URL, http.Header{}, &unlockedVia{service: name, fileName: got.FileName, maxConnections: got.MaxConnections}, nil
}

// theLink stands for a direct link in errors.
const theLink = "(the service's link)"

// hideLink takes the direct links out of err's text: known (the address
// asked) and the one net/http names in a *url.Error (a redirect's hop), so
// neither the saved error nor the log holds them. The error chain stays for
// errors.Is and errors.As.
func hideLink(err error, known string) error {
	if err == nil {
		return nil
	}
	links := []string{known}
	var ue *url.Error
	if errors.As(err, &ue) {
		links = append(links, ue.URL)
	}
	msg := err.Error()
	for _, l := range links {
		if l != "" && l != theLink {
			msg = strings.ReplaceAll(msg, strconv.Quote(l), theLink)
			msg = strings.ReplaceAll(msg, l, theLink)
		}
	}
	if msg == err.Error() {
		return err
	}
	return &hiddenLink{err: err, msg: msg}
}

// hiddenLink is an error whose text holds no direct link.
type hiddenLink struct {
	err error
	msg string
}

func (h *hiddenLink) Error() string { return h.msg }
func (h *hiddenLink) Unwrap() error { return h.err }

// apply marks meta as come through the service: its name for the file when
// the server gave none, and its connection cap.
func (u *unlockedVia) apply(meta *domain.RemoteMetadata) {
	if u == nil {
		return
	}
	meta.Through, meta.MaxConnections = u.service, u.maxConnections
	if meta.Filename == "" {
		meta.Filename = u.fileName
	}
}
