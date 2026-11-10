// Ported from Mister Download Manager, crates/ipc/src/lib.rs and crates/ipc/src/api.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

/* ---------- command line ---------- */

func TestBareMagnetsAndTorrentFiles(t *testing.T) {
	c, ok := ParseArgs([]string{"magnet:?xt=urn:btih:abc"})
	if !ok || c.URL != "magnet:?xt=urn:btih:abc" {
		t.Fatalf("magnet: %+v %v", c, ok)
	}
	c, ok = ParseArgs([]string{`C:\dl\x.TORRENT`})
	if !ok || c.URL != `C:\dl\x.TORRENT` {
		t.Fatalf("torrent: %+v %v", c, ok)
	}
	if _, ok = ParseArgs([]string{"stray"}); ok {
		t.Fatal("a stray word is nothing to do")
	}
	c, ok = ParseArgs([]string{"/show", "Options:Connection"})
	if !ok || c.Show != "options:connection" {
		t.Fatalf("show: %+v", c)
	}
}

func TestIDMCommandLine(t *testing.T) {
	a := func(s string) []string { return strings.Split(s, " ") }
	c, ok := ParseArgs(a(`/d http://x/a.zip /p C:\dl /f b.zip /a /n`))
	if !ok || c.URL != "http://x/a.zip" || c.Dir != `C:\dl` || c.Name != "b.zip" {
		t.Fatalf("got %+v", c)
	}
	if !c.Later || !c.Silent || c.StartQueue {
		t.Fatalf("flags: %+v", c)
	}
	if c, _ = ParseArgs(a("/s")); !c.StartQueue || c.Startup {
		t.Fatalf("/s: %+v", c)
	}
	if c, _ = ParseArgs(a("/exit")); !c.Exit {
		t.Fatal("/exit")
	}
	if c, _ = ParseArgs([]string{StartupArg}); !c.Startup {
		t.Fatal("startup arg")
	}
	if c, _ = ParseArgs(a("--startup")); !c.Startup {
		t.Fatal("--startup")
	}
	if c, _ = ParseArgs(a("-D http://x/ -Q")); c.URL != "http://x/" {
		t.Fatalf("case and dash: %+v", c)
	}
	if _, ok = ParseArgs(nil); ok {
		t.Fatal("no args is nothing to do")
	}
}

/* ---------- request reading ---------- */

// requestFrom sends data to a fresh local socket and returns what
// readRequest makes of it.
func requestFrom(t *testing.T, data []byte) (string, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write(data)
		time.Sleep(300 * time.Millisecond)
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	got, rerr := readRequest(conn)
	lingerClose(conn)
	<-done
	return got, rerr
}

func TestRequestLinesAreBounded(t *testing.T) {
	got, err := requestFrom(t, []byte("{\"a\":1}\nrest"))
	if err != nil || got != `{"a":1}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err = requestFrom(t, []byte(strings.Repeat("x", maxRequest+10))); !errors.Is(err, errTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
	if _, err = requestFrom(t, []byte{0xff, 0xfe, '\n'}); !errors.Is(err, errUnusable) {
		t.Fatalf("not UTF-8: %v", err)
	}
}

/* ---------- server ---------- */

// testServer runs an accept loop on a fresh port with token "secret". The
// returned channel receives every command the app takes; accept (when
// non-nil) decides whether it takes it.
func testServer(t *testing.T, accept func() bool) (port int, got chan Incoming) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got = make(chan Incoming, 16)
	s := newServer(ln, "secret", func(in Incoming) bool {
		if accept != nil && !accept() {
			return false
		}
		got <- in
		return true
	})
	s.start()
	t.Cleanup(func() { _ = s.Close() })
	return ln.Addr().(*net.TCPAddr).Port, got
}

func addrOf(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

func exchangeRaw(t *testing.T, port int, data []byte) string {
	t.Helper()
	c, err := net.Dial("tcp", addrOf(port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	_, _ = c.Write(data)
	reply, _ := bufio.NewReader(c).ReadString('\n')
	return reply
}

func TestSlowClientDoesNotHoldUpARealCommand(t *testing.T) {
	port, got := testServer(t, nil)
	// Connects and says nothing (it would hold a one-at-a-time server for 3 s).
	slow, err := net.Dial("tcp", addrOf(port))
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	start := time.Now()
	if r := exchangeRaw(t, port, []byte(`{"token":"secret","url":"http://x/a.zip"}`+"\n")); r != "ok\n" {
		t.Fatalf("reply %q", r)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if in := <-got; in.Cmd.URL != "http://x/a.zip" || in.Reply != nil {
		t.Fatalf("got %+v", in)
	}
}

func TestTooLargeAndQuittingAreSaid(t *testing.T) {
	var quitting atomic.Bool
	port, _ := testServer(t, func() bool { return !quitting.Load() })
	big := `{"token":"secret","cookies":"` + strings.Repeat("x", maxRequest+10) + "\"}\n"
	if r := exchangeRaw(t, port, []byte(big)); r != "too-large\n" {
		t.Fatalf("big: %q", r)
	}
	// A command as large as the browser's host takes still goes through.
	fits := `{"token":"secret","url":"http://x/a.zip","cookies":"` + strings.Repeat("x", MaxMessage) + "\"}\n"
	if r := exchangeRaw(t, port, []byte(fits)); r != "ok\n" {
		t.Fatalf("fits: %q", r)
	}
	quitting.Store(true)
	if r := exchangeRaw(t, port, []byte(`{"token":"secret","url":"http://x/a.zip"}`+"\n")); r != "gone\n" {
		t.Fatalf("quitting: %q", r)
	}
}

func TestWebPagesAndWrongTokensGetNothing(t *testing.T) {
	port, got := testServer(t, nil)
	post := "POST / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n{\"token\":\"secret\"}"
	if r := exchangeRaw(t, port, []byte(post)); r != "" {
		t.Fatalf("web page got %q", r)
	}
	if r := exchangeRaw(t, port, []byte(`{"token":"guess","url":"http://x/"}`+"\n")); r != "denied\n" {
		t.Fatalf("wrong token got %q", r)
	}
	if r := exchangeRaw(t, port, []byte("not json\n")); r != "denied\n" {
		t.Fatalf("garbage got %q", r)
	}
	select {
	case in := <-got:
		t.Fatalf("delivered %+v", in)
	default:
	}
}

func TestQueriesGetTheAppsAnswer(t *testing.T) {
	port, got := testServer(t, nil)
	go func() {
		in := <-got
		if in.Cmd.Query != "list" || in.Reply == nil {
			if in.Reply != nil {
				in.Reply <- "wrong"
			}
			return
		}
		in.Reply <- `{"downloads":[]}`
	}()
	if r := exchangeRaw(t, port, []byte(`{"token":"secret","query":"list"}`+"\n")); r != "{\"downloads\":[]}\n" {
		t.Fatalf("reply %q", r)
	}
}

func TestAStuckAppTimesOut(t *testing.T) {
	port, _ := testServer(t, nil) // takes the query, never answers
	if r := exchangeRaw(t, port, []byte(`{"token":"secret","api":true}`+"\n")); r != "timeout\n" {
		t.Fatalf("reply %q", r)
	}
}

func TestConnectionsBeyondTheLimitAreClosed(t *testing.T) {
	port, _ := testServer(t, nil)
	var held []net.Conn
	for range maxInFlight {
		c, err := net.Dial("tcp", addrOf(port))
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	time.Sleep(200 * time.Millisecond) // let the server accept them all
	extra, err := net.Dial("tcp", addrOf(port))
	if err != nil {
		t.Fatal(err)
	}
	_ = extra.SetDeadline(time.Now().Add(time.Second))
	if _, err = extra.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("over the limit: want closed, got %v", err)
	}
	_ = extra.Close()
	for _, c := range held {
		_ = c.Close()
	}
	// Slots come back once the silent clients go.
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := exchangeRaw(t, port, []byte(`{"token":"secret"}`+"\n"))
		if r == "ok\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slots not released: %q", r)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func TestSecretsCompareWhole(t *testing.T) {
	if !sameSecret("abc", "abc") || sameSecret("abc", "abd") || sameSecret("abc", "ab") {
		t.Fatal("sameSecret")
	}
}

func TestTokensAreRandom(t *testing.T) {
	a, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := randomToken()
	if a == b || len(a) != 64 {
		t.Fatalf("tokens %q %q", a, b)
	}
}

func TestLooksLikeHTTP(t *testing.T) {
	for line, want := range map[string]bool{
		"POST / HTTP/1.1\r": true, "GET /x HTTP/1.0": true,
		`{"token":"a"}`: false, "": false, `{"url":"x HTTP/"}`: false,
	} {
		if got := looksLikeHTTP(line); got != want {
			t.Errorf("looksLikeHTTP(%q) = %v", line, got)
		}
	}
}

/* ---------- end to end through ipc.json ---------- */

func TestServeAndSendThroughTheDataFolder(t *testing.T) {
	dir := t.TempDir()
	got := make(chan Incoming, 4)
	srv, err := Serve(dir, func(in Incoming) bool {
		if in.Reply != nil {
			in.Reply <- Line(ErrorAnswer{Error: "the automation API is off", Code: APIOff})
			return true
		}
		got <- in
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(dir, "ipc.json")); info.Mode().Perm() != 0o600 {
			t.Fatalf("ipc.json mode %v", info.Mode().Perm())
		}
	}
	if pid, ok := AppPID(dir); !ok || pid != os.Getpid() {
		t.Fatalf("pid %d %v", pid, ok)
	}
	size := int64(42)
	if err = Send(dir, Command{URL: "https://x/a.zip", Name: "a.zip", Size: &size, Silent: true}); err != nil {
		t.Fatal(err)
	}
	in := <-got
	if in.Cmd.URL != "https://x/a.zip" || in.Cmd.Name != "a.zip" || !in.Cmd.Silent || *in.Cmd.Size != 42 {
		t.Fatalf("got %+v", in.Cmd)
	}
	answer, err := Request(dir, Command{Query: "list"})
	if err != nil {
		t.Fatal(err)
	}
	_, e, err := ParseAnswer[ListAnswer](answer)
	if err != nil || e == nil || e.Code != APIOff {
		t.Fatalf("answer %q: %v %v", answer, e, err)
	}

	if err = srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "ipc.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ipc.json left behind: %v", err)
	}
	if err = Send(dir, Command{}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("after close: %v", err)
	}
}

func TestStaleEndpointsAreNotRunningAndStaleTokensRefused(t *testing.T) {
	dir := t.TempDir()
	// A port nothing listens on any more.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	data, _ := json.Marshal(endpoint{Port: port, Token: "old"})
	if err := PrivateWrite(endpointPath(dir), data); err != nil {
		t.Fatal(err)
	}
	if err := Send(dir, Command{}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("dead port: %v", err)
	}
	// A live server with another token.
	livePort, _ := testServer(t, nil)
	data, _ = json.Marshal(endpoint{Port: livePort, Token: "old"})
	if err := PrivateWrite(endpointPath(dir), data); err != nil {
		t.Fatal(err)
	}
	if err := Send(dir, Command{}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("stale token: %v", err)
	}
	if err := SendWaiting(dir, Command{}, 300*time.Millisecond); !errors.Is(err, ErrRefused) {
		t.Fatalf("SendWaiting gives up with the last error: %v", err)
	}
}

func TestSendWaitingReachesAnInstanceThatStartsLate(t *testing.T) {
	dir := t.TempDir()
	got := make(chan Incoming, 1)
	started := make(chan *Server, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		srv, err := Serve(dir, func(in Incoming) bool { got <- in; return true })
		if err != nil {
			started <- nil
			return
		}
		started <- srv
	}()
	err := SendWaiting(dir, Command{URL: "https://x/late"}, 5*time.Second)
	if srv := <-started; srv != nil {
		defer srv.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if in := <-got; in.Cmd.URL != "https://x/late" {
		t.Fatalf("got %+v", in.Cmd)
	}
}

func TestSinkAsk(t *testing.T) {
	s := NewSink(func(in Incoming) bool {
		go func() { in.Reply <- "pong" }()
		return true
	})
	if a, ok := s.Ask(Command{Query: "ping"}, time.Second); !ok || a != "pong" {
		t.Fatalf("%q %v", a, ok)
	}
	gone := NewSink(func(Incoming) bool { return false })
	if _, ok := gone.Ask(Command{}, time.Second); ok {
		t.Fatal("a quitting app answers nothing")
	}
	silent := NewSink(func(Incoming) bool { return true })
	if _, ok := silent.Ask(Command{}, 50*time.Millisecond); ok {
		t.Fatal("no answer in time")
	}
}

/* ---------- lock and data folder ---------- */

func TestOnlyOneInstanceHoldsTheLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Lock(dir); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second: %v", err)
	}
	_ = first.Close()
	again, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}

func TestDataDirOverrides(t *testing.T) {
	env := t.TempDir()
	t.Setenv(DataDirEnv, env)
	if d, err := DataDir(); err != nil || d != env {
		t.Fatalf("env: %q %v", d, err)
	}
	set := t.TempDir()
	SetDataDir(set)
	t.Cleanup(func() { SetDataDir("") })
	if d, _ := DataDir(); d != set {
		t.Fatalf("SetDataDir: %q", d)
	}
}

func TestPrivateWriteReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	for _, s := range []string{"one", "two"} {
		if err := PrivateWrite(path, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "two" {
		t.Fatalf("content %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
}

/* ---------- wire format ---------- */

func TestBrowserCookiesTakeTheExtensionsNames(t *testing.T) {
	var c Command
	line := `{"cookie_jar":[{"name":"a","value":"1","domain":".youtube.com","hostOnly":true,"path":"/","secure":true,"expirationDate":1.5e9}]}`
	if err := json.Unmarshal([]byte(line), &c); err != nil {
		t.Fatal(err)
	}
	k := c.CookieJar[0]
	if !k.HostOnly || k.Expires == nil || *k.Expires != 1.5e9 || k.Domain != ".youtube.com" {
		t.Fatalf("cookie %+v", k)
	}
}

func TestEnvelopeIsFlat(t *testing.T) {
	b, _ := json.Marshal(envelope{Token: "t", Command: Command{URL: "u", Remote: true}})
	if string(b) != `{"token":"t","url":"u"}` {
		t.Fatalf("line %s", b)
	}
	if !(Command{Pair: true}).WantsReply() || (Command{URL: "x"}).WantsReply() {
		t.Fatal("WantsReply")
	}
}

func TestErrorsAndAnswersReadBack(t *testing.T) {
	_, e, err := ParseAnswer[ListAnswer](`{"error":"the automation API is off","code":"api-off"}`)
	if err != nil || e == nil || e.Code != APIOff {
		t.Fatalf("error answer: %v %v", e, err)
	}
	list, e, err := ParseAnswer[ListAnswer](`{"downloads":[],"speed":5}`)
	if err != nil || e != nil || list.Speed != 5 {
		t.Fatalf("list: %+v %v %v", list, e, err)
	}
	if _, _, err = ParseAnswer[ListAnswer]("ok"); err == nil {
		t.Fatal(`"ok" is not a list`)
	}
}
