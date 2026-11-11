// Ported from Mister Download Manager, crates/labserver/src/debrid_torrents.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debridtest

// Fake torrents at the three fake services: a magnet link sent to the
// account is "fetched" over a few polls, then its files are there, each a
// link the service hands back. Answers as each service's public API
// documentation shows them (paths, JSON shapes, status words and codes);
// nothing is copied from the services.
//
//   - The torrent's name is the magnet's dn (else "torrent"). It holds two
//     files: <name>/video.mkv (2 MB) and <name>/extras/notes.txt (64 KB).
//   - Its info hash (xt=urn:btih:…) says how it goes: "dead…" dies at the
//     service; "bad…" adds a file whose path climbs out of the folder
//     (../../escape.bin); anything else is ready after a few status polls.
//   - Real-Debrid's and AllDebrid's file links are the fake host's
//     (http://hoster.test/file/…), unlocked as any link; Premiumize's are
//     this server's own direct links (/gen/…).
//   - Held lists what the accounts still hold (a test checks the cleanup).

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type torrent struct {
	service  string
	name     string
	hash     string
	polls    int
	selected bool
}

type torrentFile struct {
	path  string
	size  string
	bytes int64
}

// files is a torrent's files.
func (t *torrent) files() []torrentFile {
	v := []torrentFile{{t.name + "/video.mkv", "2M", 2 << 20}, {t.name + "/extras/notes.txt", "64K", 64 << 10}}
	if strings.HasPrefix(t.hash, "bad") {
		v = append(v, torrentFile{"../../escape.bin", "1K", 1 << 10})
	}
	return v
}

func (f torrentFile) name() string { return f.path[strings.LastIndex(f.path, "/")+1:] }

// hostLink is the fake host's link for a file: the same bytes on every unlock.
func (f torrentFile) hostLink() string {
	return fmt.Sprintf("http://%s/file/%s/%s", Hoster, f.size, f.name())
}

// directLink is this server's direct link for a file (Premiumize's cloud),
// the bytes the fake host's would be.
func (f torrentFile) directLink(r *http.Request) string {
	return fmt.Sprintf("http://%s/gen/%s/%s?seed=%d", r.Host, f.size, f.name(),
		Seed(Hoster+"/file/"+f.size+"/"+f.name()))
}

// Held is the ids the fake accounts still hold.
func (s *Server) Held() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for id := range s.torrents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// addTorrent takes a magnet link: its id at service, name and hash.
func (s *Server) addTorrent(service, magnet string) (id, name, hash string, ok bool) {
	u, err := url.Parse(magnet)
	if err != nil || u.Scheme != "magnet" {
		return "", "", "", false
	}
	q := u.Query()
	hash, found := strings.CutPrefix(q.Get("xt"), "urn:btih:")
	if !found || hash == "" {
		return "", "", "", false
	}
	if name = q.Get("dn"); name == "" {
		name = "torrent"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	switch service {
	case "rd":
		id = fmt.Sprintf("RDT%d", s.nextID)
	case "ad":
		id = strconv.FormatInt(s.nextID, 10)
	default:
		id = fmt.Sprintf("pmt%d", s.nextID)
	}
	s.torrents[id] = &torrent{service: service, name: name, hash: strings.ToLower(hash)}
	return id, name, hash, true
}

// pollTorrent counts one poll: the torrent (a copy) with its polls so far.
func (s *Server) pollTorrent(id, service string) (torrent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.torrents[id]
	if t == nil || t.service != service {
		return torrent{}, false
	}
	t.polls++
	return *t, true
}

func (s *Server) removeTorrent(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.torrents[id] == nil {
		return false
	}
	delete(s.torrents, id)
	return true
}

// filesOf is a held torrent's files at service (nil: none such).
func (s *Server) filesOf(id, service string) []torrentFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.torrents[id]; t != nil && (service == "" || t.service == service) {
		return t.files()
	}
	return nil
}

func tokenOK(r *http.Request) bool {
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && (t == "good" || strings.HasPrefix(t, "good-") || strings.HasPrefix(t, "at-"))
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

/* ---------- Real-Debrid ---------- */

func (s *Server) rdTorrents(w http.ResponseWriter, r *http.Request, p string) bool {
	rest, ok := strings.CutPrefix(p, "rest/1.0/torrents/")
	if !ok {
		return false
	}
	if !tokenOK(r) {
		rdError(w, 401, "bad_token", 8)
		return true
	}
	switch {
	case rest == "addMagnet" && r.Method == http.MethodPost:
		id, _, _, ok := s.addTorrent("rd", r.PostForm.Get("magnet"))
		if !ok {
			rdError(w, 400, "wrong_parameter", 2)
			return true
		}
		writeJSON(w, 201, map[string]any{"id": id, "uri": "https://api.real-debrid.com/rest/1.0/torrents/info/" + id})
	case strings.HasPrefix(rest, "info/"):
		id := strings.TrimPrefix(rest, "info/")
		t, ok := s.pollTorrent(id, "rd")
		if !ok {
			rdError(w, 404, "unknown_ressource", 7)
			return true
		}
		status, progress := "downloaded", 100
		switch {
		case strings.HasPrefix(t.hash, "dead"):
			status, progress = "dead", 0
		case t.polls == 1:
			status, progress = "magnet_conversion", 0
		case !t.selected:
			status, progress = "waiting_files_selection", 0
		case t.polls < 4:
			status, progress = "downloading", 50
		}
		list, links := []any{}, []string{}
		var total int64
		sel := 0
		if t.selected {
			sel = 1
		}
		for i, f := range t.files() {
			list = append(list, map[string]any{"id": i + 1, "path": "/" + f.path, "bytes": f.bytes, "selected": sel})
			if status == "downloaded" {
				links = append(links, f.hostLink())
			}
			total += f.bytes
		}
		writeJSON(w, 200, map[string]any{"id": id, "filename": t.name, "original_filename": t.name, "hash": t.hash,
			"bytes": total, "original_bytes": total, "host": "real-debrid.com", "split": 2000, "progress": progress,
			"status": status, "added": "2026-11-06T00:00:00.000Z", "files": list, "links": links})
	case strings.HasPrefix(rest, "selectFiles/") && r.Method == http.MethodPost:
		if r.PostForm.Get("files") != "all" {
			rdError(w, 400, "wrong_parameter", 2)
			return true
		}
		s.mu.Lock()
		t := s.torrents[strings.TrimPrefix(rest, "selectFiles/")]
		if t != nil {
			t.selected = true
		}
		s.mu.Unlock()
		if t == nil {
			rdError(w, 404, "unknown_ressource", 7)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(rest, "delete/") && r.Method == http.MethodDelete:
		if !s.removeTorrent(strings.TrimPrefix(rest, "delete/")) {
			rdError(w, 404, "unknown_ressource", 7)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

/* ---------- AllDebrid ---------- */

func (s *Server) adTorrents(w http.ResponseWriter, r *http.Request, p string) bool {
	switch p {
	case "v4/magnet/upload", "v4.1/magnet/status", "v4/magnet/files", "v4/magnet/delete":
	default:
		return false
	}
	if !tokenOK(r) {
		adError(w, "AUTH_BAD_APIKEY")
		return true
	}
	const size = (2 << 20) + (64 << 10)
	switch p {
	case "v4/magnet/upload":
		m := r.PostForm.Get("magnets[]")
		if m == "" {
			adError(w, "MAGNET_NO_URI")
			return true
		}
		id, name, hash, ok := s.addTorrent("ad", m)
		if !ok {
			adOK(w, map[string]any{"magnets": []any{map[string]any{"magnet": m,
				"error": map[string]any{"code": "MAGNET_INVALID_URI", "message": "Magnet is not valid"}}}})
			return true
		}
		adOK(w, map[string]any{"magnets": []any{map[string]any{"magnet": m, "hash": hash, "name": name,
			"size": size, "ready": false, "id": atoi(id)}}})
	case "v4.1/magnet/status":
		id := r.PostForm.Get("id")
		t, ok := s.pollTorrent(id, "ad")
		if !ok {
			adError(w, "MAGNET_INVALID_ID")
			return true
		}
		status, code, done := "Ready", 4, int64(size)
		switch {
		case strings.HasPrefix(t.hash, "dead"):
			status, code, done = "File not available - no peer", 15, 0
		case t.polls < 3:
			status, code, done = "Downloading", 1, size/2
		}
		adOK(w, map[string]any{"magnets": []any{map[string]any{"id": atoi(id), "filename": t.name, "size": size,
			"status": status, "statusCode": code, "downloaded": done, "uploaded": 0, "seeders": 3}}})
	case "v4/magnet/files":
		id := r.PostForm.Get("id[]")
		files := s.filesOf(id, "ad")
		if files == nil {
			adOK(w, map[string]any{"magnets": []any{map[string]any{"id": id,
				"error": map[string]any{"code": "MAGNET_INVALID_ID", "message": "lab"}}}})
			return true
		}
		adOK(w, map[string]any{"magnets": []any{map[string]any{"id": atoi(id), "files": adTree(files)}}})
	case "v4/magnet/delete":
		if !s.removeTorrent(r.PostForm.Get("id")) {
			adError(w, "MAGNET_INVALID_ID")
			return true
		}
		adOK(w, map[string]any{"message": "Magnet was successfully deleted"})
	}
	return true
}

// adNode is AllDebrid's tree: a folder {n, e} or a file {n, s, l}.
type adNode struct {
	N string    `json:"n"`
	S int64     `json:"s,omitempty"`
	L string    `json:"l,omitempty"`
	E []*adNode `json:"e,omitempty"`
}

// adTree is the files as AllDebrid's tree: the torrent's folder, its files
// and its "extras" folder.
func adTree(files []torrentFile) []*adNode {
	root := &adNode{}
	for _, f := range files {
		parts := strings.Split(f.path, "/")
		level := root
		for _, dir := range parts[:len(parts)-1] {
			var next *adNode
			for _, n := range level.E {
				if n.N == dir && n.L == "" {
					next = n
				}
			}
			if next == nil {
				next = &adNode{N: dir}
				level.E = append(level.E, next)
			}
			level = next
		}
		level.E = append(level.E, &adNode{N: parts[len(parts)-1], S: f.bytes, L: f.hostLink()})
	}
	return root.E
}

/* ---------- Premiumize ---------- */

func (s *Server) pmTorrents(w http.ResponseWriter, r *http.Request, p string) bool {
	switch p {
	case "transfer/create":
		id, name, _, ok := s.addTorrent("pm", r.PostForm.Get("src"))
		if !ok {
			pmError(w, "invalid_request")
			return true
		}
		writeJSON(w, 200, map[string]any{"status": "success", "id": id, "name": name, "type": "torrent"})
	case "transfer/list":
		s.mu.Lock()
		var ids []string
		for id, t := range s.torrents {
			if t.service == "pm" {
				ids = append(ids, id)
			}
		}
		s.mu.Unlock()
		sort.Strings(ids)
		transfers := []any{}
		for _, id := range ids {
			t, ok := s.pollTorrent(id, "pm")
			if !ok {
				continue
			}
			status, progress, message := "finished", 1.0, ""
			var folder any = "f-" + id
			switch {
			case strings.HasPrefix(t.hash, "dead"):
				status, progress, message, folder = "error", 0, "lab: no peers", nil
			case t.polls < 3:
				status, progress, folder = "running", 0.5, nil
			}
			transfers = append(transfers, map[string]any{"id": id, "name": t.name, "message": message, "status": status,
				"progress": progress, "folder_id": folder, "file_id": nil, "created_at": 0})
		}
		writeJSON(w, 200, map[string]any{"status": "success", "transfers": transfers})
	case "folder/list":
		// f-<transfer> is the torrent's folder, f-<transfer>-x its "extras".
		fid := r.URL.Query().Get("id")
		rest, ok := strings.CutPrefix(fid, "f-")
		if !ok {
			pmError(w, "not_found")
			return true
		}
		tid, extras := strings.CutSuffix(rest, "-x")
		files := s.filesOf(tid, "")
		if files == nil {
			pmError(w, "not_found")
			return true
		}
		content := []any{}
		for _, f := range files {
			if strings.Contains(f.path, "/extras/") == extras {
				content = append(content, map[string]any{"id": "i-" + f.name(), "name": f.name(), "type": "file",
					"size": f.bytes, "mime_type": "application/octet-stream", "created_at": 0, "link": f.directLink(r)})
			}
		}
		if !extras {
			content = append(content, map[string]any{"id": "f-" + tid + "-x", "name": "extras", "type": "folder", "created_at": 0})
		}
		writeJSON(w, 200, map[string]any{"status": "success", "name": "x", "parent_id": "root", "folder_id": fid,
			"content": content})
	case "transfer/delete":
		if !s.removeTorrent(r.PostForm.Get("id")) {
			pmError(w, "not_found")
			return true
		}
		writeJSON(w, 200, map[string]any{"status": "success"})
	default:
		return false
	}
	return true
}
