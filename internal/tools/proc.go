// Ported from Mister Download Manager, crates/tools/src/proc.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// Supervised helper processes (yt-dlp, gallery-dl): every child runs
// without a console window and can be ended with everything it started
// (Windows: a Job Object; elsewhere its process group), gets its arguments
// in fixed layers (the user's extras, ours, then the fixed ones and -- before
// the URL) with CR / LF / NUL refused, and keeps only a short stderr tail
// for messages. MDM's relay through a chosen network adapter is not ported;
// the app's own fixed proxy reaches the helpers through the environment, never
// their command line.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
)

const (
	// tailLines is how many stderr lines are kept for error messages.
	tailLines = 20
	// warningsKept is how many WARNING: lines a run keeps.
	warningsKept = 50
	// waitDelay is how long a run's last output is waited for once it has
	// ended (something it started may hold stderr open).
	waitDelay = 2 * time.Second
)

var (
	// ErrBadArgument: an argument contains a line break or NUL.
	ErrBadArgument = errors.New("an argument contains a line break or NUL")
	// ErrTimeout: a helper didn't finish in time and was stopped.
	ErrTimeout = errors.New("the helper took too long and was stopped")
)

// FailedError is a helper that ended with a failure, in its own words.
type FailedError struct {
	Tool, Message string
}

func (e *FailedError) Error() string { return e.Tool + " failed: " + e.Message }

// shared holds what the app sets for every helper run.
var shared struct {
	mu      sync.RWMutex
	cookies *BrowserCookies
	proxy   string
}

// SetProxy makes the helpers go through proxyURL (the app's own fixed
// proxy); "" leaves them the system's proxy settings. It reaches them
// through the environment, where other programs can't read it.
func SetProxy(proxyURL string) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	shared.proxy = strings.TrimSpace(proxyURL)
}

// CookieBrowsers are the browsers yt-dlp reads cookies from
// (--cookies-from-browser), in the order Options lists them. Safari only on
// macOS.
var CookieBrowsers = []string{"firefox", "chrome", "edge", "brave", "opera", "vivaldi", "chromium", "whale", "safari"}

// CookieBrowsersHere are the browsers this system offers.
func CookieBrowsersHere() []string {
	var out []string
	for _, b := range CookieBrowsers {
		if b != "safari" || runtime.GOOS == "darwin" {
			out = append(out, b)
		}
	}
	return out
}

// BrowserCookies is the browser whose cookies yt-dlp reads on this
// computer, and its profile (a name, or a profile folder: a Chromium fork's
// chromium:<its profile folder>), for the runs the extension passed no
// cookies to.
type BrowserCookies struct {
	Browser, Profile string
}

// NewBrowserCookies is browser (one of CookieBrowsers) and its profile;
// false for any other name.
func NewBrowserCookies(browser, profile string) (*BrowserCookies, bool) {
	b := strings.ToLower(strings.TrimSpace(browser))
	if b == "" || !slices.Contains(CookieBrowsers, b) {
		return nil, false
	}
	return &BrowserCookies{Browser: b, Profile: strings.TrimSpace(profile)}, true
}

// Spec is yt-dlp's BROWSER[:PROFILE].
func (c *BrowserCookies) Spec() string {
	if c.Profile != "" {
		return c.Browser + ":" + c.Profile
	}
	return c.Browser
}

// SetBrowserCookies sets Options' cookies for video sites (nil: off).
func SetBrowserCookies(c *BrowserCookies) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	shared.cookies = c
}

func browserCookies() *BrowserCookies {
	shared.mu.RLock()
	defer shared.mu.RUnlock()
	return shared.cookies
}

// noCookies is yt-dlp's "no cookies from any browser": a run told this gets
// none of the chosen browser's.
const noCookies = "--no-cookies-from-browser"

// browserCookieArgs are the cookies a yt-dlp run with args gets from the
// chosen browser: none when the run has cookies already, else
// --cookies-from-browser BROWSER[:PROFILE]. The extension's cookies are the
// very page's, from its window and profile, as the browser sent them a
// moment ago: they win, and the browser's store is read only when nothing
// came (a pasted address, a playlist listed later).
func browserCookieArgs(args []string, chosen *BrowserCookies) []string {
	settled := slices.ContainsFunc(args, func(a string) bool {
		return a == "--cookies" || a == "--cookies-from-browser" || a == noCookies
	})
	if chosen == nil || settled {
		return nil
	}
	return []string{"--cookies-from-browser", chosen.Spec()}
}

// withoutCookies is ours for a look-up without cookies: the cookie file
// taken out, the browser's store refused.
func withoutCookies(ours []string) []string {
	out := make([]string, 0, len(ours)+1)
	for i := 0; i < len(ours); i++ {
		if ours[i] == "--cookies" || ours[i] == "--cookies-from-browser" {
			i++
			continue
		}
		out = append(out, ours[i])
	}
	return append(out, noCookies)
}

// withCookies reports a run with arguments ours that has cookies (its own,
// or the chosen browser's).
func withCookies(ours []string, chosen *BrowserCookies) bool {
	return chosen != nil || slices.ContainsFunc(ours, func(a string) bool {
		return a == "--cookies" || a == "--cookies-from-browser"
	})
}

// LookedUp is a page's -J output, and whether it took looking the page up
// without cookies.
type LookedUp struct {
	JSON           []byte
	WithoutCookies bool
}

// LookUp looks a page up (yt-dlp -J, ours holding its arguments). -J
// chooses a format too, and fails when none is left; signed-in cookies make
// that likely on YouTube (only players needing a JavaScript runtime are
// asked). The same page without the cookies lists its qualities, so a
// look-up that fails this way and had cookies is made once more without
// them; if that fails too, the first error is the one told.
func LookUp(ctx context.Context, exe string, ours []string, pageURL string, timeout time.Duration) (LookedUp, error) {
	run := func(args []string) ([]byte, error) {
		cmd, err := YtDlpCmd(exe, nil, args, pageURL)
		if err != nil {
			return nil, err
		}
		return outputCmd(ctx, cmd, "yt-dlp", timeout)
	}
	out, first := run(ours)
	if first == nil {
		return LookedUp{JSON: out}, nil
	}
	if withCookies(ours, browserCookies()) && FormatUnavailable(first.Error()) {
		if out, err := run(withoutCookies(ours)); err == nil {
			return LookedUp{JSON: out, WithoutCookies: true}, nil
		}
	}
	return LookedUp{}, first
}

// cookieTrouble reports a WARNING: (or ERROR:) line saying yt-dlp couldn't
// read the browser's cookies.
func cookieTrouble(line string) bool {
	l := strings.ToLower(line)
	for _, k := range []string{"cookie database", "cookies database", "decrypt", "unknown cookie version",
		"keyring", "secretstorage", "kwallet", "local state", "failed to load cookies"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// safe refuses text with line breaks or NUL: they would let a page title or
// URL smuggle in options or split a line-based protocol.
func safe(s string) error {
	if strings.ContainsAny(s, "\r\n\x00") {
		return ErrBadArgument
	}
	return nil
}

// FromBrowser is what the browser's request for a page carried, so yt-dlp
// asks as the browser did: the page that embeds a player (Referer), the
// browser's User-Agent, and the page's cookies: each with its domain and
// flags (Jar), or, from an older extension, a Cookie header (Cookies).
type FromBrowser struct {
	Referer, UserAgent, Cookies string
	Jar                         []ipc.BrowserCookie
}

// CookieFile is a private cookie file in the Netscape format yt-dlp's
// --cookies reads. Remove it once the run has ended (yt-dlp may write it
// back at its end).
type CookieFile struct{ path string }

// Path is where the file is.
func (f *CookieFile) Path() string { return f.path }

// Remove deletes the file; a nil file is fine.
func (f *CookieFile) Remove() {
	if f != nil {
		_ = os.Remove(f.path)
	}
}

// Args are yt-dlp's arguments for pageURL's page, and the cookie file they
// name. Cookies never go on the command line, where other programs can read
// them.
func (b FromBrowser) Args(pageURL string) ([]string, *CookieFile, error) {
	var args []string
	if r := strings.TrimSpace(b.Referer); r != "" {
		if err := safe(r); err != nil {
			return nil, nil, err
		}
		args = append(args, "--referer", r)
	}
	if ua := strings.TrimSpace(b.UserAgent); ua != "" {
		if err := safe(ua); err != nil {
			return nil, nil, err
		}
		args = append(args, "--user-agent", ua)
	}
	var text string
	switch {
	case len(b.Jar) > 0:
		text = jarCookies(b.Jar)
	case strings.TrimSpace(b.Cookies) != "":
		u, err := url.Parse(pageURL)
		if err != nil || u.Hostname() == "" {
			return args, nil, nil
		}
		text = headerCookies(u.Hostname(), strings.TrimSpace(b.Cookies))
	default:
		return args, nil, nil
	}
	f, err := cookieFile(text)
	if err != nil {
		return nil, nil, fmt.Errorf("could not start yt-dlp: %w", err)
	}
	return append(args, "--cookies", f.path), f, nil
}

// netscape is a cookie file's first line. Each cookie is a line: domain,
// sent to its subdomains too (TRUE, FALSE), path, HTTPS only, expiry
// (seconds since 1970; 0: a session cookie), name, value, separated by tabs.
const netscape = "# Netscape HTTP Cookie File\n"

// field reports text that can go in a cookie file's field (no tab or line
// break to shift the others).
func field(s string) bool { return !strings.ContainsAny(s, "\t\r\n\x00") }

func flag(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

// jarCookies files the browser's cookies, each for its own domain, path and
// flags.
func jarCookies(jar []ipc.BrowserCookie) string {
	var b strings.Builder
	b.WriteString(netscape)
	for _, c := range jar {
		host := strings.TrimLeft(c.Domain, ".")
		if c.Name == "" || host == "" || strings.ContainsAny(host, " /") ||
			!field(c.Name) || !field(c.Value) || !field(c.Domain) || !field(c.Path) {
			continue
		}
		domain := "." + host
		if c.HostOnly {
			domain = host
		}
		path := "/"
		if strings.HasPrefix(c.Path, "/") {
			path = c.Path
		}
		var expires uint64
		if c.Expires != nil && *c.Expires >= 1 && *c.Expires < 1e15 {
			expires = uint64(*c.Expires)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", domain, flag(!c.HostOnly), path, flag(c.Secure), expires, c.Name, c.Value)
	}
	return b.String()
}

// headerCookies files an older extension's Cookie header (a=1; b=2) for the
// site's domain and its subdomains (headerDomain), as session cookies.
func headerCookies(host, cookies string) string {
	domain := headerDomain(host)
	var b strings.Builder
	b.WriteString(netscape)
	for _, pair := range strings.Split(cookies, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || !field(domain) || !field(name) || !field(value) {
			continue
		}
		fmt.Fprintf(&b, ".%s\tTRUE\t/\tFALSE\t0\t%s\t%s\n", domain, name, value)
	}
	return b.String()
}

// headerDomain is where a page's cookies without their domains go: its host,
// or the site's own domain where yt-dlp asks another of its hosts: every
// YouTube host (yt-dlp looks videos up on www.youtube.com, and the sign-in
// cookies are .youtube.com's), and a site's mobile pages (m., mobile.).
func headerDomain(host string) string {
	host = strings.TrimRight(host, ".")
	const youtube = "youtube.com"
	lower := strings.ToLower(host)
	if lower == youtube || strings.HasSuffix(lower, "."+youtube) {
		return host[len(host)-len(youtube):]
	}
	for _, mobile := range []string{"m.", "mobile."} {
		if site, ok := strings.CutPrefix(host, mobile); ok && strings.Contains(site, ".") {
			return site
		}
	}
	return host
}

var cookieFiles atomic.Uint64

// cookieFile writes text into a file only this user can read.
func cookieFile(text string) (*CookieFile, error) {
	name := fmt.Sprintf("yellowflash-cookies-%d-%d.txt", os.Getpid(), cookieFiles.Add(1))
	path := filepath.Join(os.TempDir(), name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &CookieFile{path}, nil
}

// layers are the user's extras and ours, then the chosen browser's cookies
// when the run has none. Reading them, yt-dlp's warnings stay on (a
// look-up's --no-warnings is dropped): the one saying the browser's cookies
// couldn't be decrypted explains the site's "sign in" that follows.
func layers(user, ours []string, chosen *BrowserCookies) ([]string, error) {
	all := append(slices.Clone(user), ours...)
	fromStore := browserCookieArgs(all, chosen)
	var out []string
	for _, a := range all {
		if len(fromStore) == 0 || a != "--no-warnings" {
			out = append(out, a)
		}
	}
	out = append(out, fromStore...)
	for _, a := range out {
		if err := safe(a); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// programIn is the file named name (name.exe on Windows) in one of dirs.
func programIn(dirs []string, name string) (string, bool) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, d := range dirs {
		if p := filepath.Join(d, name); d != "" && isFile(p) {
			return p, true
		}
	}
	return "", false
}

// nvmNode is Node.js from nvm (~/.nvm/versions/node/<version>/bin/node),
// the newest name first: a program started from the desktop doesn't have
// nvm's folders on its PATH.
func nvmNode(home string) (string, bool) {
	entries, err := os.ReadDir(filepath.Join(home, ".nvm", "versions", "node"))
	if err != nil {
		return "", false
	}
	var dirs []string
	for _, e := range entries {
		dirs = append(dirs, filepath.Join(home, ".nvm", "versions", "node", e.Name(), "bin"))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	return programIn(dirs, "node")
}

// jsRuntime is what yt-dlp should be told about JavaScript: YouTube's
// formats need a runtime, and yt-dlp looks for Deno alone unless told of
// another. Deno found: nothing to say. Else Node.js or Bun, if there is one.
func jsRuntime(dirs []string, home string) (name, path string, ok bool) {
	if _, found := programIn(dirs, "deno"); found {
		return "", "", false
	}
	if p, found := programIn(dirs, "node"); found {
		return "node", p, true
	}
	if home != "" {
		if p, found := nvmNode(home); found {
			return "node", p, true
		}
	}
	if p, found := programIn(dirs, "bun"); found {
		return "bun", p, true
	}
	return "", "", false
}

// jsRuntimeArgs are --js-runtimes for this computer's Node.js or Bun, looked
// for once.
var jsRuntimeArgs = sync.OnceValue(func() []string {
	home, _ := os.UserHomeDir()
	if name, path, ok := jsRuntime(filepath.SplitList(os.Getenv("PATH")), home); ok {
		return []string{"--js-runtimes", name + ":" + path}
	}
	return nil
})

// utf8Args make yt-dlp's own output UTF-8; PYTHONIOENCODING does Python's.
// On a Windows with a legacy code page Python writes to a pipe in that code
// page, and a title's letters made the final path unreadable.
var utf8Args = []string{"--encoding", "utf-8"}

// helperEnv is the environment a helper runs with: UTF-8 output, and the
// app's proxy for it and everything it starts.
func helperEnv() []string {
	env := append(os.Environ(), "PYTHONIOENCODING=utf-8")
	shared.mu.RLock()
	proxy := shared.proxy
	shared.mu.RUnlock()
	if proxy != "" {
		for _, v := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
			env = append(env, v+"="+proxy)
		}
	}
	return env
}

// fixedArgs come after the user's and ours, so nothing can switch them off.
func fixedArgs() []string {
	args := []string{"--ignore-config", "--no-exec", "--no-update"}
	args = append(args, jsRuntimeArgs()...)
	return append(args, utf8Args...)
}

// YtDlpCmd is yt-dlp with arguments in layers: user extras, then ours (then
// the chosen browser's cookies when neither brought any), then the fixed
// ones last, then -- and the URL (so a URL starting with - is never read as
// an option).
func YtDlpCmd(exe string, user, ours []string, pageURL string) (*exec.Cmd, error) {
	args, err := layers(user, ours, browserCookies())
	if err != nil {
		return nil, err
	}
	if err := safe(pageURL); err != nil {
		return nil, err
	}
	args = append(append(args, fixedArgs()...), "--", pageURL)
	cmd := exec.Command(exe, args...)
	cmd.Env = helperEnv()
	return cmd, nil
}

// YtDlpInfoCmd is yt-dlp working from a looked-up page (-J output, maybe
// edited) instead of an address: the same layers, then --load-info-json and
// the file.
func YtDlpInfoCmd(exe string, user, ours []string, info string) (*exec.Cmd, error) {
	args, err := layers(user, ours, browserCookies())
	if err != nil {
		return nil, err
	}
	if err := safe(info); err != nil {
		return nil, err
	}
	args = append(append(args, fixedArgs()...), "--load-info-json", info)
	cmd := exec.Command(exe, args...)
	cmd.Env = helperEnv()
	return cmd, nil
}

// GalleryDlCmd is gallery-dl with arguments in the same layers: ours, then
// the fixed ones (no configuration file of the person's, never a question
// on the console when the release takes --no-input), then -- and the
// address.
func GalleryDlCmd(tool Installed, ours []string, pageURL string) (*exec.Cmd, error) {
	args := slices.Clone(ours)
	args = append(args, "--ignore-config")
	if GalleryAcceptsOf(tool.Version).NoInput {
		args = append(args, "--no-input")
	}
	args = append(args, "--", pageURL)
	for _, a := range args {
		if err := safe(a); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(tool.Exe, args...)
	cmd.Env = helperEnv()
	return cmd, nil
}

// stderrLines keeps a run's last stderr lines and its warnings.
type stderrLines struct {
	mu       sync.Mutex
	partial  []byte
	tail     []string
	warnings []string
}

func (s *stderrLines) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, b...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		s.add(strings.TrimRight(string(s.partial[:i]), "\r"))
		s.partial = s.partial[i+1:]
	}
	// A line without its end, held to a bound.
	if len(s.partial) > 64<<10 {
		s.add(string(s.partial))
		s.partial = nil
	}
	return len(b), nil
}

func (s *stderrLines) add(line string) {
	if w, ok := strings.CutPrefix(line, "WARNING:"); ok && len(s.warnings) < warningsKept {
		s.warnings = append(s.warnings, strings.TrimSpace(w))
	}
	if len(s.tail) >= tailLines {
		s.tail = s.tail[1:]
	}
	s.tail = append(s.tail, line)
}

func (s *stderrLines) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.partial) > 0 {
		s.add(strings.TrimRight(string(s.partial), "\r"))
		s.partial = nil
	}
}

// Proc is a running helper with its stderr tail.
type Proc struct {
	cmd    *exec.Cmd
	name   string
	stderr *stderrLines
	tree   *processTree
	// Stdout is the helper's standard output: read it to its end before
	// Wait.
	Stdout io.Reader
}

// Spawn starts cmd supervised: no window, ended with all it starts, its
// stdout piped for the caller, stderr kept as a tail.
func Spawn(cmd *exec.Cmd, name string) (*Proc, error) {
	p := &Proc{cmd: cmd, name: name, stderr: &stderrLines{}}
	cmd.Stdin = nil
	cmd.Stderr = p.stderr
	cmd.WaitDelay = waitDelay
	if cmd.Stdout == nil {
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		p.Stdout = out
	}
	platform.HideConsole(cmd)
	prepareTree(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start %s: %w", name, err)
	}
	p.tree = adoptTree(cmd)
	return p, nil
}

// Kill ends the helper and everything it started.
func (p *Proc) Kill() { p.tree.kill(p.cmd) }

// Wait waits for the helper to end; a failure carries its words
// (Failure).
func (p *Proc) Wait() error {
	err := p.cmd.Wait()
	p.stderr.flush()
	p.tree.release()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return p.Failure()
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	return err
}

// Tail is the last stderr lines, for an error message.
func (p *Proc) Tail() string {
	p.stderr.mu.Lock()
	defer p.stderr.mu.Unlock()
	return strings.Join(p.stderr.tail, "\n")
}

// Warnings are the tool's WARNING: lines so far, oldest first, without the
// word.
func (p *Proc) Warnings() []string {
	p.stderr.mu.Lock()
	defer p.stderr.mu.Unlock()
	return slices.Clone(p.stderr.warnings)
}

// Failure is a failure with what the tool said: its last ERROR: line, else
// its last line, with what it said earlier about the browser's cookies.
func (p *Proc) Failure() error {
	p.stderr.mu.Lock()
	defer p.stderr.mu.Unlock()
	tail := p.stderr.tail
	message := ""
	for i := len(tail) - 1; i >= 0 && message == ""; i-- {
		if rest, ok := strings.CutPrefix(tail[i], "ERROR:"); ok {
			message = strings.TrimSpace(rest)
		}
	}
	if message == "" && len(tail) > 0 {
		message = strings.TrimSpace(tail[len(tail)-1])
	}
	if message == "" {
		message = "it stopped without saying why"
	}
	return &FailedError{Tool: p.name, Message: withCookieTrouble(message, p.stderr.warnings, p.stderr.tail)}
}

// withCookieTrouble adds to a failure's message what yt-dlp said earlier
// about the browser's cookies, when that isn't the message itself: Chrome's
// cookies it couldn't decrypt are only a warning, and the site's "sign in"
// that follows is the error.
func withCookieTrouble(message string, warnings, tail []string) string {
	earlier := slices.Clone(warnings)
	for _, l := range tail {
		l = strings.TrimPrefix(l, "ERROR:")
		l = strings.TrimPrefix(l, "WARNING:")
		earlier = append(earlier, strings.TrimSpace(l))
	}
	for _, why := range earlier {
		if cookieTrouble(why) && why != message && !strings.Contains(message, why) {
			return message + " (" + why + ")"
		}
	}
	return message
}

// outputCmd runs cmd to the end within timeout and returns its stdout
// (killed on timeout or when ctx ends).
func outputCmd(ctx context.Context, cmd *exec.Cmd, name string, timeout time.Duration) ([]byte, error) {
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	p, err := Spawn(cmd, name)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		return stdout.Bytes(), nil
	case <-timer.C:
		p.Kill()
		<-done
		return nil, fmt.Errorf("%w: %s", ErrTimeout, name)
	case <-ctx.Done():
		p.Kill()
		<-done
		return nil, ctx.Err()
	}
}

// output runs exe with args to the end within timeout and returns its
// standard output.
func output(ctx context.Context, exe string, args []string, timeout time.Duration) ([]byte, error) {
	return outputCmd(ctx, exec.Command(exe, args...), filepath.Base(exe), timeout)
}
