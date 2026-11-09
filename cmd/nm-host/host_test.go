// Ported from Mister Download Manager, crates/nmhost/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
)

const chromeUA = "Mozilla/5.0 (Windows NT 10.0) Chrome/141.0.0.0 Safari/537.36"

// testHost records what would reach the app and the log.
func testHost() (*host, *[]ipc.Command, *[]string) {
	var sent []ipc.Command
	var logged []string
	h := &host{
		deliver: func(c ipc.Command) Reply { sent = append(sent, c); return done() },
		log:     func(l string) { logged = append(logged, l) },
	}
	return h, &sent, &logged
}

func parse(t *testing.T, raw string) Message {
	t.Helper()
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The extension loaded unpacked and each store's copy connect, and both are
// served: the manifest's allowed_origins and the host's caller check agree.
func TestOnlyOurExtensionInEitherBrowser(t *testing.T) {
	manifest := chromiumManifest(`C:\x\yellowflash-nmhost.exe`)
	origins := manifest["allowed_origins"].([]string)
	for _, id := range browser.ChromiumIDs() {
		o := "chrome-extension://" + id + "/"
		if !slices.Contains(origins, o) || !ourCaller([]string{o}) {
			t.Errorf("%s not served", o)
		}
	}
	if !ourCaller([]string{`C:\x\host.firefox.json`, browser.FirefoxID}) {
		t.Error("Firefox's add-on")
	}
	for _, args := range [][]string{
		{"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"},
		{"chrome-extension://oogbclbbninliongijffeeggalppliok/"}, // MDM's
		{`C:\x\host.json`, "evil@example.com"},
		nil,
	} {
		if ourCaller(args) {
			t.Errorf("served %q", args)
		}
	}
	ff := firefoxManifest("/usr/bin/yellowflash-nmhost")
	if ff["name"] != browser.HostName || !reflect.DeepEqual(ff["allowed_extensions"], []string{browser.FirefoxID}) {
		t.Errorf("Firefox's manifest: %v", ff)
	}
}

func TestWebAndFTPLinksAreDownloadable(t *testing.T) {
	for _, u := range []string{"https://x.org/a.zip", "FTP://x.org/a.iso", "ftps://x/a", "ftpes://x/a"} {
		if !downloadable(u) {
			t.Error(u)
		}
	}
	for _, u := range []string{"file:///C:/a", "javascript:alert(1)", ""} {
		if downloadable(u) {
			t.Error(u)
		}
	}
}

func TestOptionsDecideWhatIsTaken(t *testing.T) {
	h, sent, _ := testHost()
	rules := browser.DefaultRules()
	msg := func(url, name string) Message {
		return Message{URL: url, Filename: name, UserAgent: chromeUA}
	}
	// Not a listed type: left to the browser, nothing delivered.
	if r := h.answer(msg("https://example.com/page.html", "page.html"), rules); r.OK || !strings.Contains(r.Error, "FileType") {
		t.Errorf("page: %+v", r)
	}
	if r := h.answer(msg("https://dl.update.microsoft.com/kb.exe", "kb.exe"), rules); r.OK || !strings.Contains(r.Error, "Site") {
		t.Errorf("excluded site: %+v", r)
	}
	rules.Browsers = []browser.BrowserCapture{{Name: "Google Chrome", Capture: false}}
	if r := h.answer(msg("https://example.com/a.zip", `C:\Users\u\Downloads\a.zip`), rules); r.OK || !strings.Contains(r.Error, "Browser") {
		t.Errorf("browser off: %+v", r)
	}
	if len(*sent) != 0 {
		t.Errorf("delivered %v", *sent)
	}
	if r := h.answer(Message{Rules: true}, rules); !r.OK || !strings.Contains(*r.Types, "ZIP") {
		t.Errorf("rules: %+v", r)
	}
	if got := fileNameOf(&Message{URL: "https://x.org/f/b.rar?x=1"}); got != "b.rar" {
		t.Errorf("fileNameOf = %q", got)
	}
	// Right-click → Download with Yellow Flash: any link, whatever Options say.
	page := msg("https://example.com/page.html", "")
	if _, ok := verdict(&page, rules); ok {
		t.Error("a page taken")
	}
	page.Explicit = true
	if _, ok := verdict(&page, rules); !ok {
		t.Error("an explicit link refused")
	}
	rules.AdvancedIntegration = false
	if _, ok := verdict(&page, rules); !ok {
		t.Error("an explicit link refused with integration off")
	}
}

func TestTheVideoButtonAsksTheRules(t *testing.T) {
	h, sent, _ := testHost()
	rules := browser.DefaultRules()
	rules.VideoPanelSites = "*.quiet.test"
	ask := func(page string) Message { return Message{Panel: true, URL: page, UserAgent: chromeUA} }
	if !h.answer(ask("https://videos.test/watch"), rules).OK {
		t.Error("a video site")
	}
	if h.answer(ask("https://www.quiet.test/watch"), rules).OK {
		t.Error("a quiet site")
	}
	rules.VideoPanel = false
	if h.answer(ask("https://videos.test/watch"), rules).OK {
		t.Error("the button is off")
	}
	// Clicked where it's off (the page was open before the change): refused.
	r := h.answer(Message{Video: true, URL: "https://videos.test/watch", UserAgent: chromeUA}, rules)
	if r.OK || !strings.Contains(r.Error, "off") || len(*sent) != 0 {
		t.Errorf("clicked where off: %+v, sent %v", r, *sent)
	}
	// The toolbar button's media list asks for a stream explicitly: taken
	// wherever the button is off, the manifest fetched as from its page.
	jar := []ipc.BrowserCookie{{Name: "cdn", Value: "1", Domain: ".cdn.test", Path: "/"}}
	listed := Message{Video: true, Explicit: true, URL: "https://www.quiet.test/watch",
		Src: "https://cdn.test/v/master.m3u8?t=1", Cookies: "cdn=1", CookieJar: jar, UserAgent: chromeUA}
	cmd, _, err := videoCommand(&listed, rules, browserOf(&listed))
	if err != "" {
		t.Fatal(err)
	}
	want := ipc.Command{Browser: true, VideoPage: "https://cdn.test/v/master.m3u8?t=1", Referer: "https://www.quiet.test/watch",
		Cookies: "cdn=1", CookieJar: jar, UserAgent: chromeUA}
	if !reflect.DeepEqual(cmd, want) {
		t.Errorf("got %+v\nwant %+v", cmd, want)
	}
	// Never a page that isn't on the web.
	listed.URL = "file:///C:/x.html"
	if _, _, err := videoCommand(&listed, rules, ""); err == "" {
		t.Error("a local page")
	}
	// From the wire: the extension's camelCase names.
	if m := parse(t, `{"hideSite":"tube.test","openOptions":false}`); m.HideSite != "tube.test" {
		t.Errorf("hideSite: %+v", m)
	}
}

func TestTheButtonsAndMenusReachTheApp(t *testing.T) {
	h, sent, _ := testHost()
	rules := browser.DefaultRules()
	for _, m := range []Message{
		{HideSite: " tube.test "},
		{OpenOptions: true, Page: "general"},
		{OpenOptions: true},
		{Open: true},
		{ExcludeSite: "x.test"},
		{Links: []string{"mailto:a@b", "https://x.test/a.zip"}, Referrer: "https://x.test/"},
	} {
		if r := h.answer(m, rules); !r.OK {
			t.Errorf("%+v: %+v", m, r)
		}
	}
	want := []ipc.Command{
		{PanelHideSite: "tube.test"},
		{Show: "options:general"},
		{Show: "options:panels"},
		{},
		{ExcludeSite: "x.test"},
		{Browser: true, Links: []string{"https://x.test/a.zip"}, Referer: "https://x.test/"},
	}
	if !reflect.DeepEqual(*sent, want) {
		t.Errorf("got %+v\nwant %+v", *sent, want)
	}
}

func TestTheBrowsersWholeContextReachesTheApp(t *testing.T) {
	m := parse(t, `{"url":"https://x.test/get?id=1","finalUrl":"https://cdn.test/f/abc","filename":"C:\\dl\\report.pdf",
		"size":12345,"mime":"application/pdf","post":"token=abc","postType":"application/x-www-form-urlencoded",
		"private":true,"userAgent":"UA","brands":["Brave"],"v":"0.2.5"}`)
	if b := browserOf(&m); b != "Brave Browser" {
		t.Errorf("browser %q", b)
	}
	c := toCommand(m)
	if c.URL != "https://x.test/get?id=1" || c.FinalURL != "https://cdn.test/f/abc" || c.Name != "report.pdf" {
		t.Errorf("addresses and name: %+v", c)
	}
	if c.Size == nil || *c.Size != 12345 || c.Mime != "application/pdf" {
		t.Errorf("size and type: %+v", c)
	}
	if c.Post != "token=abc" || c.PostType != "application/x-www-form-urlencoded" || !c.Private {
		t.Errorf("form and window: %+v", c)
	}
	// No form: no Content-Type for one.
	c = toCommand(parse(t, `{"url":"https://x.test/a.zip","postType":"text/plain","size":0}`))
	if c.PostType != "" || c.Size != nil || c.FinalURL != "" {
		t.Errorf("no form: %+v", c)
	}
}

func TestRedirectsAreCheckedAtBothEnds(t *testing.T) {
	rules := browser.DefaultRules()
	m := Message{URL: "https://example.com/get/a.zip", FinalURL: "https://download.windowsupdate.com/a.zip",
		Filename: "a.zip", UserAgent: chromeUA}
	if d, ok := verdict(&m, rules); ok || d != browser.Site {
		t.Errorf("to an excluded site: %q, %v", d, ok)
	}
	m.FinalURL = "https://cdn.example.com/a.zip"
	if _, ok := verdict(&m, rules); !ok {
		t.Error("to a CDN")
	}
}

func TestRulesSayEverythingTheExtensionNeeds(t *testing.T) {
	h, _, _ := testHost()
	rules := browser.DefaultRules()
	hello := Message{Hello: true, UserAgent: "Mozilla/5.0 Firefox/145.0"}
	r := h.answer(hello, rules)
	if !r.OK || !*r.Capture || !*r.Opened || r.Version != ipc.AppVersion || !strings.Contains(*r.ExcludedSites, "windowsupdate") {
		t.Errorf("hello: %+v", r)
	}
	rules.Browsers = []browser.BrowserCapture{{Name: "Mozilla Firefox", Capture: false}}
	raw, _ := json.Marshal(h.answer(hello, rules))
	for _, s := range []string{`"excludedSites"`, `"capture":false`, `"excludedAddresses":[]`, `"minKb":0`} {
		if !strings.Contains(string(raw), s) {
			t.Errorf("%s lacks %s", raw, s)
		}
	}
	// Links that aren't downloads are refused without anything sent.
	if r := h.answer(Message{Links: []string{"javascript:void(0)", "mailto:a@b"}}, rules); r.OK || r.Why != "refused" {
		t.Errorf("links: %+v", r)
	}
}

func TestARefusalSaysWhichRule(t *testing.T) {
	h, _, _ := testHost()
	rules := browser.DefaultRules()
	rules.Browsers = []browser.BrowserCapture{{Name: "Microsoft Edge", Capture: false}}
	r := h.answer(Message{URL: "https://x.test/p.png", Filename: "p.png", UserAgent: chromeUA}, rules)
	if r.OK || r.Why != "options" || r.Declined != "file-type" {
		t.Errorf("p.png: %+v", r)
	}
	// The words older extensions read stay as they were.
	if r.Error != "left to the browser by Options (FileType)" {
		t.Errorf("error %q", r.Error)
	}
	edge := chromeUA + " Edg/141.0.0.0"
	r = h.answer(Message{URL: "https://x.test/a.zip", Filename: "a.zip", UserAgent: edge}, rules)
	if raw, _ := json.Marshal(r); !strings.Contains(string(raw), `"declined":"browser"`) {
		t.Errorf("edge: %s", raw)
	}
	raw, _ := json.Marshal(h.answer(Message{Hello: true, UserAgent: edge}, rules))
	if !strings.Contains(string(raw), `"capture":false`) || !strings.Contains(string(raw), `"captureOff":"browser"`) {
		t.Errorf("hello from edge: %s", raw)
	}
}

func TestTheExtensionsOwnRefusalsAreOnlyNoted(t *testing.T) {
	h, sent, logged := testHost()
	m := parse(t, `{"declined":{"why":"file-type","site":"x.test","name":"p.png"},"userAgent":"UA","brands":[],"v":"0.3.0"}`)
	if *m.Declined != (Notice{Why: "file-type", Site: "x.test", Name: "p.png"}) {
		t.Errorf("notice %+v", *m.Declined)
	}
	// No address of its own: an older host refuses it as an address it
	// can't download, and hands nothing over.
	if downloadable(m.URL) {
		t.Error("a notice has an address")
	}
	if r := h.answer(m, browser.DefaultRules()); !r.OK || r.Error != "" || len(*sent) != 0 || len(*logged) != 1 {
		t.Errorf("noted: %+v, sent %v, logged %v", r, *sent, *logged)
	}
	if noticeReason("site") != browser.Site.Reason() || !strings.Contains(noticeReason("site-access"), "site access") {
		t.Error("reasons")
	}
	if got := noticeReason("x\ny"); got != `"x y"` {
		t.Errorf("unknown code: %s", got)
	}
}

func TestLogLinesNameWhatWasLeftAndWhy(t *testing.T) {
	reason := browser.FileType.Reason()
	if got := leftLine("Waterfox", "x.test", "p.png", reason, false); got != "browser integration: Waterfox keeps p.png from x.test: its type isn't in Options > File types" {
		t.Errorf("line %q", got)
	}
	// A private window's download is never named; nor can a name break the line.
	if p := leftLine("Google Chrome", "secret.test", "s.zip", reason, true); strings.Contains(p, "secret") || !strings.Contains(p, "private window") {
		t.Errorf("private %q", p)
	}
	if strings.Contains(leftLine("", "x.test", "a\nb.zip", reason, false), "\n") {
		t.Error("a line break")
	}
	if !strings.HasPrefix(leftLine("", "", "", reason, false), "browser integration: a browser keeps a download:") {
		t.Error("nothing named")
	}
}

func TestOlderExtensionsMessagesStillWork(t *testing.T) {
	rules := browser.DefaultRules()
	for _, raw := range []string{
		`{"url":"https://x.test/a.zip","finalUrl":"","referrer":"https://x.test/","cookies":"","filename":"a.zip","size":1048576,"mime":"application/zip","userAgent":"Mozilla/5.0 Chrome/141.0.0.0 Safari/537.36","brands":["Not:A-Brand","Chromium","Google Chrome"],"v":"0.2.4"}`,
		`{"url":"https://x.test/a.zip","referrer":"https://x.test/","cookies":"","userAgent":"Mozilla/5.0 Chrome/141.0.0.0 Safari/537.36","filename":"a.zip"}`,
	} {
		m := parse(t, raw)
		if _, ok := verdict(&m, rules); !ok {
			t.Errorf("refused %s", raw)
		}
		if n := toCommand(m).Name; n != "a.zip" {
			t.Errorf("name %q", n)
		}
	}
}

func frame(s string) []byte {
	var b bytes.Buffer
	_ = writeMessage(&b, []byte(s))
	return b.Bytes()
}

func TestFramingRoundTrip(t *testing.T) {
	buf := frame(`{"url":"https://x/a.zip"}`)
	if binary.LittleEndian.Uint32(buf) != 25 {
		t.Errorf("length %v", buf[:4])
	}
	r := bytes.NewReader(buf)
	if m, err := readMessage(r); err != nil || string(m) != `{"url":"https://x/a.zip"}` {
		t.Errorf("read %q, %v", m, err)
	}
	if _, err := readMessage(r); !errors.Is(err, io.EOF) {
		t.Errorf("clean end of input: %v", err)
	}
}

func TestHugeMessagesAreReadPastAndSaid(t *testing.T) {
	big := 2 << 20
	input := binary.LittleEndian.AppendUint32(nil, uint32(big))
	input = append(input, bytes.Repeat([]byte("x"), big)...)
	input = append(input, frame(`{"rules":true}`)...)
	r := bytes.NewReader(input)
	var tooLarge errTooLarge
	if _, err := readMessage(r); !errors.As(err, &tooLarge) || int(tooLarge) != big {
		t.Errorf("huge: %v", err)
	}
	if _, err := readMessage(r); err != nil {
		t.Errorf("the next one: %v", err)
	}
	// Served: said, then the next one answered.
	h, _, _ := testHost()
	var out bytes.Buffer
	if err := h.serve(bytes.NewReader(input), &out, browser.DefaultRules, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	replies := readReplies(t, out.Bytes())
	if len(replies) != 2 || replies[0]["why"] != "too-large" || replies[1]["ok"] != true {
		t.Errorf("replies %v", replies)
	}
}

func readReplies(t *testing.T, out []byte) []map[string]any {
	t.Helper()
	var replies []map[string]any
	r := bytes.NewReader(out)
	for {
		raw, err := readMessage(r)
		if errors.Is(err, io.EOF) {
			return replies
		}
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		replies = append(replies, v)
	}
}

// The extension's cookies with their domains, as cookies.getAll names them
// (its other fields ignored), reach the app; an older extension has none.
func TestCookiesKeepTheirDomains(t *testing.T) {
	m := parse(t, `{"url":"https://music.youtube.com/watch?v=x","cookies":"SID=1; pref=2","userAgent":"UA",
		"cookieJar":[{"name":"SID","value":"1","domain":".youtube.com","hostOnly":false,"path":"/","secure":true,"httpOnly":true,"sameSite":"lax","session":false,"expirationDate":1893456000.5,"storeId":"0"},
		             {"name":"pref","value":"2","domain":"music.youtube.com","hostOnly":true,"path":"/","secure":false,"session":true}]}`)
	c := toCommand(m)
	exp := 1893456000.5
	want := []ipc.BrowserCookie{
		{Name: "SID", Value: "1", Domain: ".youtube.com", Path: "/", Secure: true, Expires: &exp},
		{Name: "pref", Value: "2", Domain: "music.youtube.com", HostOnly: true, Path: "/"},
	}
	if c.Cookies != "SID=1; pref=2" || !reflect.DeepEqual(c.CookieJar, want) {
		t.Errorf("got %+v", c.CookieJar)
	}
	// To the app and back (its command line): the same.
	line, _ := json.Marshal(c)
	var back ipc.Command
	if err := json.Unmarshal(line, &back); err != nil || !reflect.DeepEqual(back.CookieJar, c.CookieJar) {
		t.Errorf("round trip: %v %+v", err, back.CookieJar)
	}
	old := toCommand(parse(t, `{"url":"https://x/a.zip","cookies":"a=1"}`))
	if raw, _ := json.Marshal(old); len(old.CookieJar) != 0 || strings.Contains(string(raw), "cookie_jar") {
		t.Errorf("older: %s", raw)
	}
}

func TestMessageToCommand(t *testing.T) {
	c := toCommand(parse(t, `{"url":"https://x/a.zip","referrer":"https://x/","cookies":"a=1","userAgent":"UA","filename":""}`))
	want := ipc.Command{Browser: true, URL: "https://x/a.zip", Referer: "https://x/", Cookies: "a=1", UserAgent: "UA"}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got %+v", c)
	}
}

func TestTheBrowsersProxyGoesWithTheDownload(t *testing.T) {
	c := toCommand(parse(t, `{"url":"https://x/a.zip","proxy":"socks5h://vpn.example:1080","proxyLogin":true}`))
	if c.Proxy != "socks5h://vpn.example:1080" || !c.ProxyLogin {
		t.Errorf("proxy %+v", c)
	}
	pac := `{"url":"https://x/a.zip","proxyPac":"function FindProxyForURL(u, h) { return \"PROXY p:8080\"; }"}`
	if toCommand(parse(t, pac)).ProxyPAC == "" {
		t.Error("a PAC script")
	}
	if toCommand(parse(t, `{"url":"https://x/a.zip","proxyPac":"alert(1)"}`)).ProxyPAC != "" {
		t.Error("not a PAC script")
	}
	// Only proxies the app can use, written as expected.
	for _, good := range []string{"http://10.0.0.1:3128", "https://p.example:443", "socks4://[::1]:1080"} {
		if got := browserProxy(good); got != good {
			t.Errorf("%s gave %q", good, got)
		}
	}
	for _, bad := range []string{"ftp://p:21", "http://p", "http://p:0", "http://user:pw@p:8080",
		"http://p:8080/path", "socks5://bad host:1080"} {
		if got := browserProxy(bad); got != "" {
			t.Errorf("%s gave %q", bad, got)
		}
	}
}
