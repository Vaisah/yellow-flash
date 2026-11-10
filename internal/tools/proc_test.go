// Ported from Mister Download Manager, crates/tools/src/proc.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/ipc"
)

func readFile(t *testing.T, f *CookieFile) string {
	t.Helper()
	if f == nil {
		t.Fatal("no cookie file")
	}
	b, err := os.ReadFile(f.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheBrowsersContextForYtDlp(t *testing.T) {
	b := FromBrowser{Referer: "https://course.test/lesson", UserAgent: "UA/1", Cookies: "sid=abc; theme=dark"}
	args, file, err := b.Args("https://player.test/video/1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args[:4], []string{"--referer", "https://course.test/lesson", "--user-agent", "UA/1"}) || args[4] != "--cookies" {
		t.Errorf("args %q", args)
	}
	text := readFile(t, file)
	if !strings.HasPrefix(text, "# Netscape HTTP Cookie File") || !strings.Contains(text, ".player.test\tTRUE\t/\tFALSE\t0\tsid\tabc\n") {
		t.Errorf("file %q", text)
	}
	if slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "abc") }) {
		t.Error("a cookie on the command line")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(file.Path()); fi.Mode().Perm() != 0o600 {
			t.Errorf("cookie file mode %v", fi.Mode().Perm())
		}
	}
	file.Remove()
	if _, err := os.Stat(file.Path()); err == nil {
		t.Error("not removed once done")
	}
	if args, file, _ := (FromBrowser{}).Args("https://x.test/"); len(args) != 0 || file != nil {
		t.Error("nothing from the browser, yet arguments")
	}
}

// The button over a YouTube video sends the watch page with the cookies the
// browser has for it. An older extension sends them as a header, without
// their domains: they are filed for youtube.com and its subdomains, so they
// reach www.youtube.com, the host yt-dlp asks, from a Music or mobile page
// too.
func TestAYouTubePagesCookiesReachTheHostYtDlpAsks(t *testing.T) {
	b := FromBrowser{Cookies: "VISITOR_INFO1_LIVE=v; SAPISID=s/1; __Secure-3PAPISID=s3; __Secure-3PSID=p3; LOGIN_INFO=AFmmF2s:QUQ3; __Host-GAPS=1:g"}
	args, file, err := b.Args("https://www.youtube.com/watch?v=A1WG3x-Ebpk")
	if err != nil || args[0] != "--cookies" {
		t.Fatalf("%q %v", args, err)
	}
	defer file.Remove()
	text := readFile(t, file)
	for _, nv := range [][2]string{{"VISITOR_INFO1_LIVE", "v"}, {"SAPISID", "s/1"}, {"__Secure-3PAPISID", "s3"},
		{"__Secure-3PSID", "p3"}, {"LOGIN_INFO", "AFmmF2s:QUQ3"}, {"__Host-GAPS", "1:g"}} {
		if line := ".youtube.com\tTRUE\t/\tFALSE\t0\t" + nv[0] + "\t" + nv[1] + "\n"; !strings.Contains(text, line) {
			t.Errorf("%q not in %q", line, text)
		}
	}
	if n := strings.Count(text, "\n"); n != 7 {
		t.Errorf("%d lines, want the header and six cookies", n)
	}
	for _, page := range []string{"https://music.youtube.com/watch?v=A1WG3x-Ebpk", "https://m.youtube.com/watch?v=A1WG3x-Ebpk", "https://youtube.com/shorts/A1WG3x-Ebpk"} {
		_, f, _ := b.Args(page)
		if !strings.Contains(readFile(t, f), ".youtube.com\tTRUE\t/\tFALSE\t0\tSAPISID\ts/1\n") {
			t.Errorf("%s", page)
		}
		f.Remove()
	}
}

func TestAHeaderGoesToTheSitesOwnDomain(t *testing.T) {
	for host, want := range map[string]string{
		"www.youtube.com": "youtube.com", "music.youtube.com": "youtube.com", "m.youtube.com": "youtube.com",
		"youtube.com": "youtube.com", "Music.YouTube.com.": "YouTube.com", "m.facebook.com": "facebook.com",
		"mobile.twitter.com": "twitter.com", "m.vk.com": "vk.com", "player.test": "player.test",
		"www.example.org": "www.example.org", "notyoutube.com": "notyoutube.com",
		"youtube.com.evil.test": "youtube.com.evil.test", "m.org": "m.org", "127.0.0.1": "127.0.0.1",
	} {
		if got := headerDomain(host); got != want {
			t.Errorf("%s: %s", host, got)
		}
	}
}

func cookie(name, domain string, hostOnly bool) ipc.BrowserCookie {
	return ipc.BrowserCookie{Name: name, Value: name + "-v", Domain: domain, HostOnly: hostOnly, Path: "/"}
}

// A newer extension sends each cookie with the domain, path and flags the
// browser keeps: the file has each as it is, and the header is left out.
func TestEachCookieKeepsItsDomainAndFlags(t *testing.T) {
	exp := 1893456000.75
	sid := cookie("SID", ".youtube.com", false)
	sid.Secure, sid.Expires = true, &exp
	acct := cookie("acct", "youtube.com", false) // without a leading dot, as Firefox may name it
	acct.Path = "/account"
	odd := cookie("odd", "youtube.com", true) // a path that isn't one: the whole site
	odd.Path = "account"
	line := cookie("line", ".youtube.com", false)
	line.Value = "a\nb"
	b := FromBrowser{Cookies: "SID=SID-v; pref=pref-v", Jar: []ipc.BrowserCookie{
		sid, cookie("pref", "music.youtube.com", true), acct, odd,
		// Never a field that would shift the others, an empty name or domain.
		cookie("tab\tname", ".youtube.com", false), line, cookie("", ".youtube.com", false),
		cookie("nodomain", "", false), cookie("dots", "...", false), cookie("slash", "youtube.com/x", false),
	}}
	args, file, err := b.Args("https://music.youtube.com/watch?v=x")
	if err != nil || args[0] != "--cookies" {
		t.Fatalf("%q %v", args, err)
	}
	defer file.Remove()
	want := "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tSID-v\n" +
		"music.youtube.com\tFALSE\t/\tFALSE\t0\tpref\tpref-v\n" +
		".youtube.com\tTRUE\t/account\tFALSE\t0\tacct\tacct-v\n" +
		"youtube.com\tFALSE\t/\tFALSE\t0\todd\todd-v\n"
	if got := readFile(t, file); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	// A page with no address of its own still gets the jar's.
	if _, f, _ := b.Args("not a url"); f == nil {
		t.Error("no file for a page without an address")
	} else {
		f.Remove()
	}
}

func yt(t *testing.T, chosen *BrowserCookies, ours ...string) []string {
	t.Helper()
	out, err := layers(nil, ours, chosen)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Options → Cookies for video sites. Off: nothing added. On: every run
// without cookies of its own reads the browser's, with yt-dlp's warnings
// kept on; a run with the extension's cookies keeps those alone.
func TestTheBrowsersCookiesOnlyWhenTheExtensionSentNone(t *testing.T) {
	if got := yt(t, nil, "-J", "--no-warnings"); !slices.Equal(got, []string{"-J", "--no-warnings"}) {
		t.Errorf("off: %q", got)
	}
	firefox, _ := NewBrowserCookies("Firefox", "")
	if firefox.Spec() != "firefox" {
		t.Error(firefox.Spec())
	}
	if got := yt(t, firefox, "-J", "--no-warnings"); !slices.Equal(got, []string{"-J", "--cookies-from-browser", "firefox"}) {
		t.Errorf("on: %q", got)
	}
	if got := yt(t, firefox, "-J", "--cookies", "/tmp/c.txt"); !slices.Equal(got, []string{"-J", "--cookies", "/tmp/c.txt"}) {
		t.Errorf("the extension's cookies should win: %q", got)
	}
	named, _ := NewBrowserCookies("chrome", " Profile 1 ")
	if named.Spec() != "chrome:Profile 1" {
		t.Error(named.Spec())
	}
	fork, _ := NewBrowserCookies("chromium", `C:\Users\a\AppData\Local\Helium\User Data\Default`)
	if got := yt(t, fork, "-f", "b"); !slices.Equal(got, []string{"-f", "b", "--cookies-from-browser", `chromium:C:\Users\a\AppData\Local\Helium\User Data\Default`}) {
		t.Errorf("fork: %q", got)
	}
	// Only the browsers yt-dlp reads; Off is no browser.
	if _, ok := NewBrowserCookies("helium", ""); ok {
		t.Error("helium")
	}
	if _, ok := NewBrowserCookies("", "x"); ok {
		t.Error("no browser")
	}
	// A profile with a line break never reaches yt-dlp.
	bad, _ := NewBrowserCookies("firefox", "a\nb")
	if _, err := layers(nil, nil, bad); !errors.Is(err, ErrBadArgument) {
		t.Errorf("a line break: %v", err)
	}
	if slices.Contains(CookieBrowsersHere(), "safari") != (runtime.GOOS == "darwin") {
		t.Error("Safari")
	}
}

// yt-dlp is told of Node.js or Bun, from the PATH or nvm, and left alone
// with Deno.
func TestTheJavaScriptRuntimeYtDlpIsToldOf(t *testing.T) {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	touch := func(dir, name string) {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, name+exe), nil, 0o755)
	}
	root := t.TempDir()
	path, home := filepath.Join(root, "bin"), filepath.Join(root, "home")
	_ = os.MkdirAll(path, 0o755)
	dirs := []string{path}
	if _, _, ok := jsRuntime(dirs, home); ok {
		t.Error("nothing there")
	}
	for _, v := range []string{"v20.1.0", "v22.3.0"} {
		touch(filepath.Join(home, ".nvm", "versions", "node", v, "bin"), "node")
	}
	if name, found, ok := jsRuntime(dirs, home); !ok || name != "node" || !strings.Contains(found, "v22.3.0") {
		t.Errorf("nvm: %s %s", name, found)
	}
	touch(path, "bun")
	if name, _, _ := jsRuntime(dirs, ""); name != "bun" {
		t.Errorf("bun: %s", name)
	}
	touch(path, "node")
	if _, found, _ := jsRuntime(dirs, ""); found != filepath.Join(path, "node"+exe) {
		t.Errorf("node: %s", found)
	}
	touch(path, "deno")
	if _, _, ok := jsRuntime(dirs, home); ok {
		t.Error("deno is yt-dlp's own")
	}
}

// The second look-up has no cookies of any kind, and everything else the
// first had.
func TestALookUpWithoutCookiesKeepsTheRest(t *testing.T) {
	ours := []string{"-J", "--cookies", "/tmp/c.txt", "--referer", "https://p.test/"}
	if got := withoutCookies(ours); !slices.Equal(got, []string{"-J", "--referer", "https://p.test/", noCookies}) {
		t.Errorf("without: %q", got)
	}
	firefox, _ := NewBrowserCookies("firefox", "")
	if !withCookies(ours, nil) || !withCookies([]string{"-J"}, firefox) || withCookies([]string{"-J"}, nil) {
		t.Error("withCookies")
	}
	// Told so, the chosen browser's cookies aren't read either.
	if got := yt(t, firefox, "-J", noCookies); !slices.Equal(got, []string{"-J", noCookies}) {
		t.Errorf("told so: %q", got)
	}
}

// Why the browser's cookies didn't help stays on the failure's message.
func TestTheCookieTroubleIsKeptOnTheFailure(t *testing.T) {
	tail := []string{
		"[youtube] Extracting URL: https://www.youtube.com/watch?v=x",
		"Extracted 3 cookies from chrome (41 could not be decrypted)",
		"ERROR: [youtube] x: Sign in to confirm you're not a bot.",
	}
	got := withCookieTrouble("[youtube] x: Sign in to confirm you're not a bot.", []string{"unknown cookie version: \"b'v20'\""}, tail)
	if got != "[youtube] x: Sign in to confirm you're not a bot. (unknown cookie version: \"b'v20'\")" {
		t.Errorf("got %q", got)
	}
	copyFail := []string{
		"ERROR: Could not copy Chrome cookie database. See  https://github.com/yt-dlp/yt-dlp/issues/7271  for more info",
		"ERROR: failed to load cookies",
	}
	if got := withCookieTrouble("failed to load cookies", nil, copyFail); !strings.Contains(got, "Could not copy Chrome cookie database") {
		t.Errorf("got %q", got)
	}
	if got := withCookieTrouble("Video unavailable", []string{"a warning"}, nil); got != "Video unavailable" {
		t.Errorf("got %q", got)
	}
}

func TestHostileTextNeverReachesTheArgumentList(t *testing.T) {
	if _, err := YtDlpCmd("yt-dlp", nil, nil, "https://x/\n--exec calc"); !errors.Is(err, ErrBadArgument) {
		t.Error("a line break in the address")
	}
	if _, err := YtDlpCmd("yt-dlp", []string{"--format\x00best"}, nil, "https://x/"); !errors.Is(err, ErrBadArgument) {
		t.Error("a NUL in the user's extras")
	}
	if _, err := GalleryDlCmd(Installed{Exe: "gallery-dl", Version: "1.30.0"}, nil, "https://x/\r"); !errors.Is(err, ErrBadArgument) {
		t.Error("gallery-dl")
	}
}

func TestFixedArgumentsComeLastAndTheURLAfterTheSeparator(t *testing.T) {
	cmd, err := YtDlpCmd("yt-dlp", []string{"--user"}, []string{"-J"}, "-https://x/")
	if err != nil {
		t.Fatal(err)
	}
	args := slices.Clone(cmd.Args[1:])
	// This computer's Node.js or Bun, if it has one; tested on its own.
	if i := slices.Index(args, "--js-runtimes"); i >= 0 {
		args = slices.Delete(args, i, i+2)
	}
	want := []string{"--user", "-J", "--ignore-config", "--no-exec", "--no-update", "--encoding", "utf-8", "--", "-https://x/"}
	if !slices.Equal(args, want) {
		t.Errorf("got %q", args)
	}
	g, _ := GalleryDlCmd(Installed{Exe: "gallery-dl", Version: "1.26.9"}, []string{"-E"}, "https://x/")
	if !slices.Equal(g.Args[1:], []string{"-E", "--ignore-config", "--", "https://x/"}) {
		t.Errorf("gallery-dl 1.26.9: %q", g.Args)
	}
	g, _ = GalleryDlCmd(Installed{Exe: "gallery-dl", Version: "1.30.0"}, nil, "https://x/")
	if !slices.Contains(g.Args, "--no-input") {
		t.Errorf("gallery-dl 1.30.0: %q", g.Args)
	}
}

// The app's own proxy reaches the helpers through their environment, never
// their command line.
func TestTheProxyGoesThroughTheEnvironment(t *testing.T) {
	SetProxy("http://user:secret@proxy.test:3128")
	defer SetProxy("")
	cmd, err := YtDlpCmd("yt-dlp", nil, nil, "https://x/")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(cmd.Args, func(a string) bool { return strings.Contains(a, "secret") }) {
		t.Error("the proxy on the command line")
	}
	if !slices.Contains(cmd.Env, "HTTPS_PROXY=http://user:secret@proxy.test:3128") || !slices.Contains(cmd.Env, "PYTHONIOENCODING=utf-8") {
		t.Error("not in the environment")
	}
}

func selfAs(t *testing.T, mode string, extra ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(append(os.Environ(), fakeModeEnv+"="+mode), extra...)
	return cmd
}

func TestOutputAndFailuresOfARealProcess(t *testing.T) {
	ctx := context.Background()
	cmd := selfAs(t, "args")
	cmd.Args = append(cmd.Args, "hello")
	if out, err := outputCmd(ctx, cmd, "fake", 10*time.Second); err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("output %q %v", out, err)
	}
	_, err := outputCmd(ctx, selfAs(t, "fail"), "fake", 10*time.Second)
	var failed *FailedError
	if !errors.As(err, &failed) || failed.Message != "[youtube] x: Sign in to confirm you're not a bot. (unknown cookie version: \"b'v20'\")" {
		t.Errorf("failure %v", err)
	}
	start := time.Now()
	if _, err := outputCmd(ctx, selfAs(t, "sleep"), "fake", 500*time.Millisecond); !errors.Is(err, ErrTimeout) || time.Since(start) > 10*time.Second {
		t.Errorf("timeout %v after %v", err, time.Since(start))
	}
}

// Killing a helper ends what it started too (yt-dlp.exe is a launcher whose
// child downloads): once killed, the child's file stops growing.
func TestKillingAHelperEndsItsChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker.txt")
	p, err := Spawn(selfAs(t, "parent", "YELLOWFLASH_FAKE_MARKER="+marker), "fake")
	if err != nil {
		t.Fatal(err)
	}
	size := func() int64 {
		fi, err := os.Stat(marker)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	deadline := time.Now().Add(10 * time.Second)
	for size() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if size() == 0 {
		t.Fatal("the child never ran")
	}
	p.Kill()
	_ = p.Wait()
	time.Sleep(500 * time.Millisecond)
	after := size()
	time.Sleep(time.Second)
	if size() != after {
		t.Error("the child outlived the helper")
	}
}
