// Ported from Mister Download Manager, crates/engine/src/sites/mod.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package sites knows sites whose links are not the file's own address: a
// Google Drive link leads to Drive's viewer, not to the file. The network
// prober asks these hooks on its way to the file; everything a site knows
// stays in its own file, so the engine stays generic and a later site is one
// more file here. Everything here is pure functions over addresses and page
// text.
package sites

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// RefusalKind says why a site refuses to give the file.
type RefusalKind int

// Refusal kinds.
const (
	// DriveMessage is Google Drive's own error page (most often: too many
	// downloads of this file lately), with Google's words.
	DriveMessage RefusalKind = iota + 1
	// SignIn: the file is not shared with everyone.
	SignIn
	// NoAccess: signed in, but this account has no access (or the owner
	// turned downloading off).
	NoAccess
	// Folder: a Drive folder lists files; it is not one.
	Folder
)

// Refusal is a site's own refusal to give the file. No retry helps now.
type Refusal struct {
	Kind    RefusalKind
	Message string // DriveMessage only
}

func (r *Refusal) Error() string {
	switch r.Kind {
	case DriveMessage:
		return "Google Drive won't give this file now: " + r.Message
	case SignIn:
		return "this Google Drive file needs you to sign in: sign in to Google in your browser, then download it from there"
	case NoAccess:
		return "your Google account has no access to this file, or its owner turned off downloading"
	case Folder:
		return "this is a Google Drive folder, not a file"
	}
	return fmt.Sprintf("the site refused the file (%d)", r.Kind)
}

// PageAction is what a site's web page says about the file.
type PageAction int

// Page actions.
const (
	// Nothing the site's rules know: read the page generically.
	Nothing PageAction = iota
	// Next: ask Page.Next next (a confirmation form's target).
	Next
	// Refused: the site refuses (Page.Refusal).
	Refused
)

// Page is ReadPage's answer.
type Page struct {
	Action  PageAction
	Next    *url.URL
	Refusal *Refusal
}

// PagePeek is how much of a web page is read to find where it leads.
const PagePeek = 256 << 10

// MaxPageHops is the most pages in a row followed on the way to a file.
const MaxPageHops = 5

// NotAFile refuses a link that names something that is not a file (a Drive
// folder) before anything is asked.
func NotAFile(u *url.URL) *Refusal { return driveNotAFile(u) }

// Rewrite returns the address to ask instead of u when u is a site's link
// to a file rather than the file (a Drive viewer link → its download
// address).
func Rewrite(u *url.URL) (*url.URL, bool) { return driveRewrite(u) }

// IsSiteHost reports a host whose pages ReadPage knows.
func IsSiteHost(u *url.URL) bool { return IsDriveHost(u) }

// ReadPage reads a web page answered 200 at at (its first PagePeek bytes).
func ReadPage(at *url.URL, page []byte) Page {
	if !IsDriveHost(at) {
		return Page{}
	}
	return driveReadPage(at, string(page))
}

// RefusedBy reports an answer that is a site's refusal by where it ended (at,
// asked as asked) and its status.
func RefusedBy(asked, at *url.URL, status int) *Refusal { return driveRefused(asked, at, status) }

// SameParty reports that a and b are one party's hosts (Drive's three): the
// user's credentials given for one go to the other, as the browser's own
// .google.com cookies do.
func SameParty(a, b *url.URL) bool { return driveSameParty(a, b) }

// UserAgent is the User-Agent a site's hosts want instead of the default
// ("" for the download's own).
func UserAgent(u *url.URL) string {
	if IsDriveHost(u) {
		return DriveUserAgent
	}
	return ""
}

// SecondLook is where to ask once more before a site's refusal at at is
// reported (Drive: whether the ID is a Google Doc).
func SecondLook(at *url.URL) (*url.URL, bool) { return driveSecondLook(at) }

// ExpectsFile reports that at is a site's address for a file: a web page from
// it is the site talking, never the file.
func ExpectsFile(at *url.URL) bool { return driveExpectsFile(at) }

/* ---------- ordered queries ---------- */

type pair struct{ k, v string }

// queryPairs returns u's query parameters in order, decoded.
func queryPairs(u *url.URL) []pair {
	var out []pair
	for _, part := range strings.Split(u.RawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		k, err1 := url.QueryUnescape(k)
		v, err2 := url.QueryUnescape(v)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, pair{k, v})
	}
	return out
}

// encodePairs encodes pairs in order, form-encoded.
func encodePairs(pairs []pair) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, url.QueryEscape(p.k)+"="+url.QueryEscape(p.v))
	}
	return strings.Join(parts, "&")
}

// sameAddress reports a and b are the same address, whatever the order of
// their query's parameters.
func sameAddress(a, b *url.URL) bool {
	base := func(u *url.URL) string {
		c := *u
		c.RawQuery, c.Fragment, c.RawFragment = "", "", ""
		return c.String()
	}
	sorted := func(u *url.URL) []pair {
		p := queryPairs(u)
		sort.Slice(p, func(i, j int) bool {
			if p[i].k != p[j].k {
				return p[i].k < p[j].k
			}
			return p[i].v < p[j].v
		})
		return p
	}
	if base(a) != base(b) {
		return false
	}
	pa, pb := sorted(a), sorted(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return false
		}
	}
	return true
}
