package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/tools"
)

// cli runs the command line with its own data folder: exit code, stdout,
// stderr.
func cli(t *testing.T, data string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	stdout, stderr = &out, &errOut
	ipc.SetDataDir(data)
	t.Cleanup(func() { stdout, stderr = os.Stdout, os.Stderr; ipc.SetDataDir("") })
	code := run(context.Background(), args)
	return code, out.String(), errOut.String()
}

func TestHelpVersionAndWrongCommands(t *testing.T) {
	data := t.TempDir()
	if code, out, _ := cli(t, data); code != 0 || !strings.Contains(out, "torrent-get") || !strings.Contains(out, "--json") {
		t.Error(code, out)
	}
	if code, out, _ := cli(t, data, "--version"); code != 0 || !strings.HasPrefix(out, name+" ") {
		t.Error(code, out)
	}
	if code, _, errOut := cli(t, data, "frobnicate"); code != 2 || !strings.Contains(errOut, "no command") {
		t.Error(code, errOut)
	}
	if code, _, errOut := cli(t, data, "api", "maybe"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Error(code, errOut)
	}
	if code, _, errOut := cli(t, data, "get", "-c", "99", "https://example.com/a"); code != 2 || !strings.Contains(errOut, "between 1 and 16") {
		t.Error(code, errOut)
	}
}

func TestFlagsAnywhereAmongTheArguments(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	o := fs.String("o", ".", "")
	later := fs.Bool("later", false, "")
	rest, err := parse(fs, []string{"https://a", "-o", "dir", "b", "--later", "--", "-c"})
	if err != nil || *o != "dir" || !*later || !slices.Equal(rest, []string{"https://a", "b", "-c"}) {
		t.Error(rest, err, *o, *later)
	}
	if _, err := parse(flag.NewFlagSet("y", flag.ContinueOnError), []string{"-nope"}); err == nil {
		t.Error("an unknown flag")
	}
}

func TestSubtitlesArgument(t *testing.T) {
	if s := subsArg("none", true, true); len(s.Langs) != 0 {
		t.Error(s)
	}
	if s := subsArg(" en, fr ,", true, false); !slices.Equal(s.Langs, []string{"en", "fr"}) || !s.Automatic || s.Embed {
		t.Error(s)
	}
	if s := subsArg("ALL", false, true); !slices.Equal(s.Langs, []string{tools.AllUploaders}) || !s.Embed {
		t.Error(s)
	}
}

// fakeApp answers on the app's channel in data: a list, actions, the API
// switch, a pairing code; every request kept.
func fakeApp(t *testing.T, data string, apiOn bool) func() []map[string]any {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ep, _ := json.Marshal(map[string]any{"port": ln.Addr().(*net.TCPAddr).Port, "token": "secret"})
	if err := os.WriteFile(filepath.Join(data, "ipc.json"), ep, 0o600); err != nil {
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
			answer := "ok"
			switch {
			case req["api"] != nil:
				answer = ipc.Line(ipc.APIAnswer{API: req["api"] == true})
			case req["pair"] == true:
				answer = ipc.Line(ipc.PairAnswer{Code: "12345678", Minutes: 10, URLs: []string{"https://192.168.1.2:8463/"}, QR: "█▀▄\n"})
			case !apiOn && (req["query"] != nil || req["action"] != nil):
				answer = ipc.Line(ipc.ErrorAnswer{Error: "the automation API is off", Code: ipc.APIOff})
			case req["query"] == "list":
				answer = `{"downloads":[],"speed":0,"queues":[],"categories":[],"free":null}`
			case req["action"] != nil:
				answer = ipc.Line(map[string]any{"done": req["ids"], "skipped": []any{}})
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

func TestTheRunningAppsCommands(t *testing.T) {
	data := t.TempDir()
	if code, _, errOut := cli(t, data, "status"); code != 1 || !strings.Contains(errOut, "isn't running") {
		t.Error("no app", code, errOut)
	}
	seen := fakeApp(t, data, true)
	if code, out, _ := cli(t, data, "status"); code != 0 || !strings.Contains(out, `"downloads":[]`) {
		t.Error(code, out)
	}
	if code, out, _ := cli(t, data, "pause", "a", "torrent:b"); code != 0 || !strings.Contains(out, `"done":["a","torrent:b"]`) {
		t.Error(code, out)
	}
	if code, out, _ := cli(t, data, "api", "on"); code != 0 || out != `{"api":true}`+"\n" {
		t.Error(code, out)
	}
	if code, out, _ := cli(t, data, "pair"); code != 0 || !strings.Contains(out, "1234 5678") || !strings.Contains(out, "█▀▄") || !strings.Contains(out, "https://192.168.1.2:8463/") {
		t.Error(code, out)
	}
	if code, out, _ := cli(t, data, "--json", "pair"); code != 0 || !strings.Contains(out, `"code":"12345678"`) {
		t.Error(code, out)
	}
	if code, _, _ := cli(t, data, "add", "local.torrent", "-o", "sub", "--later"); code != 0 {
		t.Error(code)
	}
	all := seen()
	add := all[len(all)-1]
	cwd, _ := os.Getwd()
	if add["url"] != filepath.Join(cwd, "local.torrent") || add["dir"] != filepath.Join(cwd, "sub") || add["later"] != true || add["silent"] != true || add["token"] != "secret" {
		t.Error(add)
	}

	off := t.TempDir()
	fakeApp(t, off, false)
	if code, out, _ := cli(t, off, "status"); code != 1 || !strings.Contains(out, ipc.APIOff) {
		t.Error("the API off", code, out)
	}
}

// get downloads with the engine into its own list; list shows it.
func TestGetDownloadsAndListsIt(t *testing.T) {
	body := bytes.Repeat([]byte("yellow flash "), 50_000)
	modified := time.Unix(1_700_000_000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "file.bin", modified, bytes.NewReader(body))
	}))
	defer srv.Close()
	data, out := t.TempDir(), t.TempDir()
	sum := sha256.Sum256(body)
	code, stdoutText, errText := cli(t, data, "--json", "get", srv.URL+"/file.bin", "-o", out, "-c", "4", "--checksum", "sha256:"+hex.EncodeToString(sum[:]))
	if code != 0 || !strings.Contains(stdoutText, `"event":"done"`) || !strings.Contains(stdoutText, hex.EncodeToString(sum[:])) {
		t.Fatal(code, stdoutText, errText)
	}
	if got, _ := os.ReadFile(filepath.Join(out, "file.bin")); !bytes.Equal(got, body) {
		t.Error("the file differs")
	}
	if code, listed, _ := cli(t, data, "list"); code != 0 || !strings.Contains(listed, "COMPLETED") || !strings.Contains(listed, "file.bin") {
		t.Error(code, listed)
	}
	if code, _, errText := cli(t, data, "resume", "no-such-id"); code != 1 || errText == "" {
		t.Error(code, errText)
	}
}

// A .torrent made here reads back; torrents never run without a proxy.
func TestTorrentCreateInfoAndNoProxy(t *testing.T) {
	data, src := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "Pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, size := range map[string]int{"a.bin": 70000, "b.txt": 10} {
		if err := os.WriteFile(filepath.Join(src, "Pack", n), bytes.Repeat([]byte("x"), size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "pack.torrent")
	if code, out, errText := cli(t, data, "torrent-create", filepath.Join(src, "Pack"), "-o", file); code != 0 || !strings.Contains(out, "magnet:?xt=urn:btih:") {
		t.Fatal(code, out, errText)
	}
	code, out, _ := cli(t, data, "--json", "torrent-info", file)
	var info struct {
		Name  string `json:"name"`
		Total int64  `json:"total"`
		Files []struct{ Path string }
	}
	if err := json.Unmarshal([]byte(out), &info); code != 0 || err != nil || info.Name != "Pack" || info.Total != 70010 || len(info.Files) != 2 {
		t.Error(code, out, err)
	}
	if code, _, errText := cli(t, data, "torrent-get", file, "-o", t.TempDir()); code != 1 || !strings.Contains(errText, "--proxy") {
		t.Error(code, errText)
	}
	if code, _, errText := cli(t, data, "torrent-info", "magnet:?xt=urn:btih:"+strings.Repeat("ab", 20)); code != 1 || !strings.Contains(errText, "--proxy") {
		t.Error(code, errText)
	}
	if code, _, errText := cli(t, data, "torrent-get", file, "--proxy", "http://127.0.0.1:3128"); code != 1 || !strings.Contains(errText, "SOCKS5") {
		t.Error(code, errText)
	}
}

func TestHelpersMustBeInstalled(t *testing.T) {
	data := t.TempDir()
	if code, _, errText := cli(t, data, "video-info", "https://example.com/watch"); code != 1 || !strings.Contains(errText, "install-ytdlp") {
		t.Error(code, errText)
	}
	if code, _, errText := cli(t, data, "gallery-get", "https://example.com/album"); code != 1 || !strings.Contains(errText, "install-gallery-dl") {
		t.Error(code, errText)
	}
	if code, _, errText := cli(t, data, "video-info", "file:///etc/passwd"); code != 1 || errText == "" {
		t.Error(code, errText)
	}
}
