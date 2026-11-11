// Ported from Mister Download Manager, crates/remote/src/{pairing,devices,addrs,tls}.rs (tests) and crates/remote/tests/server.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vaisah/internal/ipc"
)

// fakeClock is a pairing clock moved by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) wait()          { c.t = c.t.Add(Pace + 20*time.Millisecond) }

func ip(last byte) netip.Addr { return netip.AddrFrom4([4]byte{192, 168, 1, last}) }

func wrongFor(code string) string {
	if code == "00000000" {
		return "11111111"
	}
	return "00000000"
}

func pairing() (*Pairing, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	return &Pairing{now: c.now}, c
}

func TestACodePairsOnce(t *testing.T) {
	p, c := pairing()
	if r, _ := p.Check("12345678", ip(2)); r != NoCode {
		t.Error("none shown", r)
	}
	code := p.Start()
	if len(code) != 8 {
		t.Fatal(code)
	}
	c.wait()
	if r, _ := p.Check(code[:4]+" "+code[4:], ip(2)); r != Right {
		t.Error(r)
	}
	c.wait()
	if r, _ := p.Check(code, ip(2)); r != NoCode {
		t.Error("used", r)
	}
}

func TestOneDeviceGetsFivePacedTries(t *testing.T) {
	p, c := pairing()
	code := p.Start()
	wrong := wrongFor(code)
	if r, left := p.Check(wrong, ip(9)); r != Wrong || left != Tries-1 {
		t.Error(r, left)
	}
	if r, _ := p.Check(code, ip(9)); r != TooSoon {
		t.Error("one try a second", r)
	}
	for left := Tries - 2; left >= 0; left-- {
		c.wait()
		if r, l := p.Check(wrong, ip(9)); r != Wrong || l != left {
			t.Error(r, l)
		}
	}
	c.wait()
	if r, left := p.Check(code, ip(9)); r != Wrong || left != 0 {
		t.Error("this device is out of tries, even with the right code", r, left)
	}
}

// A device guessing on the network neither slows the phone being paired
// nor uses up its tries.
func TestAGuessingDeviceDoesNotBlockThePhone(t *testing.T) {
	p, c := pairing()
	code := p.Start()
	wrong := wrongFor(code)
	for range Tries {
		if r, _ := p.Check(wrong, ip(66)); r != Wrong {
			t.Fatal(r)
		}
		c.wait()
	}
	if r, left := p.Check(wrong, ip(66)); r != Wrong || left != 0 {
		t.Error(r, left)
	}
	if r, _ := p.Check(code, ip(2)); r != Right {
		t.Error(r)
	}
}

func TestTwentyWrongTriesInAllVoidTheCode(t *testing.T) {
	p, c := pairing()
	code := p.Start()
	wrong := wrongFor(code)
	for range Tries {
		for d := range TotalTries / Tries {
			if r, _ := p.Check(wrong, ip(byte(100+d))); r != Wrong {
				t.Fatal(r)
			}
		}
		c.wait()
	}
	if r, _ := p.Check(code, ip(2)); r != NoCode {
		t.Error("void", r)
	}
}

func TestACodeRunsOut(t *testing.T) {
	p, c := pairing()
	code := p.Start()
	if shown, left := p.Shown(); shown != code || left != CodeValid {
		t.Error(shown, left)
	}
	c.t = c.t.Add(CodeValid)
	if shown, _ := p.Shown(); shown != "" {
		t.Error("still shown")
	}
	if r, _ := p.Check(code, ip(2)); r != NoCode {
		t.Error(r)
	}
}

func TestPairCheckRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote", "devices.json")
	d := LoadDevices(path)
	phone, token := d.Add("Phone\a\n with a very long name that goes on and on and on")
	if len([]rune(phone.Name)) > MaxName || !strings.HasPrefix(phone.Name, "Phone with") {
		t.Errorf("%q", phone.Name)
	}
	if len(token) != 64 {
		t.Error(token)
	}
	// The file holds the hash only.
	b, _ := os.ReadFile(path)
	if bytes.Contains(b, []byte(token)) || !bytes.Contains(b, []byte(hashToken(token))) {
		t.Error("the token is in the file")
	}
	again := LoadDevices(path)
	if got, ok := again.Check(token); !ok || got.ID != phone.ID {
		t.Error(got, ok)
	}
	if _, ok := again.Check("wrong"); ok {
		t.Error("wrong token")
	}
	if !again.Remove(phone.ID) || again.Remove(phone.ID) {
		t.Error("Remove")
	}
	if _, ok := again.Check(token); ok {
		t.Error("removed: refused")
	}
	if d, _ := d.Add("\t"); d.Name != "?" {
		t.Error(d.Name)
	}
}

func adapter(name string, addrs ...string) Adapter {
	a := Adapter{Name: name}
	for _, s := range addrs {
		a.IPs = append(a.IPs, netip.MustParseAddr(s))
	}
	return a
}

func TestOnlyLocalNetworkAddressesByDefault(t *testing.T) {
	list := []Adapter{adapter("Wi-Fi", "192.168.1.20", "fd12::5", "2a01:db8::1", "fe80::1"),
		adapter("Modem", "81.2.3.4"), adapter("Docker", "172.17.0.2")}
	got := Chosen("", list)
	want := []netip.Addr{netip.MustParseAddr("172.17.0.2"), netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("fd12::5")}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%v", got)
		}
	}
	// Never everything at once, and a public address only when chosen.
	for _, none := range []string{"0.0.0.0", "::", "10.0.0.9"} {
		if len(Chosen(none, list)) != 0 {
			t.Error(none)
		}
	}
	if len(Chosen("81.2.3.4", list)) != 1 || len(Chosen("modem", list)) != 1 {
		t.Error("chosen")
	}
	if got := Chosen(ThisComputer, list); len(got) != 1 || !got[0].IsLoopback() {
		t.Error(got)
	}
	if got := Chosen("[::1]", list); len(got) != 1 || got[0] != netip.IPv6Loopback() {
		t.Error(got)
	}
}

func TestHosts(t *testing.T) {
	for h, want := range map[string]string{"192.168.1.2:8463": "192.168.1.2", "[fd00::1]:8463": "[fd00::1]", "nas.local": "nas.local"} {
		if got := HostOf(h); got != want {
			t.Errorf("%s: %s", h, got)
		}
	}
	for _, ok := range []string{"192.168.1.2", "[fd00::1]", "localhost", "NAS.example"} {
		if !HostAllowed(ok, []string{"nas.example"}) {
			t.Error(ok)
		}
	}
	for _, bad := range []string{"evil.example", "192.168.1.2.evil.example"} {
		if HostAllowed(bad, nil) {
			t.Error(bad)
		}
	}
	if URL(netip.MustParseAddr("192.168.1.2"), 8463) != "https://192.168.1.2:8463/" || URL(netip.MustParseAddr("fd00::1"), 1) != "https://[fd00::1]:1/" {
		t.Error("URL")
	}
}

func TestTheCertificateIsKeptUntilItRunsOut(t *testing.T) {
	dir := t.TempDir()
	names := []string{"localhost", "127.0.0.1"}
	a, err := identity(dir, names)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := identity(dir, names)
	if a.Fingerprint != b.Fingerprint || len(a.Fingerprint) != 32*3-1 {
		t.Error(a.Fingerprint, b.Fingerprint)
	}
	m, _ := json.Marshal(made{Until: today() + 5})
	if err := os.WriteFile(filepath.Join(dir, "cert.json"), m, 0o600); err != nil {
		t.Fatal(err)
	}
	if c, _ := identity(dir, names); c.Fingerprint == a.Fingerprint {
		t.Error("near its end: a new one")
	}
}

// start is a remote control on 127.0.0.1, any port, with a stand-in app
// that answers every command with {"seen": …} and records it.
func start(t *testing.T) (*Remote, string, func() []ipc.Command) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []ipc.Command
	)
	sink := ipc.NewSink(func(in ipc.Incoming) bool {
		cmd := in.Cmd
		mu.Lock()
		seen = append(seen, cmd)
		mu.Unlock()
		what := cmd.Query
		if what == "" {
			what = cmd.Action
		}
		if what == "" {
			what = cmd.URL
		}
		b, _ := json.Marshal(map[string]any{"seen": what, "remote": cmd.Remote})
		in.Reply <- string(b)
		return true
	})
	texts := func(lang string) PageTexts {
		if strings.HasPrefix(lang, "de") {
			return PageTexts{Lang: "de", Texts: map[string]string{"Pair": "Koppeln"}}
		}
		return PageTexts{Lang: "en"}
	}
	r := New(filepath.Join(t.TempDir(), "remote"), sink, texts, nil, t.Logf)
	t.Cleanup(r.Stop)
	r.Apply(Config{On: true, Port: 0, Listen: ThisComputer, Hosts: []string{"yf.example"}})
	st := r.Status()
	if st.State != Listening || len(st.Addrs) != 1 || !st.Addrs[0].Addr().IsLoopback() {
		t.Fatalf("not listening: %+v", st)
	}
	return r, "https://127.0.0.1:" + strconv.Itoa(int(st.Addrs[0].Port())) + "/", func() []ipc.Command {
		mu.Lock()
		defer mu.Unlock()
		return append([]ipc.Command(nil), seen...)
	}
}

type client struct {
	t *testing.T
	c *http.Client
}

func newClient(t *testing.T) client {
	return client{t, &http.Client{Timeout: 10 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// do sends a request; headers are name, value pairs ("host" sets Host).
func (c client) do(method, url, token string, body any, headers ...string) (*http.Response, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, url, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if strings.EqualFold(headers[i], "host") {
			req.Host = headers[i+1]
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var j map[string]any
	b, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(b, &j)
	if j == nil {
		j = map[string]any{"_body": string(b)}
	}
	return res, j
}

func TestPairThenControl(t *testing.T) {
	r, base, seen := start(t)
	c := newClient(t)

	// The page, hardened.
	res, j := c.do("GET", base, "", nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		res.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(j["_body"].(string), "app.js") {
		t.Errorf("%d %v", res.StatusCode, res.Header)
	}
	if res, _ := c.do("GET", base+"icon.png", "", nil); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Error("icon", res.StatusCode)
	}
	// Texts in the browser's language.
	if _, j := c.do("GET", base+"api/texts?lang=de-DE,de", "", nil); j["lang"] != "de" || j["texts"].(map[string]any)["Pair"] != "Koppeln" {
		t.Error(j)
	}

	// Nothing without pairing.
	if res, _ := c.do("GET", base+"api/list", "", nil); res.StatusCode != 401 {
		t.Error(res.StatusCode)
	}
	if res, _ := c.do("GET", base+"api/list", "0123456789abcdef", nil); res.StatusCode != 401 {
		t.Error("a made-up token", res.StatusCode)
	}

	pair := func(code string) (*http.Response, map[string]any) {
		return c.do("POST", base+"api/pair", "", map[string]string{"code": code, "name": "Test phone"})
	}
	if res, _ := pair("12345678"); res.StatusCode != 410 {
		t.Error("no code shown", res.StatusCode)
	}
	// The pace counts from the try above.
	time.Sleep(Pace)
	code := r.StartPairing()
	res, j = pair(wrongFor(code))
	if res.StatusCode != 403 || j["left"] != float64(4) {
		t.Error(res.StatusCode, j)
	}
	if res, _ := pair(code); res.StatusCode != 429 {
		t.Error("too soon, even with the right code", res.StatusCode)
	}
	time.Sleep(Pace)
	res, j = pair(code)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode, j)
	}
	token := j["token"].(string)
	if _, _, paired := r.PairingState(); paired != "Test phone" || len(r.Devices()) != 1 {
		t.Error(paired, r.Devices())
	}

	// Paired: commands reach the app, marked remote.
	if res, j := c.do("GET", base+"api/list", token, nil); res.StatusCode != 200 || j["seen"] != "list" || j["remote"] != true {
		t.Error(res.StatusCode, j)
	}
	if res, _ := c.do("POST", base+"api/add", token, map[string]string{"url": "https://example.com/a.zip", "queue": "Main"}); res.StatusCode != 200 {
		t.Error(res.StatusCode)
	}
	if add := seen()[len(seen())-1]; !add.Remote || !add.Silent || !add.Later || add.Queue != "Main" || add.URL != "https://example.com/a.zip" {
		t.Errorf("%+v", add)
	}
	// A torrent file: its bytes, never a path.
	if res, _ := c.do("POST", base+"api/torrent?start=true", token, []byte("d4:infod4:name1:xee")); res.StatusCode != 200 {
		t.Error(res.StatusCode)
	}
	if tr := seen()[len(seen())-1]; len(tr.TorrentFile) == 0 || tr.URL != "" || tr.Later {
		t.Errorf("%+v", tr)
	}
	if res, _ := c.do("POST", base+"api/torrent", token, []byte("<html>")); res.StatusCode != 400 {
		t.Error(res.StatusCode)
	}
	// Only the list's actions.
	if res, _ := c.do("POST", base+"api/act", token, map[string]any{"action": "exit", "ids": []string{}}); res.StatusCode != 400 {
		t.Error(res.StatusCode)
	}
	if res, _ := c.do("POST", base+"api/queue", token, map[string]any{"name": "Main", "start": true}); res.StatusCode != 200 {
		t.Error(res.StatusCode)
	}
	if q := seen()[len(seen())-1]; q.Action != "start_queue" || q.Queue != "Main" {
		t.Errorf("%+v", q)
	}

	// Another site's page, a rebinding name: refused before anything else.
	if res, _ := c.do("POST", base+"api/add", token, map[string]string{"url": "https://example.com/b.zip"}, "Origin", "https://evil.example"); res.StatusCode != 403 {
		t.Error(res.StatusCode)
	}
	if res, _ := c.do("GET", base+"api/list", token, nil, "host", "evil.example"); res.StatusCode != 421 {
		t.Error(res.StatusCode)
	}
	if res, _ := c.do("GET", base+"api/list", token, nil, "host", "yf.example"); res.StatusCode != 200 {
		t.Error("a name the person added", res.StatusCode)
	}
	if res, _ := c.do("POST", base+"api/act", token, map[string]any{"action": "pause", "ids": []string{"x"}}, "Origin", strings.TrimSuffix(base, "/")); res.StatusCode != 200 {
		t.Error("its own page", res.StatusCode)
	}
	if res, _ := c.do("POST", base+"api/act", token, map[string]any{"action": "pause", "ids": []string{"x"}}, "Origin", "chrome-extension://abc"); res.StatusCode != 200 {
		t.Error("the extension", res.StatusCode)
	}
	// The page opened from a link on another site: shown (it holds
	// nothing); its API isn't for other sites.
	if res, _ := c.do("GET", base, "", nil, "Sec-Fetch-Site", "cross-site"); res.StatusCode != 200 {
		t.Error(res.StatusCode)
	}
	if res, _ := c.do("GET", base+"api/list", token, nil, "Sec-Fetch-Site", "cross-site"); res.StatusCode != 403 {
		t.Error(res.StatusCode)
	}

	// Plain http: sent on to https.
	if res, _ := c.do("GET", strings.Replace(base, "https", "http", 1), "", nil); res.StatusCode != 301 || res.Header.Get("Location") != base {
		t.Error(res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := c.do("GET", strings.Replace(base, "https", "http", 1), "", nil, "host", "evil.example"); res.StatusCode != 400 {
		t.Error("a redirect to another name", res.StatusCode)
	}

	// Removed in Options: refused at once.
	r.RemoveDevice(r.Devices()[0].ID)
	if res, _ := c.do("GET", base+"api/list", token, nil); res.StatusCode != 401 {
		t.Error(res.StatusCode)
	}

	// Turned off: the port is let go.
	u := strings.TrimSuffix(strings.TrimPrefix(base, "https://"), "/")
	r.Stop()
	if r.Status().State != Off {
		t.Error(r.Status())
	}
	time.Sleep(300 * time.Millisecond)
	l, err := net.Listen("tcp", u)
	if err != nil {
		t.Error("the port is still held:", err)
	} else {
		l.Close()
	}
}

// A device can forget itself.
func TestADeviceForgetsItself(t *testing.T) {
	r, base, _ := start(t)
	c := newClient(t)
	r.StartPairing()
	code, _, _ := r.PairingState()
	_, j := c.do("POST", base+"api/pair", "", map[string]string{"code": code, "name": "Tablet"})
	token, _ := j["token"].(string)
	if res, _ := c.do("POST", base+"api/forget", token, nil); res.StatusCode != 204 {
		t.Error(res.StatusCode)
	}
	if len(r.Devices()) != 0 {
		t.Error(r.Devices())
	}
}

func TestNeverEveryAddress(t *testing.T) {
	r := New(t.TempDir(), ipc.NewSink(func(ipc.Incoming) bool { return false }), nil, nil, nil)
	defer r.Stop()
	for _, listen := range []string{"0.0.0.0", "::", "203.0.113.9"} {
		r.Apply(Config{On: true, Listen: listen})
		if st := r.Status(); st.State != NoAddress {
			t.Error(listen, st)
		}
	}
}

func TestATakenPortIsSaid(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	r := New(t.TempDir(), ipc.NewSink(func(ipc.Incoming) bool { return false }), nil, nil, nil)
	defer r.Stop()
	port := l.Addr().(*net.TCPAddr).Port
	r.Apply(Config{On: true, Port: port, Listen: ThisComputer})
	if st := r.Status(); st.State != PortInUse || st.Port != port {
		t.Error(st)
	}
}
