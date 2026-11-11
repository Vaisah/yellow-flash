// Ported from Mister Download Manager, crates/labserver/src/debrid.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package debridtest is a fake Real-Debrid, AllDebrid and Premiumize for
// tests, answering as their documentation says: the same paths (under
// /rd/, /ad/ and /pm/api/), the same JSON shapes, the same error envelopes.
// Nothing here is copied from the services; the shapes are the documented
// ones.
//
//   - Tokens (Authorization: Bearer …): "good" (or "good-…") is a premium
//     account, "free" a free one, and Real-Debrid's device flow's access
//     tokens ("at-…") are premium. Anything else is refused.
//   - The fake file host: http://hoster.test/file/<size>/<name>[?flags]. Its
//     direct link is this server's /gen/<size>/<name>, the same bytes on
//     every unlock of the same link. Flags: gone=1 (the file is
//     unavailable), down=1 (the host is down), chunks=N (Real-Debrid's
//     connection cap), delayed=N (AllDebrid: the link is "being generated"
//     for N polls). Any other host is unsupported; hoster2.test is listed
//     by Real-Debrid and AllDebrid but has no files.
//   - Sign-in by code: Real-Debrid's device code and AllDebrid's PIN are
//     allowed after two polls.
package debridtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Hoster is the fake file host's name: it resolves nowhere (the service
// reaches it, the downloader doesn't).
const Hoster = "hoster.test"

// Request is one request the server saw.
type Request struct {
	Method, Path, Authorization string
}

// Server is the fake services.
type Server struct {
	srv *httptest.Server

	mu      sync.Mutex
	log     []Request
	devices map[string]int // device code → polls
	pins    map[string]int // check → polls
	delayed map[int64]delayedLink
	nextID  int64
	// torrents are what the accounts hold (torrents.go).
	torrents map[string]*torrent
}

type delayedLink struct {
	left int
	link string
}

// Start runs the fake services until the test ends.
func Start(t testing.TB) *Server {
	s := &Server{devices: map[string]int{}, pins: map[string]int{}, delayed: map[int64]delayedLink{},
		torrents: map[string]*torrent{}}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the server's base address, "http://127.0.0.1:PORT/".
func (s *Server) URL() string { return s.srv.URL + "/" }

// Log is every request seen so far.
func (s *Server) Log() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.log...)
}

// Content is the bytes of the fake host's file at link path (as
// "/file/1M/a.bin"), as its direct link serves them.
func Content(linkPath string) []byte {
	parts := strings.Split(strings.Trim(linkPath, "/"), "/")
	if len(parts) != 3 {
		return nil
	}
	n, _ := ParseSize(parts[1])
	return gen(Seed(Hoster+"/file/"+parts[1]+"/"+parts[2]), n)
}

// Seed is the content seed of a fake host's file: FNV-1a of its link
// without flags.
func Seed(link string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(link); i++ {
		h = (h ^ uint64(link[i])) * 0x100000001b3
	}
	return h
}

func gen(seed uint64, n int64) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// ParseSize reads "1M", "512K", "300" as bytes (at most 64 MiB).
func ParseSize(s string) (int64, bool) {
	mult := int64(1)
	if t, ok := strings.CutSuffix(s, "M"); ok {
		mult, s = 1<<20, t
	} else if t, ok := strings.CutSuffix(s, "K"); ok {
		mult, s = 1<<10, t
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > (64<<20)/mult {
		return 0, false
	}
	return n * mult, true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.log = append(s.log, Request{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization")})
	s.mu.Unlock()
	_ = r.ParseForm()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/gen/"):
		serveFile(w, r)
	case strings.HasPrefix(p, "/rd/"):
		s.realDebrid(w, r, strings.TrimPrefix(p, "/rd/"))
	case strings.HasPrefix(p, "/ad/"):
		s.allDebrid(w, r, strings.TrimPrefix(p, "/ad/"))
	case strings.HasPrefix(p, "/pm/api/"):
		s.premiumize(w, r, strings.TrimPrefix(p, "/pm/api/"))
	default:
		http.NotFound(w, r)
	}
}

// serveFile is a direct link: /gen/<size>/<name>?seed=N, with ranges.
func serveFile(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/gen/"), "/")
	n, ok := ParseSize(parts[0])
	seed, err := strconv.ParseUint(r.URL.Query().Get("seed"), 10, 64)
	if len(parts) != 2 || !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Unix(1_700_000_000, 0), bytes.NewReader(gen(seed, n)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// bearer reads the request's token: premium or free, or refused (ok false).
func bearer(r *http.Request, deviceTokens bool) (premium, ok bool) {
	t, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case !found:
		return false, false
	case t == "good" || strings.HasPrefix(t, "good-"):
		return true, true
	case t == "free":
		return false, true
	case deviceTokens && strings.HasPrefix(t, "at-"):
		return true, true
	}
	return false, false
}

// directFor is the fake host's direct link on this server for link: its
// size, name and address, or why not: "unsupported", "gone", "down".
func directFor(r *http.Request, link *url.URL) (size int64, name, direct, why string) {
	if link.Hostname() != Hoster {
		return 0, "", "", "unsupported"
	}
	q := link.Query()
	if q.Has("gone") {
		return 0, "", "", "gone"
	}
	if q.Has("down") {
		return 0, "", "", "down"
	}
	parts := strings.Split(strings.Trim(link.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "file" {
		return 0, "", "", "gone"
	}
	size, _ = ParseSize(parts[1])
	direct = fmt.Sprintf("http://%s/gen/%s/%s?seed=%d", r.Host, parts[1], parts[2],
		Seed(Hoster+"/file/"+parts[1]+"/"+parts[2]))
	return size, parts[2], direct, ""
}

func (s *Server) id() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return time.Now().UnixMilli()%1_000_000_000 + s.nextID
}

/* ---------- Real-Debrid ---------- */

func rdError(w http.ResponseWriter, status int, e string, code int) {
	writeJSON(w, status, map[string]any{"error": e, "error_code": code})
}

func (s *Server) realDebrid(w http.ResponseWriter, r *http.Request, p string) {
	switch p {
	case "rest/1.0/user":
		premium, ok := bearer(r, true)
		if !ok {
			rdError(w, 401, "bad_token", 8)
			return
		}
		kind, left := "free", 0
		if premium {
			kind, left = "premium", 30*86400
		}
		writeJSON(w, 200, map[string]any{"id": 1, "username": "labuser", "email": "lab@example.invalid",
			"type": kind, "premium": left, "expiration": "2027-01-01T00:00:00.000Z"})
	case "rest/1.0/hosts/domains":
		writeJSON(w, 200, []string{Hoster, "hoster2.test"})
	case "rest/1.0/unrestrict/link":
		premium, ok := bearer(r, true)
		if !ok {
			rdError(w, 401, "bad_token", 8)
			return
		}
		if r.PostForm.Get("remote") != "0" {
			rdError(w, 400, "remote must be 0", 2) // always sent as 0
			return
		}
		link, err := url.Parse(r.PostForm.Get("link"))
		if err != nil || link.Host == "" {
			rdError(w, 400, "bad_link", 2)
			return
		}
		if link.Hostname() != Hoster {
			rdError(w, 503, "hoster_unsupported", 16)
			return
		}
		if !premium {
			rdError(w, 403, "hoster_not_free", 20)
			return
		}
		size, name, direct, why := directFor(r, link)
		switch why {
		case "gone":
			rdError(w, 503, "unavailable_file", 24)
			return
		case "down":
			rdError(w, 503, "hoster_unavailable", 19)
			return
		}
		chunks := 16
		if c, err := strconv.Atoi(link.Query().Get("chunks")); err == nil {
			chunks = c
		}
		writeJSON(w, 200, map[string]any{"id": "LAB", "filename": name, "mimeType": "application/octet-stream",
			"filesize": size, "link": link.String(), "host": Hoster, "chunks": chunks, "crc": 1,
			"download": direct, "streamable": 0})
	case "oauth/v2/device/code":
		if r.URL.Query().Get("new_credentials") != "yes" {
			rdError(w, 400, "new_credentials", 2)
			return
		}
		code := fmt.Sprintf("dc-%d", s.id())
		s.mu.Lock()
		s.devices[code] = 0
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"device_code": code, "user_code": "LABCODE1", "interval": 1,
			"expires_in": 120, "verification_url": "https://real-debrid.com/device"})
	case "oauth/v2/device/credentials":
		code := r.URL.Query().Get("code")
		s.mu.Lock()
		n, known := s.devices[code]
		if known {
			s.devices[code] = n + 1
		}
		s.mu.Unlock()
		switch {
		case !known:
			rdError(w, 403, "unknown_code", 3)
		case n+1 <= 2:
			rdError(w, 403, "authorization_pending", 9)
		default:
			writeJSON(w, 200, map[string]any{"client_id": "cid-" + code, "client_secret": "cs-" + code})
		}
	case "oauth/v2/token":
		f := r.PostForm
		if f.Get("grant_type") != "http://oauth.net/grant_type/device/1.0" ||
			!strings.HasPrefix(f.Get("client_id"), "cid-") || !strings.HasPrefix(f.Get("client_secret"), "cs-") {
			rdError(w, 400, "invalid_client", 2)
			return
		}
		if c := f.Get("code"); !strings.HasPrefix(c, "dc-") && !strings.HasPrefix(c, "rt-") {
			rdError(w, 400, "invalid_grant", 2)
			return
		}
		n := s.id()
		writeJSON(w, 200, map[string]any{"access_token": fmt.Sprintf("at-%d", n), "expires_in": 3600,
			"token_type": "Bearer", "refresh_token": fmt.Sprintf("rt-%d", n)})
	default:
		if !s.rdTorrents(w, r, p) {
			rdError(w, 404, "unknown_ressource", 7)
		}
	}
}

/* ---------- AllDebrid ---------- */

func adOK(w http.ResponseWriter, data any) {
	writeJSON(w, 200, map[string]any{"status": "success", "data": data})
}

func adError(w http.ResponseWriter, code string) {
	writeJSON(w, 200, map[string]any{"status": "error", "error": map[string]any{"code": code, "message": "lab: " + code}})
}

func (s *Server) allDebrid(w http.ResponseWriter, r *http.Request, p string) {
	switch p {
	case "v4/user":
		premium, ok := bearer(r, false)
		if !ok {
			adError(w, "AUTH_BAD_APIKEY")
			return
		}
		until := int64(0)
		if premium {
			until = time.Now().Unix() + 30*86400
		}
		adOK(w, map[string]any{"user": map[string]any{"username": "labuser", "email": "lab@example.invalid",
			"isPremium": premium, "isSubscribed": premium, "isTrial": false, "premiumUntil": until}})
	case "v4/hosts/domains":
		adOK(w, map[string]any{"hosts": []string{Hoster, "hoster2.test"}, "streams": []string{}, "redirectors": []string{}})
	case "v4/link/unlock":
		premium, ok := bearer(r, false)
		if !ok {
			adError(w, "AUTH_BAD_APIKEY")
			return
		}
		link, err := url.Parse(r.PostForm.Get("link"))
		if err != nil || link.Host == "" {
			adError(w, "LINK_IS_MISSING")
			return
		}
		if link.Hostname() == Hoster && !premium {
			adError(w, "MUST_BE_PREMIUM")
			return
		}
		size, name, direct, why := directFor(r, link)
		switch why {
		case "unsupported":
			adError(w, "LINK_HOST_NOT_SUPPORTED")
			return
		case "down":
			adError(w, "LINK_HOST_UNAVAILABLE")
			return
		case "gone":
			adError(w, "LINK_DOWN")
			return
		}
		if polls, _ := strconv.Atoi(link.Query().Get("delayed")); polls > 0 {
			id := s.id()
			s.mu.Lock()
			s.delayed[id] = delayedLink{left: polls, link: direct}
			s.mu.Unlock()
			adOK(w, map[string]any{"link": "", "host": "hostertest", "filename": name, "filesize": size,
				"id": "LAB", "hostDomain": Hoster, "delayed": id})
			return
		}
		adOK(w, map[string]any{"link": direct, "host": "hostertest", "filename": name, "paws": false,
			"filesize": size, "streams": []string{}, "id": "LAB", "hostDomain": Hoster})
	case "v4/link/delayed":
		if _, ok := bearer(r, false); !ok {
			adError(w, "AUTH_BAD_APIKEY")
			return
		}
		id, _ := strconv.ParseInt(r.PostForm.Get("id"), 10, 64)
		s.mu.Lock()
		d, known := s.delayed[id]
		if known && d.left > 0 {
			d.left--
			s.delayed[id] = d
		}
		s.mu.Unlock()
		switch {
		case !known:
			adError(w, "DELAYED_INVALID_ID")
		case d.left > 0:
			adOK(w, map[string]any{"status": 1, "time_left": 1})
		default:
			adOK(w, map[string]any{"status": 2, "time_left": 0, "link": d.link})
		}
	case "v4.1/pin/get":
		check := fmt.Sprintf("chk-%d", s.id())
		s.mu.Lock()
		s.pins[check] = 0
		s.mu.Unlock()
		adOK(w, map[string]any{"pin": "LABP", "check": check, "expires_in": 120,
			"user_url": "https://alldebrid.com/pin/?pin=LABP", "base_url": "https://alldebrid.com/pin/"})
	case "v4/pin/check":
		if r.PostForm.Get("pin") != "LABP" {
			adError(w, "PIN_INVALID")
			return
		}
		check := r.PostForm.Get("check")
		s.mu.Lock()
		n, known := s.pins[check]
		if known {
			s.pins[check] = n + 1
		}
		s.mu.Unlock()
		switch {
		case !known:
			adError(w, "PIN_EXPIRED")
		case n+1 <= 2:
			adOK(w, map[string]any{"activated": false, "expires_in": 100})
		default:
			adOK(w, map[string]any{"activated": true, "expires_in": 100, "apikey": "good"})
		}
	default:
		if !s.adTorrents(w, r, p) {
			adError(w, "GENERIC")
		}
	}
}

/* ---------- Premiumize ---------- */

func pmError(w http.ResponseWriter, code string) {
	writeJSON(w, 200, map[string]any{"status": "error", "message": "lab: " + code, "code": code})
}

func (s *Server) premiumize(w http.ResponseWriter, r *http.Request, p string) {
	premium, ok := bearer(r, false)
	if !ok {
		pmError(w, "authentication_failed")
		return
	}
	switch p {
	case "account/info":
		var until any
		if premium {
			until = time.Now().Unix() + 30*86400
		}
		writeJSON(w, 200, map[string]any{"status": "success", "customer_id": "1234567", "premium_until": until,
			"limit_used": 0.1, "booster_points": 0})
	case "services/list":
		writeJSON(w, 200, map[string]any{"status": "success", "cache": []string{}, "directdl": []string{"hostertest"},
			"queue": []string{}, "fairusefactor": map[string]any{"hostertest": 1},
			"aliases": map[string]any{"hostertest": []string{Hoster}}, "regexpatterns": map[string]any{}})
	case "transfer/directdl":
		if !premium {
			pmError(w, "permission_denied")
			return
		}
		link, err := url.Parse(r.PostForm.Get("src"))
		if err != nil || link.Host == "" {
			pmError(w, "not_found")
			return
		}
		size, name, direct, why := directFor(r, link)
		switch why {
		case "unsupported":
			pmError(w, "service_unsupported")
		case "down":
			pmError(w, "service_down")
		case "gone":
			pmError(w, "not_found")
		default:
			writeJSON(w, 200, map[string]any{"status": "success",
				"content": []map[string]any{{"path": name, "size": size, "link": direct}}})
		}
	default:
		if !s.pmTorrents(w, r, p) {
			pmError(w, "not_found")
		}
	}
}
