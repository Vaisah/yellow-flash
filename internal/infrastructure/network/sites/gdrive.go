// Ported from Mister Download Manager, crates/engine/src/sites/gdrive.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package sites

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// Google Drive and Google Docs links. A Drive link is the viewer's address,
// not the file's: the file comes from drive.usercontent.google.com/download,
// after a "can't scan this file for viruses" page for large files; Docs,
// Sheets and Slides come as an export (an Office file). Pages are matched on
// their structure (the form's id, hidden inputs, the uc-error-* classes,
// the address an answer ends at), never on their words, which Google
// translates.

const (
	driveHost       = "drive.google.com"
	docsHost        = "docs.google.com"
	usercontentHost = "drive.usercontent.google.com"
	signInHost      = "accounts.google.com"
	// messageMax is the longest Drive message kept.
	messageMax = 300
)

var driveHosts = []string{driveHost, docsHost, usercontentHost}

// DriveUserAgent is what Drive's hosts are asked with: a current browser's.
// Google's file host answers some non-browser agents with "500 Internal
// Server Error" for every file.
const DriveUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// DriveKind is what a Drive link names.
type DriveKind int

// Drive link kinds.
const (
	DriveFile DriveKind = iota + 1
	DriveDoc
	DriveFolder
)

// DocKind is a Google editors' file, exported as an Office file (Drawings
// as PNG).
type DocKind string

// Doc kinds, by the first path segment of their address.
const (
	Document     DocKind = "document"
	Spreadsheet  DocKind = "spreadsheets"
	Presentation DocKind = "presentation"
	Drawing      DocKind = "drawings"
)

func (d DocKind) format() string {
	switch d {
	case Document:
		return "docx"
	case Spreadsheet:
		return "xlsx"
	case Presentation:
		return "pptx"
	}
	return "png"
}

// DriveLink is a recognised Drive or Docs link.
type DriveLink struct {
	ID          string
	Kind        DriveKind
	Doc         DocKind // DriveDoc only
	ResourceKey string  // resourcekey=: older shared links need it
	AuthUser    string  // which signed-in account (authuser=, or /u/N/)
}

// IsDriveHost reports one of Drive's own hosts (an exact name, never a
// suffix), over http(s).
func IsDriveHost(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return in(strings.ToLower(u.Hostname()), driveHosts...)
}

func hostIs(u *url.URL, host string) bool { return strings.EqualFold(u.Hostname(), host) }

// isID reports Drive's IDs: letters, digits, '_' and '-', 25 or more.
func isID(s string) bool {
	if len(s) < 25 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// query returns the query's first non-empty key.
func query(u *url.URL, key string) string {
	for _, p := range queryPairs(u) {
		if p.k == key && p.v != "" {
			return p.v
		}
	}
	return ""
}

// queryID returns the query's id= when there is exactly one and it is an ID.
func queryID(u *url.URL) string {
	id, n := "", 0
	for _, p := range queryPairs(u) {
		if p.k == "id" {
			id, n = p.v, n+1
		}
	}
	if n == 1 && isID(id) {
		return id
	}
	return ""
}

// account strips a leading u/N (an account number) from segs.
func account(segs []string) (string, []string) {
	if len(segs) >= 2 && segs[0] == "u" && segs[1] != "" && strings.Trim(segs[1], "0123456789") == "" {
		return segs[1], segs[2:]
	}
	return "", segs
}

func in(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// ParseDrive recognises a Drive or Docs link. ok is false when it is not
// one, or is already a file's address (an export, a published copy, the
// browser's own confirmed download link), which is downloaded as it is.
func ParseDrive(u *url.URL) (DriveLink, bool) {
	if !IsDriveHost(u) {
		return DriveLink{}, false
	}
	l, ok := locate(u)
	if !ok {
		return DriveLink{}, false
	}
	l.ResourceKey = query(u, "resourcekey")
	if au := query(u, "authuser"); au != "" {
		l.AuthUser = au
	}
	return l, true
}

func locate(u *url.URL) (DriveLink, bool) {
	segs := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(segs) > 0 && segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	none := DriveLink{}
	// Folders.
	if hostIs(u, driveHost) {
		if len(segs) > 0 && segs[0] == "drive" {
			user, rest := account(segs[1:])
			if len(rest) > 0 && rest[0] == "mobile" {
				rest = rest[1:]
			}
			if len(rest) >= 2 && rest[0] == "folders" && isID(rest[1]) {
				return DriveLink{ID: rest[1], Kind: DriveFolder, AuthUser: user}, true
			}
			return none, false
		}
		if len(segs) == 1 && in(segs[0], "folderview", "embeddedfolderview") {
			if id := queryID(u); id != "" {
				return DriveLink{ID: id, Kind: DriveFolder}, true
			}
			return none, false
		}
	}
	// Docs, Sheets, Slides, Drawings (their export and published addresses
	// are not links).
	if hostIs(u, docsHost) && len(segs) > 0 {
		if doc := DocKind(segs[0]); in(string(doc), string(Document), string(Spreadsheet), string(Presentation), string(Drawing)) {
			user, rest := account(segs[1:])
			ok := len(rest) == 2 && rest[0] == "d" ||
				len(rest) == 3 && rest[0] == "d" && in(rest[2], "edit", "view", "htmlview", "preview", "copy", "mobilebasic")
			if ok && isID(rest[1]) {
				return DriveLink{ID: rest[1], Kind: DriveDoc, Doc: doc, AuthUser: user}, true
			}
			return none, false
		}
	}
	// A file by its path (the viewer).
	if (hostIs(u, driveHost) || hostIs(u, docsHost)) && len(segs) > 0 && segs[0] == "file" {
		user, rest := account(segs[1:])
		ok := len(rest) == 2 && rest[0] == "d" ||
			len(rest) == 3 && rest[0] == "d" && in(rest[2], "view", "edit", "preview")
		if ok && isID(rest[1]) {
			return DriveLink{ID: rest[1], Kind: DriveFile, AuthUser: user}, true
		}
		return none, false
	}
	// A file by its query: uc, open, download.
	if len(segs) == 1 && in(segs[0], "uc", "open", "download") {
		// The browser's own link after "Download anyway": the file's address already.
		if hostIs(u, usercontentHost) {
			for _, p := range queryPairs(u) {
				if p.k == "confirm" {
					return none, false
				}
			}
		}
		if id := queryID(u); id != "" {
			return DriveLink{ID: id, Kind: DriveFile}, true
		}
	}
	return none, false
}

func driveNotAFile(u *url.URL) *Refusal {
	if l, ok := ParseDrive(u); ok && l.Kind == DriveFolder {
		return &Refusal{Kind: Folder}
	}
	return nil
}

// on returns u with another host and path, its scheme and port kept, and
// the query pairs.
func on(u *url.URL, host, path string, pairs []pair) *url.URL {
	next := *u
	next.User = nil
	next.Host = host
	if port := u.Port(); port != "" {
		next.Host = host + ":" + port
	}
	next.Path, next.RawPath = path, ""
	next.Fragment, next.RawFragment = "", ""
	next.RawQuery, next.ForceQuery = encodePairs(pairs), false
	return &next
}

// driveRewrite gives the address the file behind a Drive or Docs link comes
// from:
//   - a file: drive.usercontent.google.com/download?id=ID&export=download&confirm=t,
//     the confirmation given up front, so a large file skips the virus-scan
//     page's round trip (the page's form is still followed if Google ever
//     wants more);
//   - a Doc: its export (/document/d/ID/export?format=docx; Drawings
//     …/export/png).
//
// A folder is not a file.
func driveRewrite(u *url.URL) (*url.URL, bool) {
	l, ok := ParseDrive(u)
	if !ok {
		return nil, false
	}
	var key []pair
	if l.ResourceKey != "" {
		key = []pair{{"resourcekey", l.ResourceKey}}
	}
	var next *url.URL
	switch {
	case l.Kind == DriveFile:
		pairs := append([]pair{{"id", l.ID}, {"export", "download"}, {"confirm", "t"}}, key...)
		if l.AuthUser != "" {
			pairs = append(pairs, pair{"authuser", l.AuthUser})
		}
		next = on(u, usercontentHost, "/download", pairs)
	case l.Kind == DriveDoc && l.Doc == Drawing:
		next = on(u, docsHost, "/drawings/d/"+l.ID+"/export/"+Drawing.format(), key)
	case l.Kind == DriveDoc:
		next = on(u, docsHost, "/"+string(l.Doc)+"/d/"+l.ID+"/export", append([]pair{{"format", l.Doc.format()}}, key...))
	default:
		return nil, false
	}
	if next.String() == u.String() {
		return nil, false
	}
	return next, true
}

// fileID returns the file ID of Drive's file address
// (drive.usercontent.google.com/download?id=…, confirmed or not), or of a
// file link, with its resource key.
func fileID(u *url.URL) (id, key string, ok bool) {
	if hostIs(u, usercontentHost) && (u.Path == "/download" || u.Path == "/uc") {
		if id := queryID(u); id != "" {
			return id, query(u, "resourcekey"), true
		}
	}
	if l, ok := ParseDrive(u); ok && l.Kind == DriveFile {
		return l.ID, l.ResourceKey, true
	}
	return "", "", false
}

// driveSecondLook: before Drive's error page for a file is reported,
// drive.google.com/open?id=ID, which leads a Google Doc to its editor
// (Drive's file host answers a Doc's ID with its quota page) and a file
// back to its viewer.
func driveSecondLook(at *url.URL) (*url.URL, bool) {
	id, key, ok := fileID(at)
	if !ok {
		return nil, false
	}
	pairs := []pair{{"id", id}}
	if key != "" {
		pairs = append(pairs, pair{"resourcekey", key})
	}
	return on(at, driveHost, "/open", pairs), true
}

func driveExpectsFile(at *url.URL) bool {
	if _, _, ok := fileID(at); ok {
		return true
	}
	l, ok := ParseDrive(at)
	return ok && l.Kind != DriveFolder
}

// driveReadPage reads Drive's page answered 200 at at, trying in order:
//  1. the virus-scan warning's <form id="download-form">: its action (on a
//     Drive host only) with every hidden input set on its query;
//  2. an older page's href="/uc?export=download&…confirm=…", on
//     docs.google.com;
//  3. a "downloadUrl":"https:…" in the page's data (Drive's or
//     *.googleusercontent.com);
//  4. an error page (uc-error-subcaption, else uc-error-caption): a refusal
//     with Google's words;
//  5. nothing known.
func driveReadPage(at *url.URL, page string) Page {
	next := formTarget(at, page)
	if next == nil {
		next = oldLink(at, page)
	}
	if next == nil {
		next = dataLink(page)
	}
	if next != nil {
		return Page{Action: Next, Next: next}
	}
	msg, ok := textOfClass(page, "uc-error-subcaption")
	if !ok {
		msg, ok = textOfClass(page, "uc-error-caption")
	}
	if ok {
		return Page{Action: Refused, Refusal: &Refusal{Kind: DriveMessage, Message: shorten(msg)}}
	}
	return Page{}
}

func shorten(s string) string {
	if utf8.RuneCountInString(s) <= messageMax {
		return s
	}
	return strings.TrimRight(string([]rune(s)[:messageMax]), " \t\n") + "…"
}

// formTarget fills in the virus-scan page's form (rule 1).
func formTarget(at *url.URL, page string) *url.URL {
	var form *tag
	for _, t := range tags(page, "form") {
		if id, ok := attribute(t.text, "id"); ok && decode(id) == "download-form" {
			form = &t
			break
		}
	}
	if form == nil {
		return nil
	}
	action, _ := attribute(form.text, "action")
	next, err := at.Parse(strings.TrimSpace(decode(action)))
	if err != nil || !IsDriveHost(next) { // never a form off Google's Drive
		return nil
	}
	next.Fragment, next.RawFragment = "", ""
	body := form.start + len(form.text)
	end := len(page)
	if e := strings.Index(asciiLower(page[body:]), "</form"); e >= 0 {
		end = body + e
	}
	pairs := queryPairs(next)
	for _, input := range tags(page[body:end], "input") {
		typ, _ := attribute(input.text, "type")
		name, _ := attribute(input.text, "name")
		name = decode(name)
		if !strings.EqualFold(typ, "hidden") || name == "" {
			continue
		}
		value, _ := attribute(input.text, "value")
		value = decode(value)
		found := -1
		for i, p := range pairs {
			if p.k == name {
				found = i
				break
			}
		}
		if found < 0 {
			pairs = append(pairs, pair{name, value})
			continue
		}
		pairs[found].v = value
		kept := pairs[:0]
		for i, p := range pairs {
			if p.k != name || i == found {
				kept = append(kept, p)
			}
		}
		pairs = kept
	}
	next.RawQuery = encodePairs(pairs)
	// A form that only asks for its own page again would loop.
	if sameAddress(next, at) {
		return nil
	}
	return next
}

// oldLink finds an older page's confirmed link (rule 2), on docs.google.com
// with at's scheme and port.
func oldLink(at *url.URL, page string) *url.URL {
	const href = `href="/uc?export=download`
	for rest := page; ; {
		i := strings.Index(rest, href)
		if i < 0 {
			return nil
		}
		value := rest[i+len(`href="`):]
		rest = rest[i+len(href):]
		close := strings.IndexByte(value, '"')
		if close < 0 {
			return nil
		}
		if v := decode(value[:close]); strings.Contains(v, "confirm=") {
			next, err := on(at, docsHost, "/", nil).Parse(v)
			if err != nil {
				return nil
			}
			return next
		}
	}
}

// dataLink finds a "downloadUrl":"https:…" in the page's data (rule 3),
// JSON-escaped.
func dataLink(page string) *url.URL {
	const key = `"downloadUrl":"`
	i := strings.Index(page, key)
	if i < 0 {
		return nil
	}
	raw := page[i+len(key):]
	end := strings.IndexByte(raw, '"')
	if end < 0 {
		return nil
	}
	s := strings.NewReplacer(`=`, "=", `=`, "=", `&`, "&", `\/`, "/").Replace(raw[:end])
	if !strings.HasPrefix(s, "https:") {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil
	}
	if IsDriveHost(u) || strings.HasSuffix(strings.ToLower(u.Hostname()), ".googleusercontent.com") {
		return u
	}
	return nil
}

// driveRefused reports Drive's refusal by where an answer ended:
//   - a Drive address that ended at Google's sign-in page: the file is not
//     shared with everyone;
//   - 401 or 403 from a Drive host: this account has no access.
func driveRefused(asked, at *url.URL, status int) *Refusal {
	if IsDriveHost(asked) && hostIs(at, signInHost) {
		return &Refusal{Kind: SignIn}
	}
	if IsDriveHost(at) && (status == 401 || status == 403) {
		return &Refusal{Kind: NoAccess}
	}
	return nil
}

// driveSameParty: two of Drive's hosts on the same port are one party. The
// browser sends its .google.com sign-in cookies to all three.
// accounts.google.com and *.googleusercontent.com are never one party with
// them.
func driveSameParty(a, b *url.URL) bool {
	return IsDriveHost(a) && IsDriveHost(b) && portOrDefault(a) == portOrDefault(b)
}

func portOrDefault(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
