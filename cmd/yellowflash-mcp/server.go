// Ported from Mister Download Manager, crates/mcp/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Yellow Flash's MCP server (Model Context Protocol). An AI assistant
// (Claude, Cursor, VS Code, …) starts it and, through it, lists the running
// app's downloads, adds new ones and pauses, resumes or removes them. A thin
// bridge to the app's automation API: nothing here downloads by itself.
//
// MDM uses the rmcp library; MCP over stdio is newline-delimited JSON-RPC
// 2.0, and the few methods a tools-only server needs are written here.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/vaisah/internal/ipc"
)

// Ask is how a tool reaches the app: ipc.Request in the program, a
// stand-in in tests.
type Ask func(ipc.Command) (string, error)

// defaultLimit is the downloads listed when the assistant doesn't say how
// many.
const defaultLimit = 100

// protocolVersion is the newest MCP version this server speaks; an older
// one the client asks for is answered with that one.
const protocolVersion = "2025-06-18"

var knownVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18"}

const (
	notRunning = "Yellow Flash isn't running. Ask the user to start it, then try again."
	apiOff     = "Yellow Flash's automation API is off. Ask the user to turn it on once with " +
		"`yellowflash-cli api on` (it stays on), then try again."
	instructions = "Controls Yellow Flash, the download manager running on this computer. " +
		"list_downloads shows its downloads with their ids; pause_downloads, resume_downloads and " +
		"remove_downloads act on those ids; add_download adds an address (http, https, ftp, ftps or a " +
		"magnet link); start_queue starts the main download queue. Listing and acting on downloads " +
		"needs the app's automation API, which the user turns on with `yellowflash-cli api on`. Sizes " +
		"and speeds are in bytes and bytes per second."
)

// Server answers one assistant.
type Server struct {
	ask     Ask
	version string
	out     *json.Encoder
	mu      sync.Mutex
}

// NewServer is a server reaching the app through ask.
func NewServer(ask Ask, version string) *Server { return &Server{ask: ask, version: version} }

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve answers the messages read from r on w until r ends.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	s.out = json.NewEncoder(w)
	in := bufio.NewReader(r)
	for {
		line, err := in.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			s.handle(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (s *Server) send(v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.out.Encode(v)
}

func (s *Server) handle(line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.JSONRPC != "2.0" {
		s.send(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
		return
	}
	// A notification (no id): nothing is answered.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return
	}
	result, rerr := s.dispatch(req)
	res := response{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		res.Error = rerr
	} else {
		res.Result = result
	}
	s.send(res)
}

func (s *Server) dispatch(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := protocolVersion
		if slices.Contains(knownVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "yellowflash", "version": s.version},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
			p.Arguments = json.RawMessage("{}")
		}
		text, known, err := s.call(p.Name, p.Arguments)
		if !known {
			return nil, &rpcError{-32602, "unknown tool " + p.Name}
		}
		if err != nil {
			text = err.Error()
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": err != nil}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

// tool describes one tool for tools/list.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

func object(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	idsSchema = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "Download ids, from list_downloads."}
	actAnswer = ` Answers {"done": [ids], "skipped": [{"id", "why"}]}.`
)

var tools = []tool{
	{Name: "list_downloads",
		Description: "Lists Yellow Flash's downloads, newest first: id, file, url, path, status (Queued, " +
			"Downloading, Stopped, Complete or Error), size and downloaded (bytes; size null while unknown), " +
			"speed (bytes per second), when it was added and, for a failed one, why (error); with the total " +
			"speed and how many match.",
		InputSchema: object(nil, map[string]any{
			"status": map[string]any{"type": "string", "description": "Only downloads with this status: Queued, Downloading, Stopped, Complete or Error."},
			"search": map[string]any{"type": "string", "description": "Only downloads whose file name or address contains this text (any case)."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "description": "At most this many, newest first (default 100)."},
		}),
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false}},
	{Name: "add_download",
		Description: "Adds a download to Yellow Flash and starts it (or only queues it, with later). A " +
			"magnet link opens the app's window to choose the torrent's files.",
		InputSchema: object([]string{"url"}, map[string]any{
			"url":       map[string]any{"type": "string", "description": "The address: http, https, ftp or ftps, or a magnet link."},
			"folder":    map[string]any{"type": "string", "description": "The folder to save it in (default: the app's download folder)."},
			"file_name": map[string]any{"type": "string", "description": "The file name to save it as (default: from the address or the server)."},
			"later":     map[string]any{"type": "boolean", "description": "true: only add it to the main queue, without starting it now."},
		}),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": true}},
	{Name: "pause_downloads", Description: "Pauses running downloads by id." + actAnswer,
		InputSchema: object([]string{"ids"}, map[string]any{"ids": idsSchema}),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}},
	{Name: "resume_downloads", Description: "Starts or resumes stopped, queued or failed downloads by id." + actAnswer,
		InputSchema: object([]string{"ids"}, map[string]any{"ids": idsSchema}),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}},
	{Name: "remove_downloads",
		Description: "Removes downloads from Yellow Flash's list by id, keeping their files, or deleting them " +
			"from the disk too with delete_files. A running download must be paused first." + actAnswer,
		InputSchema: object([]string{"ids"}, map[string]any{
			"ids":          idsSchema,
			"delete_files": map[string]any{"type": "boolean", "description": "true: delete the downloaded files from the disk too; false (the default): keep them."},
		}),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true}},
	{Name: "start_queue", Description: "Starts Yellow Flash's main download queue.",
		InputSchema: object(nil, map[string]any{}),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true}},
}

// call runs a tool: its text, or its error for the assistant; known is
// false for no such tool.
func (s *Server) call(name string, raw json.RawMessage) (text string, known bool, err error) {
	switch name {
	case "list_downloads":
		var a listArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", true, badArgs(err)
		}
		answer, err := s.request(ipc.Command{Query: "list"})
		if err != nil {
			return "", true, err
		}
		text, err := filterList(answer, a)
		return text, true, err
	case "add_download":
		var a struct {
			URL      string `json:"url"`
			Folder   string `json:"folder"`
			FileName string `json:"file_name"`
			Later    bool   `json:"later"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", true, badArgs(err)
		}
		u := strings.TrimSpace(a.URL)
		if !addable(u) {
			return "", true, fmt.Errorf("%q isn't an address Yellow Flash downloads: it takes http, https, ftp and ftps addresses and magnet links", u)
		}
		if _, err := s.request(ipc.Command{URL: u, Dir: strings.TrimSpace(a.Folder), Name: strings.TrimSpace(a.FileName),
			Later: a.Later, Silent: true}); err != nil {
			return "", true, err
		}
		if a.Later {
			return "Added " + u + " to the main queue (not started). list_downloads shows it.", true, nil
		}
		return "Added " + u + " and started it. list_downloads shows its progress.", true, nil
	case "pause_downloads", "resume_downloads", "remove_downloads":
		var a struct {
			IDs         []string `json:"ids"`
			DeleteFiles bool     `json:"delete_files"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", true, badArgs(err)
		}
		if len(a.IDs) == 0 {
			return "", true, errors.New("no download ids given (list_downloads shows them)")
		}
		action := map[string]string{"pause_downloads": "pause", "resume_downloads": "start", "remove_downloads": "remove"}[name]
		if action == "remove" && a.DeleteFiles {
			action = "delete"
		}
		text, err := s.request(ipc.Command{Action: action, IDs: a.IDs})
		return text, true, err
	case "start_queue":
		if _, err := s.request(ipc.Command{StartQueue: true}); err != nil {
			return "", true, err
		}
		return "Started the main download queue.", true, nil
	}
	return "", false, nil
}

func badArgs(err error) error { return fmt.Errorf("the arguments don't fit the tool: %v", err) }

// request is the app's answer, or what went wrong in words the assistant
// can pass on.
func (s *Server) request(cmd ipc.Command) (string, error) {
	answer, err := s.ask(cmd)
	return explain(answer, err)
}

// explain reads the app's answer: a channel failure, a refusal or an
// {"error"} become the tool's error.
func explain(answer string, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("%s (%v)", notRunning, err)
	}
	switch answer {
	case "timeout":
		return "", errors.New("Yellow Flash didn't answer in time (a dialog may be open in it); try again")
	case "denied":
		return "", errors.New("Yellow Flash refused the request: its channel file (ipc.json) is from another start. Ask the user to restart the app")
	case "gone":
		return "", errors.New("Yellow Flash is quitting; try again once it runs")
	}
	if _, e, perr := ipc.ParseAnswer[json.RawMessage](answer); perr == nil && e != nil {
		if e.Code == ipc.APIOff {
			return "", errors.New(apiOff)
		}
		return "", errors.New("Yellow Flash: " + e.Error)
	}
	return answer, nil
}

// addable reports an address the app adds: http, https, ftp, ftps, and
// magnet links.
func addable(raw string) bool {
	if len(raw) >= 7 && strings.EqualFold(raw[:7], "magnet:") {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "ftps":
		return true
	}
	return false
}

type listArgs struct {
	Status string `json:"status"`
	Search string `json:"search"`
	Limit  *int   `json:"limit"`
}

// filterList cuts the list answer to what was asked for, newest first,
// with how many match.
func filterList(answer string, a listArgs) (string, error) {
	var list ipc.ListAnswer
	if err := json.Unmarshal([]byte(answer), &list); err != nil {
		return "", fmt.Errorf("unreadable answer: %v", err)
	}
	status := ""
	if s := strings.TrimSpace(a.Status); s != "" {
		i := slices.IndexFunc(ipc.Statuses[:], func(k string) bool { return strings.EqualFold(k, s) })
		if i < 0 {
			return "", fmt.Errorf("unknown status %q: use one of %v", s, ipc.Statuses)
		}
		status = ipc.Statuses[i]
	}
	search := strings.ToLower(strings.TrimSpace(a.Search))
	downloads := slices.DeleteFunc(list.Downloads, func(d ipc.DownloadInfo) bool {
		return status != "" && d.Status != status ||
			search != "" && !strings.Contains(strings.ToLower(d.File), search) && !strings.Contains(strings.ToLower(d.URL), search)
	})
	matching := len(downloads)
	// Newest first: by when added (RFC 3339 sorts as text), unknown last.
	slices.SortStableFunc(downloads, func(x, y ipc.DownloadInfo) int {
		ax, ay := "", ""
		if x.Added != nil {
			ax = *x.Added
		}
		if y.Added != nil {
			ay = *y.Added
		}
		return strings.Compare(ay, ax)
	})
	limit := defaultLimit
	if a.Limit != nil && *a.Limit > 0 {
		limit = *a.Limit
	}
	if len(downloads) > limit {
		downloads = downloads[:limit]
	}
	if downloads == nil {
		downloads = []ipc.DownloadInfo{}
	}
	b, err := json.Marshal(map[string]any{"downloads": downloads, "matching": matching, "speed": list.Speed})
	return string(b), err
}
