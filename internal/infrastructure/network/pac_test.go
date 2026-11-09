// Ported from Mister Download Manager, crates/engine/src/pac.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func route(t *testing.T, script, raw string) []PACRoute {
	t.Helper()
	u, _ := url.Parse(raw)
	r, err := PACRoutes(script, u)
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return r
}

func TestPACAnswersBecomeRoutes(t *testing.T) {
	got := ParsePACAnswer("PROXY proxy.corp:8080; SOCKS s.corp:1080; DIRECT")
	want := []PACRoute{{Proxy: "http://proxy.corp:8080"}, {Proxy: "socks5://s.corp:1080"}, {Direct: true}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v", got)
	}
	if got = ParsePACAnswer(""); len(got) != 1 || !got[0].Direct {
		t.Fatalf("empty: %v", got)
	}
	if got = ParsePACAnswer("WEIRD x; DIRECT"); len(got) != 1 || !got[0].Direct {
		t.Fatalf("unknown kind: %v", got)
	}
}

func TestATypicalPACScriptIsRun(t *testing.T) {
	script := `
		function FindProxyForURL(url, host) {
			if (isPlainHostName(host) || dnsDomainIs(host, ".intranet.test")) return "DIRECT";
			if (shExpMatch(url, "*/downloads/*.iso")) return "PROXY iso.proxy:3128";
			if (isInNet("10.1.2.3", "10.0.0.0", "255.0.0.0") && dnsDomainLevels(host) > 1)
				return "PROXY proxy.corp:8080; DIRECT";
			return "DIRECT";
		}`
	if r := route(t, script, "http://intranet/a"); !r[0].Direct {
		t.Errorf("plain host: %v", r)
	}
	if r := route(t, script, "https://wiki.intranet.test/x"); !r[0].Direct {
		t.Errorf("intranet: %v", r)
	}
	if r := route(t, script, "https://mirror.example.org/downloads/os.iso"); r[0].Proxy != "http://iso.proxy:3128" {
		t.Errorf("iso: %v", r)
	}
	if r := route(t, script, "https://cdn.example.org/a.zip"); r[0].Proxy != "http://proxy.corp:8080" {
		t.Errorf("default: %v", r)
	}
}

func TestPACHelpersBehave(t *testing.T) {
	noSystem(t) // localhost → 127.0.0.1, without the network
	check := func(expr string) bool {
		script := "function FindProxyForURL(u, h) { return (" + expr + ") ? 'DIRECT' : 'PROXY no:1'; }"
		return route(t, script, "http://x.test/")[0].Direct
	}
	for expr, want := range map[string]bool{
		"localHostOrDomainIs('www', 'www.example.org')":                              true,
		"localHostOrDomainIs('www.other.org', 'www.example.org')":                    false,
		"isInNet('192.168.1.20', '192.168.0.0', '255.255.0.0')":                      true,
		"isInNet('10.0.0.1', '192.168.0.0', '255.255.0.0')":                          false,
		"convert_addr('104.16.41.2') === 1745889538":                                 true,
		"shExpMatch('a.b.c', 'a.?.c') && !shExpMatch('abc', 'a?')":                   true,
		"isResolvable('localhost') && dnsResolve('localhost') === '127.0.0.1'":       true,
		"typeof myIpAddress() === 'string' && myIpAddress().split('.').length === 4": true,
		"weekdayRange('SUN', 'SAT') && timeRange(0, 23)":                             true,
		"dateRange('JAN', 'DEC')":                                                    true,
		"typeof ArrayBuffer === 'undefined' && typeof Uint8Array === 'undefined'":    true,
	} {
		if got := check(expr); got != want {
			t.Errorf("%s = %v", expr, got)
		}
	}
}

func TestABrokenPACScriptIsAnError(t *testing.T) {
	u, _ := url.Parse("http://x/")
	if _, err := PACRoutes("this is not javascript (", u); err == nil {
		t.Error("syntax error accepted")
	}
	if _, err := PACRoutes("var x = 1;", u); err == nil || !strings.Contains(err.Error(), "FindProxyForURL") {
		t.Errorf("no function: %v", err)
	}
}

func TestARunawayPACScriptIsStopped(t *testing.T) {
	u, _ := url.Parse("http://x/")
	fails := func(body string) string {
		t.Helper()
		_, err := PACRoutes("function FindProxyForURL(u, h) { "+body+" }", u)
		if err == nil {
			t.Fatalf("%s: ran", body)
		}
		return err.Error()
	}
	// Endless, even catching what stops it: stopped at the time limit.
	start := time.Now()
	if e := fails("for (;;) { try { for (;;) {} } catch (e) {} }"); !strings.Contains(e, "too long") {
		t.Errorf("endless: %s", e)
	}
	if took := time.Since(start); took > 3*pacTimeLimit {
		t.Errorf("took %v", took)
	}
	// Ever more memory: stopped by the watchdog (or the clock).
	if e := fails("var a = []; for (;;) a.push(new Array(1e5).fill(1.5)); return 'DIRECT';"); !strings.Contains(e, "memory") && !strings.Contains(e, "too long") {
		t.Errorf("memory: %s", e)
	}
	// One huge buffer: not there at all.
	if e := fails("var b = new ArrayBuffer(1 << 30); return 'DIRECT';"); !strings.Contains(e, "not defined") {
		t.Errorf("buffer: %s", e)
	}
	// Endless recursion: stopped at the stack limit.
	if e := fails("function f(n) { return f(n + 1) + 1; } return f(0);"); !strings.Contains(e, "stack") {
		t.Errorf("recursion: %s", e)
	}
	// The script itself endless, before FindProxyForURL is called.
	if _, err := PACRoutes("while (true) {}", u); err == nil {
		t.Error("endless top level ran")
	}
}

// A client with a PAC script sends each request where the script says.
func TestAClientFollowsItsPACScript(t *testing.T) {
	var viaProxy string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaProxy = r.URL.String() // a proxy request carries the absolute URL
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	defer direct.Close()
	paddr := strings.TrimPrefix(proxy.URL, "http://")
	script := `function FindProxyForURL(url, host) {
		if (shExpMatch(url, "*/via-proxy/*")) return "SOCKS4 old:1080; PROXY ` + paddr + `";
		return "DIRECT";
	}`
	c := NewClient(Options{AllowPrivate: true, PACScript: script})
	get := func(path string) string {
		res, err := c.Get(direct.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	if got := get("/via-proxy/a.zip"); got != "proxied" || !strings.HasSuffix(viaProxy, "/via-proxy/a.zip") {
		t.Fatalf("proxied: %q via %q", got, viaProxy)
	}
	if got := get("/b.zip"); got != "direct" {
		t.Fatalf("direct: %q", got)
	}
}
