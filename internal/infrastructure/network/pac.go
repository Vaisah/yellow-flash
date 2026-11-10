// Ported from Mister Download Manager, crates/engine/src/pac.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// Proxy auto-config: the script's FindProxyForURL(url, host) decides the
// proxy for each address. It runs in a small JavaScript engine (goja), with
// the helper functions PAC scripts expect written here and DNS answered by
// the system. Answers are kept per script and URL. The script comes from
// the network: it gets the language only (no files, no modules, no binary
// buffers), a limited call stack, memory and time.

// PACRoute is where a request goes, in the script's order of preference.
type PACRoute struct {
	Direct bool
	// Proxy is "http://host:port", "https://…", "socks5://…" or "socks4://…".
	Proxy string
}

const (
	// pacTimeLimit bounds the script's own running time; the system's DNS
	// answers are not counted, up to pacDNSLimit of them, so a script that
	// keeps looking up slow names still ends.
	pacTimeLimit = 2 * time.Second
	pacDNSLimit  = 30 * time.Second
	// pacMemoryLimit is how much the heap may grow while a script runs (a
	// PAC script needs well under 1 MB).
	pacMemoryLimit = 64 << 20
	// pacStackLimit stops deep recursion.
	pacStackLimit = 2000
	// pacCacheMax answers are kept before the cache starts over.
	pacCacheMax = 4096
)

var (
	pacCacheMu sync.Mutex
	pacCache   = map[[2]string][]PACRoute{}
)

// PACRoutes returns the routes for u under script: its answer's entries, in
// order (the first usable one is taken). It fails when the script does not
// run.
func PACRoutes(script string, u *url.URL) ([]PACRoute, error) {
	host := strings.ToLower(u.Hostname())
	h := fnv.New64a()
	_, _ = h.Write([]byte(script))
	// Kept per script and URL: a script may decide by the URL's path
	// (shExpMatch(url, "*/downloads/*")), so two addresses on one host can
	// go different ways. (MDM keyed by host, which reused one path's answer
	// for the other.) A download's parts ask the same URL, so they hit.
	key := [2]string{fmt.Sprintf("%x", h.Sum64()), u.String()}
	pacCacheMu.Lock()
	if r, ok := pacCache[key]; ok {
		pacCacheMu.Unlock()
		return r, nil
	}
	pacCacheMu.Unlock()
	answer, err := runPAC(script, u.String(), host)
	if err != nil {
		return nil, err
	}
	r := ParsePACAnswer(answer)
	pacCacheMu.Lock()
	if len(pacCache) >= pacCacheMax {
		pacCache = map[[2]string][]PACRoute{}
	}
	pacCache[key] = r
	pacCacheMu.Unlock()
	return r, nil
}

// ParsePACAnswer reads FindProxyForURL's answer: "PROXY a:8080; SOCKS
// b:1080; DIRECT" → routes, unknown kinds skipped. An empty answer means
// direct.
func ParsePACAnswer(answer string) []PACRoute {
	schemes := map[string]string{"PROXY": "http", "HTTP": "http", "HTTPS": "https",
		"SOCKS": "socks5", "SOCKS5": "socks5", "SOCKS4": "socks4"}
	var out []PACRoute
	for _, entry := range strings.Split(answer, ";") {
		f := strings.Fields(entry)
		if len(f) == 0 {
			continue
		}
		kind := strings.ToUpper(f[0])
		if kind == "DIRECT" {
			out = append(out, PACRoute{Direct: true})
			continue
		}
		if scheme := schemes[kind]; scheme != "" && len(f) >= 2 {
			out = append(out, PACRoute{Proxy: scheme + "://" + f[1]})
		}
	}
	if len(out) == 0 {
		out = append(out, PACRoute{Direct: true})
	}
	return out
}

// pacProxy is a Transport.Proxy function deciding by script. Go's transport
// speaks to http, https and socks5 proxies; a socks4 entry is skipped for
// the next one.
func pacProxy(script string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		routes, err := PACRoutes(script, req.URL)
		if err != nil {
			return nil, fmt.Errorf("proxy auto-config script: %w", err)
		}
		for _, r := range routes {
			if r.Direct {
				return nil, nil
			}
			if strings.HasPrefix(r.Proxy, "socks4://") {
				continue
			}
			if p, err := url.Parse(r.Proxy); err == nil {
				return p, nil
			}
		}
		return nil, nil
	}
}

var errPACMemory = errors.New("the script used too much memory")

// runPAC runs the script and calls FindProxyForURL(url, host).
func runPAC(script, rawURL, host string) (answer string, err error) {
	vm := goja.New()
	vm.SetMaxCallStackSize(pacStackLimit)
	// The language only: no binary buffers (they allocate in one step,
	// beyond any watchdog), nothing a PAC script needs.
	for _, name := range []string{"ArrayBuffer", "SharedArrayBuffer", "DataView", "Int8Array", "Uint8Array",
		"Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array", "Float32Array",
		"Float64Array", "BigInt64Array", "BigUint64Array", "WeakRef", "FinalizationRegistry"} {
		_ = vm.GlobalObject().Delete(name)
	}

	// The time limit, moved on by the time DNS lookups take, never past last.
	var mu sync.Mutex
	begun := time.Now()
	end, last := begun.Add(pacTimeLimit), begun.Add(pacTimeLimit+pacDNSLimit)
	done := make(chan struct{})
	defer close(done)
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	go func() {
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-tick.C:
				mu.Lock()
				over := now.After(end)
				mu.Unlock()
				if over {
					vm.Interrupt("interrupted: the script ran too long")
					return
				}
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > base.HeapAlloc+pacMemoryLimit {
					vm.Interrupt(errPACMemory)
					return
				}
			}
		}
	}()

	resolve := func(h string) string {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ips, err := systemLookup(ctx, h)
		mu.Lock()
		end = end.Add(time.Since(start))
		if end.After(last) {
			end = last
		}
		mu.Unlock()
		if err != nil {
			return ""
		}
		for _, ip := range ips {
			if ip.Unmap().Is4() {
				return ip.Unmap().String()
			}
		}
		return ""
	}
	if err = vm.Set("__mdmResolve", resolve); err == nil {
		err = vm.Set("__mdmMyIp", myIPAddress)
	}
	if err != nil {
		return "", err
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the script failed: %v", r)
		}
	}()
	if _, err = vm.RunString(pacHelpers); err != nil {
		return "", pacError(err)
	}
	if _, err = vm.RunString(script); err != nil {
		return "", pacError(err)
	}
	find, ok := goja.AssertFunction(vm.Get("FindProxyForURL"))
	if !ok {
		return "", errors.New("the script has no FindProxyForURL")
	}
	v, err := find(goja.Undefined(), vm.ToValue(rawURL), vm.ToValue(host))
	if err != nil {
		return "", pacError(err)
	}
	return v.String(), nil
}

func pacError(err error) error {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		if v, ok := ie.Value().(error); ok {
			return v
		}
		return fmt.Errorf("%v", ie.Value())
	}
	var so *goja.StackOverflowError
	if errors.As(err, &so) {
		return errors.New("the script recursed too deep (stack overflow)")
	}
	return err
}

// myIPAddress is this computer's address on its network: the one a
// connection out would use (a UDP socket is connected, nothing is sent),
// else 127.0.0.1.
func myIPAddress() string {
	c, err := net.Dial("udp4", "192.0.2.1:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP.To4() != nil {
		return a.IP.String()
	}
	return "127.0.0.1"
}

// pacHelpers are the functions PAC scripts may call (as browsers provide
// them), MDM's own implementation from the PAC definition.
const pacHelpers = `
function isPlainHostName(host) { return String(host).indexOf('.') < 0; }
function dnsDomainIs(host, domain) {
  host = String(host).toLowerCase(); domain = String(domain).toLowerCase();
  return host.length >= domain.length && host.substring(host.length - domain.length) === domain;
}
function localHostOrDomainIs(host, hostdom) {
  host = String(host).toLowerCase(); hostdom = String(hostdom).toLowerCase();
  return host === hostdom || (host.indexOf('.') < 0 && hostdom.indexOf(host + '.') === 0);
}
function dnsResolve(host) { var a = __mdmResolve(String(host)); return a === '' ? null : a; }
function isResolvable(host) { return dnsResolve(host) !== null; }
function myIpAddress() { return __mdmMyIp(); }
function dnsDomainLevels(host) { return String(host).split('.').length - 1; }
function __mdmAddr(s) {
  var p = String(s).split('.');
  if (p.length !== 4) return null;
  var n = 0;
  for (var i = 0; i < 4; i++) {
    if (!/^\d+$/.test(p[i])) return null;
    var x = parseInt(p[i], 10);
    if (x > 255) return null;
    n = n * 256 + x;
  }
  return n;
}
function convert_addr(ipchars) { var n = __mdmAddr(ipchars); return n === null ? 0 : n; }
function isInNet(host, pattern, mask) {
  var ip = __mdmAddr(host) !== null ? host : dnsResolve(host);
  if (ip === null) return false;
  var a = __mdmAddr(ip), p = __mdmAddr(pattern), m = __mdmAddr(mask);
  if (a === null || p === null || m === null) return false;
  return ((a & m) >>> 0) === ((p & m) >>> 0);
}
function shExpMatch(str, exp) {
  var re = String(exp).replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.');
  return new RegExp('^' + re + '$').test(String(str));
}
var __mdmDays = ['SUN', 'MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT'];
var __mdmMonths = ['JAN', 'FEB', 'MAR', 'APR', 'MAY', 'JUN', 'JUL', 'AUG', 'SEP', 'OCT', 'NOV', 'DEC'];
function __mdmGmt(args) { return args.length > 0 && String(args[args.length - 1]).toUpperCase() === 'GMT'; }
function __mdmIn(v, lo, hi) { return lo <= hi ? (v >= lo && v <= hi) : (v >= lo || v <= hi); }
function weekdayRange() {
  var a = Array.prototype.slice.call(arguments), gmt = __mdmGmt(a);
  if (gmt) a.pop();
  var now = new Date(), d = gmt ? now.getUTCDay() : now.getDay();
  var d1 = __mdmDays.indexOf(String(a[0]).toUpperCase());
  var d2 = a.length > 1 ? __mdmDays.indexOf(String(a[1]).toUpperCase()) : d1;
  if (d1 < 0 || d2 < 0) return false;
  return __mdmIn(d, d1, d2);
}
function timeRange() {
  var a = Array.prototype.slice.call(arguments), gmt = __mdmGmt(a);
  if (gmt) a.pop();
  a = a.map(function (x) { return parseInt(x, 10); });
  var now = new Date();
  var h = gmt ? now.getUTCHours() : now.getHours();
  var m = gmt ? now.getUTCMinutes() : now.getMinutes();
  var s = gmt ? now.getUTCSeconds() : now.getSeconds();
  var t = h * 3600 + m * 60 + s;
  if (a.length === 1) return h === a[0];
  if (a.length === 2) return __mdmIn(h, a[0], a[1]);
  if (a.length === 4) return __mdmIn(t, a[0] * 3600 + a[1] * 60, a[2] * 3600 + a[3] * 60 + 59);
  if (a.length === 6) return __mdmIn(t, a[0] * 3600 + a[1] * 60 + a[2], a[3] * 3600 + a[4] * 60 + a[5]);
  return false;
}
function dateRange() {
  var a = Array.prototype.slice.call(arguments), gmt = __mdmGmt(a);
  if (gmt) a.pop();
  var now = new Date();
  var cur = { d: gmt ? now.getUTCDate() : now.getDate(), m: gmt ? now.getUTCMonth() : now.getMonth(),
              y: gmt ? now.getUTCFullYear() : now.getFullYear() };
  var parts = a.map(function (x) {
    var i = __mdmMonths.indexOf(String(x).toUpperCase());
    if (i >= 0) return { k: 'm', v: i };
    var n = parseInt(x, 10);
    return n > 31 ? { k: 'y', v: n } : { k: 'd', v: n };
  });
  if (parts.length === 1) return cur[parts[0].k] === parts[0].v;
  var kinds = {};
  parts.forEach(function (p) { kinds[p.k] = true; });
  var key = function (x) { return (kinds.y ? x.y : 0) * 10000 + (kinds.m ? x.m : 0) * 100 + (kinds.d ? x.d : 0); };
  var end = function (ps) {
    var x = { y: cur.y, m: cur.m, d: cur.d };
    ps.forEach(function (p) { x[p.k] = p.v; });
    return x;
  };
  var half = parts.length / 2;
  return __mdmIn(key(cur), key(end(parts.slice(0, half))), key(end(parts.slice(half))));
}
function alert() {}
`
