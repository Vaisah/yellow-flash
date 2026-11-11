// Ported from Mister Download Manager, crates/mcp/src/lib.rs (tests) and crates/mcp/tests/stdio.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vaisah/internal/ipc"
)

const list = `{"downloads":[
	{"id":"a","file":"ubuntu.iso","url":"https://releases.ubuntu.com/ubuntu.iso","status":"Complete","path":"/d/f","size":100,"downloaded":50,"speed":0,"added":"2026-01-01T00:00:00Z"},
	{"id":"b","file":"7z.exe","url":"https://www.7-zip.org/a/7z.exe","status":"Downloading","path":"/d/f","size":100,"downloaded":50,"speed":0,"added":"2026-02-01T00:00:00Z"},
	{"id":"c","file":"report.pdf","url":"https://example.com/report.pdf","status":"Stopped","path":"/d/f","size":100,"downloaded":50,"speed":0,"added":"2026-03-01T00:00:00Z"}
],"speed":1234}`

func ids(t *testing.T, answer string) []string {
	t.Helper()
	var v struct {
		Downloads []struct{ ID string } `json:"downloads"`
	}
	if err := json.Unmarshal([]byte(answer), &v); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range v.Downloads {
		out = append(out, d.ID)
	}
	return out
}

func TestListsNewestFirstFilteredAndCut(t *testing.T) {
	all, err := filterList(list, listArgs{})
	if err != nil || !slices.Equal(ids(t, all), []string{"c", "b", "a"}) || !strings.Contains(all, `"matching":3`) || !strings.Contains(all, `"speed":1234`) {
		t.Error(all, err)
	}
	if got, _ := filterList(list, listArgs{Status: "downloading"}); !slices.Equal(ids(t, got), []string{"b"}) {
		t.Error(got)
	}
	if got, _ := filterList(list, listArgs{Search: "UBUNTU"}); !slices.Equal(ids(t, got), []string{"a"}) {
		t.Error(got)
	}
	one := 1
	if got, _ := filterList(list, listArgs{Limit: &one}); !slices.Equal(ids(t, got), []string{"c"}) || !strings.Contains(got, `"matching":3`) {
		t.Error(got)
	}
	if _, err := filterList(list, listArgs{Status: "paused"}); err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Error(err)
	}
}

func TestExplainsWhatWentWrong(t *testing.T) {
	if _, err := explain("", errors.New("refused")); err == nil || !strings.HasPrefix(err.Error(), notRunning) {
		t.Error(err)
	}
	for answer, want := range map[string]string{"timeout": "in time", "denied": "restart", "gone": "quitting"} {
		if _, err := explain(answer, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Error(answer, err)
		}
	}
	if _, err := explain(`{"error":"the automation API is off","code":"api-off"}`, nil); err == nil || err.Error() != apiOff {
		t.Error(err)
	}
	if _, err := explain(`{"error":"unknown action \"x\""}`, nil); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Error(err)
	}
	if got, err := explain("ok", nil); got != "ok" || err != nil {
		t.Error(got, err)
	}
}

func TestAddsOnlyWhatTheAppDownloads(t *testing.T) {
	for _, ok := range []string{"https://example.com/a.zip", "ftp://example.com/a.zip", "MAGNET:?xt=urn:btih:abc"} {
		if !addable(ok) {
			t.Error(ok)
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "example.com/a.zip"} {
		if addable(bad) {
			t.Error(bad)
		}
	}
}

// fakeApp is a stand-in for the app: ipc.json in dir, one answer per
// connection, every request kept.
func fakeApp(t *testing.T, dir string, apiOn bool) func() []map[string]any {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ep, _ := json.Marshal(map[string]any{"port": ln.Addr().(*net.TCPAddr).Port, "token": "secret"})
	if err := os.WriteFile(filepath.Join(dir, "ipc.json"), ep, 0o600); err != nil {
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		seen []map[string]any
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			var req map[string]any
			_ = json.Unmarshal([]byte(line), &req)
			_, query := req["query"].(string)
			_, action := req["action"].(string)
			var answer string
			switch {
			case req["token"] != "secret":
				answer = "denied"
			case !apiOn && (query || action):
				answer = `{"error":"the automation API is off","code":"api-off"}`
			case req["query"] == "list":
				answer = strings.ReplaceAll(list, "\n", "")
			case action:
				b, _ := json.Marshal(map[string]any{"done": req["ids"], "skipped": []any{}})
				answer = string(b)
			default:
				answer = "ok"
			}
			mu.Lock()
			seen = append(seen, req)
			mu.Unlock()
			_, _ = io.WriteString(c, answer+"\n")
			c.Close()
		}
	}()
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// mcp is the server over pipes, as an assistant starts it.
type mcp struct {
	t    *testing.T
	in   io.WriteCloser
	out  *bufio.Reader
	next int
}

func startMCP(t *testing.T, dataDir string) *mcp {
	t.Helper()
	ask := func(cmd ipc.Command) (string, error) { return ipc.Request(dataDir, cmd) }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		_ = NewServer(ask, "test").Serve(inR, outW)
		outW.Close()
	}()
	m := &mcp{t: t, in: inW, out: bufio.NewReader(outR), next: 1}
	t.Cleanup(func() { inW.Close() })
	init := m.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init["serverInfo"].(map[string]any)["name"] != "yellowflash" || !strings.Contains(init["instructions"].(string), "api on") ||
		init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("%v", init)
	}
	m.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return m
}

func (m *mcp) send(msg any) {
	b, _ := json.Marshal(msg)
	if _, err := m.in.Write(append(b, '\n')); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mcp) read() map[string]any {
	line, err := m.out.ReadString('\n')
	if err != nil {
		m.t.Fatal("server closed:", err)
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		m.t.Fatal(err)
	}
	return msg
}

func (m *mcp) request(method string, params any) map[string]any {
	m.t.Helper()
	id := m.next
	m.next++
	m.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	msg := m.read()
	if msg["id"] != float64(id) || msg["error"] != nil {
		m.t.Fatalf("%v", msg)
	}
	return msg["result"].(map[string]any)
}

// call is a tool's text and whether it is an error.
func (m *mcp) call(name string, args any) (string, bool) {
	m.t.Helper()
	r := m.request("tools/call", map[string]any{"name": name, "arguments": args})
	return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"] == true
}

func TestAnAssistantListsAddsAndActsThroughTheApp(t *testing.T) {
	data := t.TempDir()
	seen := fakeApp(t, data, true)
	m := startMCP(t, data)

	var names []string
	for _, tl := range m.request("tools/list", map[string]any{})["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"add_download", "list_downloads", "pause_downloads", "remove_downloads", "resume_downloads", "start_queue"}) {
		t.Error(names)
	}

	text, isErr := m.call("list_downloads", map[string]any{"status": "downloading"})
	if isErr || !slices.Equal(ids(t, text), []string{"b"}) || !strings.Contains(text, `"matching":1`) {
		t.Error(text)
	}
	if text, isErr := m.call("add_download", map[string]any{"url": "https://example.com/a.zip", "folder": `D:\Files`, "later": true}); isErr || !strings.Contains(text, "main queue") {
		t.Error(text)
	}
	if text, isErr := m.call("add_download", map[string]any{"url": "file:///etc/passwd"}); !isErr || !strings.Contains(text, "isn't an address") {
		t.Error(text)
	}
	if text, isErr := m.call("remove_downloads", map[string]any{"ids": []string{"a"}, "delete_files": true}); isErr || !strings.Contains(text, `"done":["a"]`) {
		t.Error(text)
	}
	if _, isErr := m.call("pause_downloads", map[string]any{"ids": []string{"b"}}); isErr {
		t.Error("pause")
	}
	if text, isErr := m.call("pause_downloads", map[string]any{"ids": []string{}}); !isErr || !strings.Contains(text, "no download ids") {
		t.Error(text)
	}
	if _, isErr := m.call("start_queue", map[string]any{}); isErr {
		t.Error("start_queue")
	}
	// An unknown tool, an unknown method: protocol errors.
	m.send(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "tools/call", "params": map[string]any{"name": "format_disk"}})
	if msg := m.read(); msg["error"] == nil {
		t.Error(msg)
	}
	m.send(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "resources/list"})
	if msg := m.read(); msg["error"].(map[string]any)["code"] != float64(-32601) {
		t.Error(msg)
	}
	m.next = 101
	m.request("ping", map[string]any{})

	// What reached the app: the fields its channel reads, with the token.
	all := seen()
	i := slices.IndexFunc(all, func(r map[string]any) bool { _, ok := r["url"]; return ok })
	if i < 0 {
		t.Fatal("no add")
	}
	if add := all[i]; add["url"] != "https://example.com/a.zip" || add["dir"] != `D:\Files` || add["later"] != true || add["silent"] != true {
		t.Error(add)
	}
	if !slices.ContainsFunc(all, func(r map[string]any) bool { return r["action"] == "delete" }) ||
		!slices.ContainsFunc(all, func(r map[string]any) bool { return r["action"] == "pause" }) ||
		!slices.ContainsFunc(all, func(r map[string]any) bool { return r["start_queue"] == true }) {
		t.Error(all)
	}
	for _, r := range all {
		if r["token"] != "secret" {
			t.Error("no token", r)
		}
	}
}

func TestSaysWhenTheAPIIsOffOrTheAppIsNotRunning(t *testing.T) {
	data := t.TempDir()
	fakeApp(t, data, false)
	if text, isErr := startMCP(t, data).call("list_downloads", map[string]any{}); !isErr || !strings.Contains(text, "api on") {
		t.Error(text)
	}
	if text, isErr := startMCP(t, t.TempDir()).call("pause_downloads", map[string]any{"ids": []string{"a"}}); !isErr || !strings.Contains(text, "isn't running") {
		t.Error(text)
	}
}
