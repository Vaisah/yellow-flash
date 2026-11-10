// Ported from Mister Download Manager, crates/remote/src/http.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package remote

// The remote control's web server: the page (web/, built in) and its API.
// Every API request but pairing and the page's texts needs a paired
// device's token (Authorization: Bearer …); every request must name this
// server (Host) and come from its own page (Origin), and every answer
// forbids framing, sniffing and caching.

import (
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/vaisah/internal/ipc"
)

const (
	// maxTorrent is the largest .torrent file taken (the app's own limit,
	// torrent.MaxTorrentFile).
	maxTorrent = 10 << 20
	// maxBody is the largest other request body.
	maxBody = 64 << 10
	// maxURL is the longest address taken.
	maxURL = 8192
	// answerWait is how long the app gets to answer.
	answerWait = 5 * time.Second
)

//go:embed web
var web embed.FS

// server serves one listener; generation is the Remote's when it started.
type server struct {
	r          *Remote
	hosts      []string
	generation uint64
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	for path, kind := range map[string]string{
		"/app.js": "text/javascript; charset=utf-8", "/app.css": "text/css; charset=utf-8",
		"/icon.png": "image/png", "/manifest.webmanifest": "application/manifest+json",
	} {
		mux.HandleFunc("GET "+path, asset("web"+path, kind))
	}
	mux.HandleFunc("GET /{$}", asset("web/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /api/texts", s.texts)
	mux.HandleFunc("POST /api/pair", s.pair)
	mux.HandleFunc("GET /api/list", s.paired(s.list))
	mux.HandleFunc("POST /api/add", s.paired(s.add))
	mux.HandleFunc("POST /api/torrent", s.paired(s.torrent))
	mux.HandleFunc("POST /api/act", s.paired(s.act))
	mux.HandleFunc("POST /api/queue", s.paired(s.queue))
	mux.HandleFunc("POST /api/forget", s.paired(s.forget))
	return s.guard(mux)
}

func asset(name, kind string) http.HandlerFunc {
	b, err := web.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", kind)
		_, _ = w.Write(b)
	}
}

// fail answers an error, shaped as the automation API's.
func fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, ipc.Line(ipc.ErrorAnswer{Error: strings.ReplaceAll(code, "-", " "), Code: code}))
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// guard checks every request before it is handled, and hardens every
// answer.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// A connection opened before the remote control was turned off or
		// moved.
		if s.r.generationNow() != s.generation {
			fail(w, http.StatusServiceUnavailable, "stopped")
			return
		}
		// DNS rebinding: a web page's own name pointed at this computer.
		if !HostAllowed(HostOf(req.Host), s.hosts) {
			fail(w, http.StatusMisdirectedRequest, "unknown-host")
			return
		}
		// Another site's page sending requests here (its scripts can't read
		// the token, but none of its requests are taken either). The page
		// itself may be opened from a link anywhere.
		if strings.HasPrefix(req.URL.Path, "/api/") && !sameOrigin(req.Header, req.Host) {
			fail(w, http.StatusForbidden, "other-origin")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// sameOrigin: no Origin, or this server's own (https:// and the Host asked
// for), or Yellow Flash's browser extension's; and not a request the
// browser marks as sent from another site.
func sameOrigin(h http.Header, host string) bool {
	if strings.EqualFold(h.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := h.Get("Origin")
	for _, p := range []string{"chrome-extension://", "moz-extension://", "safari-web-extension://"} {
		if strings.HasPrefix(origin, p) {
			return true
		}
	}
	return origin == "" || strings.EqualFold(origin, "https://"+host)
}

// paired lets only a paired device's requests through (its id passed on).
func (s *server) paired(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		token = strings.TrimSpace(token)
		if !ok || token == "" || len(token) > 128 {
			fail(w, http.StatusUnauthorized, "not-paired")
			return
		}
		d, ok := s.r.devices.Check(token)
		if !ok {
			fail(w, http.StatusUnauthorized, "not-paired")
			return
		}
		next(w, req, d.ID)
	}
}

// ask hands cmd (marked remote) to the app and answers with what it says.
func (s *server) ask(w http.ResponseWriter, cmd ipc.Command) {
	cmd.Remote = true
	line, ok := s.r.sink.Ask(cmd, answerWait)
	if !ok {
		fail(w, http.StatusServiceUnavailable, "no-answer")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, line)
}

func readJSON(w http.ResponseWriter, req *http.Request, v any) bool {
	req.Body = http.MaxBytesReader(w, req.Body, maxBody)
	if err := json.NewDecoder(req.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad-request")
		return false
	}
	return true
}

func (s *server) texts(w http.ResponseWriter, req *http.Request) {
	lang := req.URL.Query().Get("lang")
	if r := []rune(lang); len(r) > 200 {
		lang = string(r[:200])
	}
	t := PageTexts{Lang: "en", Texts: map[string]string{}}
	if s.r.texts != nil {
		t = s.r.texts(lang)
	}
	answer(w, http.StatusOK, t)
}

func (s *server) pair(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !readJSON(w, req, &b) {
		return
	}
	peer, err := netip.ParseAddrPort(req.RemoteAddr)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad-request")
		return
	}
	switch result, left := s.r.pairing.Check(b.Code, peer.Addr()); result {
	case Right:
		d, token := s.r.devices.Add(b.Name)
		s.r.pairing.Done(d.Name)
		s.r.logf("remote control: %s paired (%s)", d.Name, peer.Addr())
		s.r.changed()
		answer(w, http.StatusOK, map[string]string{"token": token, "id": d.ID, "name": d.Name})
	case Wrong:
		s.r.logf("remote control: a wrong pairing code (%s)", peer.Addr())
		answer(w, http.StatusForbidden, map[string]any{"error": "wrong code", "code": "wrong-code", "left": left})
	case NoCode:
		fail(w, http.StatusGone, "no-code")
	default:
		fail(w, http.StatusTooManyRequests, "too-soon")
	}
}

func (s *server) list(w http.ResponseWriter, _ *http.Request, _ string) {
	s.ask(w, ipc.Command{Query: "list"})
}

func (s *server) add(w http.ResponseWriter, req *http.Request, _ string) {
	var b struct {
		URL      string `json:"url"`
		Category string `json:"category"`
		Queue    string `json:"queue"`
		// Start now (else it waits in Queue, the main queue by default).
		Start bool `json:"start"`
	}
	if !readJSON(w, req, &b) {
		return
	}
	url := strings.TrimSpace(b.URL)
	if url == "" || len(url) > maxURL {
		fail(w, http.StatusBadRequest, "bad-address")
		return
	}
	s.ask(w, ipc.Command{URL: url, Category: b.Category, Queue: b.Queue, Later: !b.Start, Silent: true})
}

func (s *server) torrent(w http.ResponseWriter, req *http.Request, _ string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxTorrent))
	// A .torrent file is a bencoded dictionary.
	if err != nil || len(body) == 0 || body[0] != 'd' {
		fail(w, http.StatusBadRequest, "not-a-torrent")
		return
	}
	q := req.URL.Query()
	s.ask(w, ipc.Command{TorrentFile: body, Category: q.Get("category"), Queue: q.Get("queue"),
		Later: q.Get("start") != "true", Silent: true})
}

func (s *server) act(w http.ResponseWriter, req *http.Request, _ string) {
	var b struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if !readJSON(w, req, &b) {
		return
	}
	switch b.Action {
	case "pause", "start", "remove", "delete":
	default:
		fail(w, http.StatusBadRequest, "bad-action")
		return
	}
	if len(b.IDs) > 10_000 {
		fail(w, http.StatusBadRequest, "bad-action")
		return
	}
	s.ask(w, ipc.Command{Action: b.Action, IDs: b.IDs})
}

func (s *server) queue(w http.ResponseWriter, req *http.Request, _ string) {
	var b struct {
		Name  string `json:"name"`
		Start bool   `json:"start"`
	}
	if !readJSON(w, req, &b) {
		return
	}
	action := "stop_queue"
	if b.Start {
		action = "start_queue"
	}
	s.ask(w, ipc.Command{Action: action, Queue: b.Name})
}

// forget: the device unpairs itself (the page's Forget this device).
func (s *server) forget(w http.ResponseWriter, _ *http.Request, id string) {
	s.r.devices.Remove(id)
	s.r.changed()
	w.WriteHeader(http.StatusNoContent)
}
