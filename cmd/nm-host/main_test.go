// Ported from Mister Download Manager, crates/nmhost/tests/host.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// The host as the browser runs it: this test binary started again as the
// program, its standard streams framed as native messages, its own data
// folder (YELLOWFLASH_DATA_DIR) with a fake Yellow Flash listening where
// ipc.json says, answering as the test wants.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
)

const runAsHost = "YELLOWFLASH_NMHOST_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runAsHost) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var caller = "chrome-extension://" + browser.UnpackedID + "/"

const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/141.0.0.0 Safari/537.36"

// dataDir is a data folder of its own, with the app's capture rules saved
// as the app saves them (rules "" for none).
func dataDir(t *testing.T, rules string) string {
	t.Helper()
	dir := t.TempDir()
	settings := map[string]string{}
	if rules != "" {
		settings[browser.SettingsKey] = rules
	}
	raw, _ := json.Marshal(map[string]any{"settings": settings})
	if err := os.WriteFile(filepath.Join(dir, "downloads.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeApp listens where ipc.json in dir says: each request line it gets goes
// to the returned channel, and it answers answer.
func fakeApp(t *testing.T, dir, answer string) <-chan map[string]any {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	raw, _ := json.Marshal(map[string]any{"port": ln.Addr().(*net.TCPAddr).Port, "token": "t0k3n"})
	if err := os.WriteFile(filepath.Join(dir, "ipc.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got := make(chan map[string]any, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err == nil {
				var v map[string]any
				_ = json.Unmarshal([]byte(line), &v)
				got <- v
				_, _ = conn.Write([]byte(answer + "\n"))
			}
			conn.Close()
		}
	}()
	return got
}

func hostCmd(dir string, args ...string) *exec.Cmd {
	c := exec.Command(os.Args[0], args...)
	c.Env = append(os.Environ(), runAsHost+"=1", ipc.DataDirEnv+"="+dir)
	return c
}

// run starts the host as caller with messages, returning its replies and
// whether it exited cleanly.
func run(t *testing.T, dir, caller string, messages ...map[string]any) ([]map[string]any, bool) {
	t.Helper()
	var in bytes.Buffer
	for _, m := range messages {
		raw, _ := json.Marshal(m)
		_ = writeMessage(&in, raw)
	}
	c := hostCmd(dir, caller)
	c.Stdin = &in
	var out bytes.Buffer
	c.Stdout = &out
	err := c.Run()
	return readReplies(t, out.Bytes()), err == nil
}

func received(ch <-chan map[string]any, wait time.Duration) (map[string]any, bool) {
	select {
	case v := <-ch:
		return v, true
	case <-time.After(wait):
		return nil, false
	}
}

// Nothing edited in Options: a model is taken, a picture stays with the
// browser.
func TestTheDefaultListTakesAModelAndLeavesAPicture(t *testing.T) {
	dir := dataDir(t, "")
	app := fakeApp(t, dir, "ok")
	offer := func(name string) map[string]any {
		return map[string]any{"url": "https://huggingface.co/org/repo/resolve/main/" + name,
			"userAgent": ua, "filename": "C:/dl/" + name}
	}
	replies, ok := run(t, dir, caller, offer("model.Q4_K_M.gguf"), offer("photo.jpg"))
	if !ok || len(replies) != 2 {
		t.Fatalf("exit %v, replies %v", ok, replies)
	}
	if replies[0]["ok"] != true {
		t.Errorf("model: %v", replies[0])
	}
	if v, got := received(app, 5*time.Second); !got || v["name"] != "model.Q4_K_M.gguf" {
		t.Errorf("app got %v", v)
	}
	if replies[1]["ok"] == true {
		t.Errorf("picture: %v", replies[1])
	}
	if v, got := received(app, 300*time.Millisecond); got {
		t.Errorf("the picture reached the app: %v", v)
	}
}

func TestRulesHandOffAndTheStatusPerBrowser(t *testing.T) {
	dir := dataDir(t, `{"autoTypes":"ZIP PDF","captureMinKB":50}`)
	app := fakeApp(t, dir, "ok")
	replies, ok := run(t, dir, caller,
		map[string]any{"rules": true, "userAgent": ua, "brands": []string{"Brave"}, "v": "0.2.5"},
		map[string]any{
			"url": "https://x.test/get?id=1", "finalUrl": "https://cdn.test/f/a.zip",
			"referrer": "https://x.test/", "cookies": "sid=1", "userAgent": ua,
			"filename": `C:\dl\a.zip`, "size": 200000, "mime": "application/zip",
			"post": "token=abc", "postType": "application/x-www-form-urlencoded", "private": true,
		})
	if !ok || len(replies) != 2 {
		t.Fatalf("exit %v, replies %v", ok, replies)
	}
	rules := replies[0]
	if rules["ok"] != true || rules["types"] != "ZIP PDF" || rules["minKb"] != 50.0 || rules["version"] != ipc.AppVersion {
		t.Errorf("rules %v", rules)
	}
	if replies[1]["ok"] != true {
		t.Errorf("hand-off %v", replies[1])
	}
	sent, got := received(app, 5*time.Second)
	if !got {
		t.Fatal("nothing reached the app")
	}
	want := map[string]any{"token": "t0k3n", "url": "https://x.test/get?id=1", "final_url": "https://cdn.test/f/a.zip",
		"name": "a.zip", "size": 200000.0, "post": "token=abc", "private": true}
	for k, v := range want {
		if sent[k] != v {
			t.Errorf("%s = %v, want %v", k, sent[k], v)
		}
	}
	seen, _ := os.ReadFile(filepath.Join(dir, "extension-seen"))
	if !strings.Contains(string(seen), "Brave Browser\t0.2.5") {
		t.Errorf("extension-seen %q", seen)
	}
}

func TestOptionsAndARefusingAppStartNothing(t *testing.T) {
	dir := dataDir(t, `{"captureMinKB":100}`)
	// The app says the command is too large: said back, no second app
	// started (the host would wait 20 s for one).
	app := fakeApp(t, dir, "too-large")
	started := time.Now()
	replies, _ := run(t, dir, caller,
		map[string]any{"url": "https://x.test/a.zip", "filename": "a.zip", "userAgent": ua},
		// Left to the browser by Options: an excluded site, a file under the minimum.
		map[string]any{"url": "https://dl.update.microsoft.com/kb.exe", "filename": "kb.exe", "userAgent": ua},
		map[string]any{"url": "https://x.test/tiny.zip", "filename": "tiny.zip", "size": 2048, "userAgent": ua})
	if time.Since(started) > 5*time.Second {
		t.Error("waited for an app that answered")
	}
	if len(replies) != 3 || replies[0]["why"] != "too-large" || replies[1]["why"] != "options" || replies[2]["why"] != "options" {
		t.Fatalf("replies %v", replies)
	}
	if _, got := received(app, time.Second); !got {
		t.Error("the first was not offered")
	}
	if v, got := received(app, 100*time.Millisecond); got {
		t.Errorf("another reached the app: %v", v)
	}
}

// Every download left to the browser is in the log with why, whether the
// host refused it or the extension left it alone and only told the host;
// none reaches the app. A private window's is logged without its site or
// name.
func TestWhatIsLeftToTheBrowserIsLogged(t *testing.T) {
	dir := dataDir(t, `{"autoTypes":"ZIP"}`)
	app := fakeApp(t, dir, "ok")
	logFile := filepath.Join(dir, "logs", "yellowflash.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, []byte("an earlier line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replies, ok := run(t, dir, caller,
		map[string]any{"url": "https://x.test/p.png", "filename": "p.png", "userAgent": ua},
		map[string]any{"url": "https://dl.update.microsoft.com/kb.zip", "filename": "kb.zip", "userAgent": ua},
		map[string]any{"url": "https://secret.test/s.png", "filename": "s.png", "userAgent": ua, "private": true},
		map[string]any{"declined": map[string]any{"why": "site-access", "site": "y.test", "name": "b.zip"}, "userAgent": ua})
	if !ok || len(replies) != 4 {
		t.Fatalf("exit %v, replies %v", ok, replies)
	}
	var declined []any
	for _, r := range replies {
		declined = append(declined, r["declined"])
	}
	if declined[0] != "file-type" || declined[1] != "site" || declined[2] != "file-type" || declined[3] != nil {
		t.Errorf("declined %v", declined)
	}
	if replies[3]["ok"] != true {
		t.Errorf("a notice is only noted: %v", replies[3])
	}
	if v, got := received(app, 500*time.Millisecond); got {
		t.Errorf("reached the app: %v", v)
	}
	raw, _ := os.ReadFile(logFile)
	log := string(raw)
	lines := strings.Split(strings.TrimSuffix(log, "\n"), "\n")
	if len(lines) != 5 || lines[0] != "an earlier line" {
		t.Fatalf("log:\n%s", log)
	}
	checks := []string{
		"Google Chrome keeps p.png from x.test: its type isn't in Options > File types",
		"kb.zip from dl.update.microsoft.com: its site is in",
		"a download from a private window",
		"b.zip from y.test: the browser doesn't let the extension read this site",
	}
	for i, c := range checks {
		if !strings.Contains(lines[i+1], "INFO nmhost") || !strings.Contains(lines[i+1], c) {
			t.Errorf("line %d %q lacks %q", i+1, lines[i+1], c)
		}
	}
	if strings.Contains(log, "secret") {
		t.Error("a private window's site was logged")
	}
}

func TestStrangersAreRefused(t *testing.T) {
	dir := dataDir(t, "")
	for _, who := range []string{"chrome-extension://someoneelse/", "chrome-extension://oogbclbbninliongijffeeggalppliok/"} {
		replies, ok := run(t, dir, who, map[string]any{"rules": true})
		if ok || len(replies) != 0 {
			t.Errorf("%s served: %v", who, replies)
		}
	}
}

// The manifests (Linux and macOS: in the browsers' folders; Windows
// registers in the registry, not tested here) are written where a user with
// only Zen and Helium reads them, and --unregister takes them away.
func TestZenAndHeliumAloneAreRegistered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registers in the registry")
	}
	home := t.TempDir()
	support := filepath.Join(home, "Library", "Application Support")
	zen, helium := filepath.Join(home, ".zen"), filepath.Join(home, ".config", "net.imput.helium")
	mozHosts := filepath.Join(home, ".mozilla", "native-messaging-hosts")
	heliumHosts := filepath.Join(helium, "NativeMessagingHosts")
	if runtime.GOOS == "darwin" {
		zen, helium = filepath.Join(support, "Zen"), filepath.Join(support, "net.imput.helium")
		mozHosts, heliumHosts = filepath.Join(support, "Mozilla", "NativeMessagingHosts"), filepath.Join(helium, "NativeMessagingHosts")
	}
	mkdir(t, zen, helium)
	hostRun := func(arg string) {
		t.Helper()
		c := hostCmd(home, arg)
		c.Env = append(c.Env, "HOME="+home, "XDG_CONFIG_HOME=", "APPDIR=")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", arg, err, out)
		}
	}
	hostRun("--register")
	manifest := func(dir string) map[string]any {
		raw, err := os.ReadFile(filepath.Join(dir, browser.HostManifest))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		return v
	}
	if _, ok := manifest(mozHosts)["allowed_extensions"].([]any); !ok {
		t.Error("Mozilla's manifest")
	}
	origins, _ := manifest(heliumHosts)["allowed_origins"].([]any)
	if len(origins) == 0 || origins[0] != caller {
		t.Errorf("Helium's origins %v", origins)
	}
	hostRun("--unregister")
	for _, d := range []string{mozHosts, heliumHosts} {
		if _, err := os.Stat(filepath.Join(d, browser.HostManifest)); err == nil {
			t.Errorf("%s left", d)
		}
	}
}

// Run from inside an AppImage's mount (APPDIR), the host refuses to
// register itself: a manifest would name a program that won't be there.
func TestAHostInsideAnAppImageMountIsNotRegistered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("AppImages are Linux's")
	}
	home := t.TempDir()
	mkdir(t, filepath.Join(home, ".config", "google-chrome"))
	c := hostCmd(home, "--register")
	c.Env = append(c.Env, "HOME="+home, "XDG_CONFIG_HOME=", "APPDIR="+filepath.Dir(os.Args[0]))
	out, err := c.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "AppImage") {
		t.Errorf("registered: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts", browser.HostManifest)); err == nil {
		t.Error("a manifest was written")
	}
}

func TestPackageManifestsNameThePackagedHelper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux packaging")
	}
	root := t.TempDir()
	if out, err := hostCmd(root, "--package-manifests", root).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, s := range browser.LinuxSystemHostDirs {
		raw, err := os.ReadFile(filepath.Join(root, s.Dir, browser.HostManifest))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		_, gecko := v["allowed_extensions"]
		if v["path"] != packagedHost || gecko != (s.Family == browser.Gecko) {
			t.Errorf("%s: %v", s.Dir, v)
		}
	}
}

func mkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
