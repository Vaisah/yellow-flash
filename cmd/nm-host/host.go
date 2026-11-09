// Ported from Mister Download Manager, crates/nmhost/src/main.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
)

// Message is a message from the extension. Absent fields are zero.
type Message struct {
	// Rules only asks for the capture rules (the file types to offer).
	// Hello (the extension installed, or the browser started) asks the same
	// and tells the app it is there.
	Rules bool `json:"rules"`
	Hello bool `json:"hello"`
	// V is the extension's version.
	V        string `json:"v"`
	URL      string `json:"url"`
	Referrer string `json:"referrer"`
	Cookies  string `json:"cookies"`
	// CookieJar is the same cookies with their domains and flags; an older
	// extension sends none.
	CookieJar []ipc.BrowserCookie `json:"cookieJar"`
	UserAgent string              `json:"userAgent"`
	// Brands are navigator.userAgentData's: Brave and Opera name themselves
	// there, not in the User-Agent; Firefox's forks add the name
	// runtime.getBrowserInfo gives ("Zen").
	Brands   []string `json:"brands"`
	Filename string   `json:"filename"`
	// FinalURL is where the browser's redirects from URL ended.
	FinalURL string `json:"finalUrl"`
	// Size (bytes, when known) and Mime are the browser's own download's.
	Size *int64 `json:"size"`
	Mime string `json:"mime"`
	// Post is a form's body to post again, PostType its Content-Type.
	Post     string `json:"post"`
	PostType string `json:"postType"`
	// Private: from a private window.
	Private bool `json:"private"`
	// Proxy is the browser's proxy for URL, or ProxyPAC its auto-config
	// script; ProxyLogin: it asked for a login.
	Proxy      string `json:"proxy"`
	ProxyPAC   string `json:"proxyPac"`
	ProxyLogin bool   `json:"proxyLogin"`
	// Explicit: the user asked for this download (right-click → Download
	// with Yellow Flash): the automatic-capture rules don't apply.
	Explicit bool `json:"explicit"`
	// Panel asks whether the download button may show over videos on URL.
	Panel bool `json:"panel"`
	// Video: the button was clicked; URL is the video's page, Src the
	// video's own address or its stream's manifest. With Explicit, the
	// toolbar button's list of the tab's media asked for it.
	Video bool   `json:"video"`
	Src   string `json:"src"`
	// HideSite: the button's menu keeps it off this host.
	HideSite string `json:"hideSite"`
	// OpenOptions opens its settings in Options (Page "general": the
	// toolbar popup asks for General).
	OpenOptions bool   `json:"openOptions"`
	Page        string `json:"page"`
	// Links are the right-click menu's links of a page or its selection.
	Links []string `json:"links"`
	// ExcludeSite: the toolbar button leaves this host to the browser.
	ExcludeSite string `json:"excludeSite"`
	// Open brings Yellow Flash's window up.
	Open bool `json:"open"`
	// Declined: the extension left a download to the browser by the rules
	// (it has them from Rules) without asking: for the log only.
	Declined *Notice `json:"declined"`
}

// Notice is a download the extension left to the browser: why (a Declined
// code, or "site-access": the browser doesn't let it read the site), its
// site and file name.
type Notice struct {
	Why  string `json:"why"`
	Site string `json:"site"`
	Name string `json:"name"`
}

// Reply is the host's answer to one message.
type Reply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Why it failed, for the extension to say and to link the help page's
	// section: not-running, too-large, refused, options.
	Why string `json:"why,omitempty"`
	// Version is the app's (the host ships with it).
	Version string `json:"version,omitempty"`
	// For Rules: Options → File types; whether this browser's downloads
	// are taken at all; the sites and addresses left to the browser; also
	// listed types the browser would open itself; the smallest file taken
	// (KB); and when capture is off, why.
	Types             *string   `json:"types,omitempty"`
	Capture           *bool     `json:"capture,omitempty"`
	ExcludedSites     *string   `json:"excludedSites,omitempty"`
	ExcludedAddresses *[]string `json:"excludedAddresses,omitempty"`
	Opened            *bool     `json:"opened,omitempty"`
	MinKB             *int64    `json:"minKb,omitempty"`
	CaptureOff        string    `json:"captureOff,omitempty"`
	// Declined: a download left to the browser by Options, which rule.
	Declined string `json:"declined,omitempty"`
}

func done() Reply { return Reply{OK: true} }

func failed(why, err string) Reply { return Reply{Why: why, Error: err} }

// host answers messages: deliver hands a command to the app, log writes a
// line to the app's log.
type host struct {
	deliver func(ipc.Command) Reply
	log     func(string)
}

// fileNameOf is the name a download will have: the browser's, else the
// address's last part.
func fileNameOf(m *Message) string {
	if f := baseName(m.Filename); f != "" {
		return f
	}
	u := m.URL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return u[strings.LastIndexByte(u, '/')+1:]
}

// baseName is a path's last part, by either separator.
func baseName(p string) string {
	return p[strings.LastIndexAny(p, `\/`)+1:]
}

func browserOf(m *Message) string { return browser.BrowserNamed(m.UserAgent, m.Brands) }

// debugNames are the rule names in the words older extensions read
// ("left to the browser by Options (FileType)").
var debugNames = map[browser.Declined]string{
	browser.IntegrationOff: "IntegrationOff", browser.BrowserOff: "Browser",
	browser.FileType: "FileType", browser.Site: "Site",
	browser.Address: "Address", browser.Small: "Small",
}

// answer is the reply to one message under rules (the app's saved Options).
func (h *host) answer(m Message, rules browser.CaptureRules) Reply {
	name := browserOf(&m)
	if m.Rules || m.Hello {
		types, sites := rules.AutoTypes, rules.ExcludedSites
		addresses := append([]string{}, rules.ExcludedAddresses...)
		capture, opened, minKB := rules.Captures(name), rules.CaptureOpened, rules.CaptureMinKB
		r := Reply{OK: true, Version: ipc.AppVersion, Types: &types, Capture: &capture,
			ExcludedSites: &sites, ExcludedAddresses: &addresses, Opened: &opened, MinKB: &minKB}
		if d, off := rules.CaptureOff(name); off {
			r.CaptureOff = string(d)
		}
		return r
	}
	if n := m.Declined; n != nil {
		h.logLeft(name, n.Site, n.Name, noticeReason(n.Why), m.Private)
		return done()
	}
	// The video button: asked about, clicked, its menu. What it sends goes
	// to the app (started if need be), which opens its window or keeps the
	// setting.
	if m.Panel {
		return Reply{OK: rules.PanelOn(m.URL, name)}
	}
	var cmd *ipc.Command
	switch {
	case strings.TrimSpace(m.HideSite) != "":
		cmd = &ipc.Command{PanelHideSite: strings.TrimSpace(m.HideSite)}
	case m.OpenOptions:
		page := "options:panels"
		if m.Page == "general" {
			page = "options:general"
		}
		cmd = &ipc.Command{Show: page}
	case m.Open:
		// An empty command: the running app comes to the front.
		cmd = &ipc.Command{}
	case strings.TrimSpace(m.ExcludeSite) != "":
		cmd = &ipc.Command{ExcludeSite: strings.TrimSpace(m.ExcludeSite)}
	case len(m.Links) > 0:
		var links []string
		for _, l := range m.Links {
			if downloadable(l) {
				links = append(links, l)
			}
		}
		if len(links) == 0 {
			return failed("refused", "no link Yellow Flash downloads")
		}
		cmd = &ipc.Command{Links: links, Referer: m.Referrer, Cookies: m.Cookies,
			UserAgent: m.UserAgent, Private: m.Private}
	case m.Video:
		c, why, err := videoCommand(&m, rules, name)
		if err != "" {
			return failed(why, err)
		}
		cmd = &c
	}
	if cmd != nil {
		return h.deliver(*cmd)
	}
	if !downloadable(m.URL) {
		return failed("refused", "not an http(s) or ftp(s) URL")
	}
	if why, ok := verdict(&m, rules); !ok {
		h.logLeft(name, browser.HostOf(m.URL), fileNameOf(&m), why.Reason(), m.Private)
		r := failed("options", "left to the browser by Options ("+debugNames[why]+")")
		r.Declined = string(why)
		return r
	}
	return h.deliver(toCommand(m))
}

// noticeReason says why the extension left a download alone, from its
// notice's code.
func noticeReason(code string) string {
	if d, ok := browser.ParseDeclined(code); ok {
		return d.Reason()
	}
	if code == "site-access" {
		return "the browser doesn't let the extension read this site (its site access is limited)"
	}
	return strconv.Quote(clean(code, 32))
}

// clean makes s fit one line of the log: no line breaks or other control
// characters, at most max characters.
func clean(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, c := range s {
		if n == max {
			break
		}
		if unicode.IsControl(c) {
			c = ' '
		}
		b.WriteRune(c)
		n++
	}
	return b.String()
}

// leftLine is the log line for a download left to the browser. A private
// window's site and file name are left out: the log outlives the window.
func leftLine(browserName, site, name, reason string, private bool) string {
	if browserName == "" {
		browserName = "a browser"
	}
	what := "a download from a private window"
	if !private {
		n, s := clean(name, 200), clean(site, 200)
		noName, noSite := strings.TrimSpace(n) == "", strings.TrimSpace(s) == ""
		switch {
		case noName && noSite:
			what = "a download"
		case noSite:
			what = n
		case noName:
			what = "a download from " + s
		default:
			what = n + " from " + s
		}
	}
	return fmt.Sprintf("browser integration: %s keeps %s: %s", browserName, what, reason)
}

func (h *host) logLeft(browserName, site, name, reason string, private bool) {
	h.log(leftLine(browserName, site, name, reason, private))
}

func webPage(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// videoCommand is the Download video window's command for a video message:
// the page, or the video's own file or its stream's manifest (Src) fetched
// as from its page; or why it is refused (the reply's why and error).
func videoCommand(m *Message, rules browser.CaptureRules, name string) (ipc.Command, string, string) {
	if !webPage(m.URL) {
		return ipc.Command{}, "refused", "not an http(s) page"
	}
	// The toolbar button's list of the tab's media asks for one explicitly:
	// whether the button over videos shows on this site doesn't matter then.
	if !m.Explicit && !rules.PanelOn(m.URL, name) {
		return ipc.Command{}, "options", "the video button is off here"
	}
	// The video clicked, not the page's first one (a feed plays many): its
	// own file when there is one, fetched as from its page.
	page, referer := m.URL, m.Referrer
	if webPage(m.Src) {
		page, referer = m.Src, m.URL
	}
	return ipc.Command{VideoPage: page, Referer: referer, Cookies: m.Cookies,
		CookieJar: m.CookieJar, UserAgent: m.UserAgent, Private: m.Private}, "", ""
}

// verdict says whether Options let the browser hand m over; an explicit
// request always may. Both addresses must pass when redirects led
// elsewhere (an excluded site sending to a CDN).
func verdict(m *Message, rules browser.CaptureRules) (browser.Declined, bool) {
	if m.Explicit {
		return "", true
	}
	name, b := fileNameOf(m), browserOf(m)
	if d, ok := rules.Accepts(m.URL, name, b); !ok {
		return d, false
	}
	if m.Size != nil && rules.TooSmall(*m.Size) {
		return browser.Small, false
	}
	if m.FinalURL != "" && m.FinalURL != m.URL {
		return rules.Accepts(m.FinalURL, name, b)
	}
	return "", true
}

// pacMax is the largest proxy auto-config script taken from the browser.
const pacMax = 256 * 1024

func toCommand(m Message) ipc.Command {
	c := ipc.Command{
		URL: m.URL, Name: baseName(m.Filename), Referer: m.Referrer, Cookies: m.Cookies,
		CookieJar: m.CookieJar, UserAgent: m.UserAgent, Mime: m.Mime, Post: m.Post,
		Private: m.Private, Proxy: browserProxy(m.Proxy), ProxyLogin: m.ProxyLogin,
	}
	if m.FinalURL != m.URL {
		c.FinalURL = m.FinalURL
	}
	if m.Size != nil && *m.Size > 0 {
		c.Size = m.Size
	}
	if m.Post != "" {
		c.PostType = m.PostType
	}
	if len(m.ProxyPAC) <= pacMax && strings.Contains(m.ProxyPAC, "FindProxyForURL") {
		c.ProxyPAC = m.ProxyPAC
	}
	return c
}

// browserProxy is the browser's proxy if it is one the app can use:
// scheme://host:port, the scheme http, https, socks4 or socks5(h), the host
// a name or an IP address (IPv6 in brackets), nothing after the port.
// Anything else is dropped (the app then uses its own proxy settings).
func browserProxy(s string) string {
	s = strings.TrimSpace(s)
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok {
		return ""
	}
	switch scheme {
	case "http", "https", "socks4", "socks5", "socks5h":
	default:
		return ""
	}
	i := strings.LastIndexByte(rest, ':')
	if i < 0 {
		return ""
	}
	h, p := rest[:i], rest[i+1:]
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 {
		return ""
	}
	okHost := false
	if v6, found := strings.CutPrefix(h, "["); found && strings.HasSuffix(v6, "]") {
		v6 = strings.TrimSuffix(v6, "]")
		okHost = v6 != "" && strings.Trim(v6, "0123456789abcdefABCDEF:.") == ""
	} else {
		okHost = h != "" && len(h) <= 253 &&
			strings.Trim(h, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-") == ""
	}
	if !okHost {
		return ""
	}
	return fmt.Sprintf("%s://%s:%d", scheme, h, port)
}

// downloadable reports the addresses the app downloads: http(s), and ftp
// links sent from a page (browsers no longer download ftp themselves).
func downloadable(u string) bool {
	scheme, _, ok := strings.Cut(u, "://")
	if !ok {
		return false
	}
	switch strings.ToLower(scheme) {
	case "http", "https", "ftp", "ftps", "ftpes":
		return true
	}
	return false
}

// errTooLarge is a message over the limit, read past and dropped.
type errTooLarge int

func (e errTooLarge) Error() string { return fmt.Sprintf("a message of %d bytes", int(e)) }

// readMessage reads one native message (u32 little-endian length, then
// JSON); io.EOF at the end of input. A message over ipc.MaxMessage is read
// past and returned as errTooLarge, to be answered "too-large".
func readMessage(r io.Reader) ([]byte, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	size := binary.LittleEndian.Uint32(n[:])
	// Browsers send up to 4 GB, but a download request is small: refuse
	// anything silly (the app's channel takes this much). Read past it, so
	// the extension hears why instead of a closed host.
	if size > ipc.MaxMessage {
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return nil, err
		}
		return nil, errTooLarge(size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMessage(w io.Writer, msg []byte) error {
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(msg)))
	if _, err := w.Write(append(n[:], msg...)); err != nil {
		return err
	}
	return nil
}

// serve answers messages from r on w until r ends. rules reads the rules
// as saved now (a change in Options applies to the next download); seen
// notes the browser that has the extension.
func (h *host) serve(r io.Reader, w io.Writer, rules func() browser.CaptureRules, seen func(browserName, version string)) error {
	for {
		raw, err := readMessage(r)
		var reply Reply
		var big errTooLarge
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case errors.As(err, &big):
			reply = failed("too-large", big.Error())
		case err != nil:
			return err
		default:
			var m Message
			if err := json.Unmarshal(raw, &m); err != nil {
				reply = failed("refused", err.Error())
				break
			}
			// This browser has the extension: the app stops offering it.
			seen(browserOf(&m), m.V)
			reply = h.answer(m, rules())
		}
		out, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		if err := writeMessage(w, out); err != nil {
			return err
		}
	}
}
