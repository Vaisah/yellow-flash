// Ported from Mister Download Manager, crates/tools/src/servers.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// Media servers that can't be reached, swapped for the next one the site
// lists. YouTube hands yt-dlp links to one cache server
// (rrN---sn-XXXX.googlevideo.com) and names its alternatives in the link's
// mn parameter (mn=sn-XXXX,sn-YYYY); a browser's player falls over to them
// when the first is dead, yt-dlp doesn't. The host is not part of the
// link's signature (sparams), so the same link works on the alternative.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// connectWait is how long a server gets to accept a connection.
const connectWait = 2 * time.Second

// Reachable reports whether host accepts a connection on 443: it resolves
// to a real address (a hosts-file block answers 0.0.0.0 or ::) and one of
// those connects in time.
func Reachable(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*connectWait)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a.Unmap().IsUnspecified() {
			continue
		}
		c, err := net.DialTimeout("tcp", netip.AddrPortFrom(a.Unmap(), 443).String(), connectWait)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

// StreamProber asks media servers whether they really serve a stream: a 1
// KiB request the way yt-dlp asks (&range= in the link), with the stream's
// own headers, one at a time over one client (servers refuse a burst).
type StreamProber struct{ Client *http.Client }

// Serves reports whether link answers with the stream.
func (p StreamProber) Serves(link string, headers map[string]string) bool {
	sep := "?"
	if strings.Contains(link, "?") {
		sep = "&"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link+sep+"range=0-1023", nil)
	if err != nil {
		return false
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := p.Client.Do(req)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode >= 200 && res.StatusCode < 300
}

// number reads a JSON number as decoded with or without UseNumber.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// eachString calls f on every string in v (links sit in formats[].url,
// requested_formats, fragments…).
func eachString(v any, f func(string)) {
	switch t := v.(type) {
	case string:
		f(t)
	case []any:
		for _, x := range t {
			eachString(x, f)
		}
	case map[string]any:
		for _, x := range t {
			eachString(x, f)
		}
	}
}

// alternatives are the servers to try instead of host, from a link's mn
// list: rr1---sn-A.googlevideo.com with mn=sn-A,sn-B gives
// rr1---sn-B.googlevideo.com.
func alternatives(host string, link *url.URL) []string {
	prefix, rest, ok := strings.Cut(host, "---")
	if !ok {
		return nil
	}
	node, domain, ok := strings.Cut(rest, ".")
	if !ok {
		return nil
	}
	var out []string
	for _, n := range strings.Split(link.Query().Get("mn"), ",") {
		if n = strings.TrimSpace(n); n != "" && n != node {
			out = append(out, prefix+"---"+n+"."+domain)
		}
	}
	return out
}

// headersOf is a format's request headers (http_headers in yt-dlp's
// information).
func headersOf(format map[string]any) map[string]string {
	out := map[string]string{}
	if h, ok := format["http_headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// Swap is a dead server and the one put in its place.
type Swap struct{ Dead, Alternative string }

// Rerouted is what Reroute changed.
type Rerouted struct {
	// Swaps are each (dead, replacement) pair once.
	Swaps []Swap
	// LeftOut are the streams no reachable alternative serves, as "137
	// (1080p)", for the download's log (a chosen quality may be among them).
	LeftOut []string
}

// Empty reports that nothing changed.
func (r Rerouted) Empty() bool { return len(r.Swaps) == 0 && len(r.LeftOut) == 0 }

// describe names a format for the log: its id and its height (or "audio").
func describe(f map[string]any) string {
	id, _ := f["format_id"].(string)
	if id == "" {
		id = "?"
	}
	if h, ok := number(f["height"]); ok {
		return fmt.Sprintf("%s (%dp)", id, int64(h))
	}
	if f["vcodec"] == "none" {
		return id + " (audio)"
	}
	return id
}

// UnreachableIn are the media servers yt-dlp's failure names as
// unreachable: it couldn't resolve one, or connect to it, or it timed out.
// A server answered at look-up time may be gone by download time: the next
// look-up treats these as dead.
func UnreachableIn(message string) []string {
	network := false
	for _, w := range []string{"Failed to resolve", "getaddrinfo", "Unable to connect", "timed out", "Connection refused", "Max retries"} {
		network = network || strings.Contains(message, w)
	}
	if !network {
		return nil
	}
	words := strings.FieldsFunc(message, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.')
	})
	var out []string
	for _, w := range words {
		if strings.HasSuffix(w, ".googlevideo.com") && strings.Contains(w, "---") && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// FormatUnavailable reports yt-dlp couldn't find the chosen format among
// the ones the page (or our edit of it) offers.
func FormatUnavailable(message string) bool {
	return strings.Contains(message, "Requested format is not available")
}

// Reroute moves the streams of unreachable media servers to an alternative
// that serves them, and says what changed. alive decides whether a server
// can be reached (all at once), serves whether it really serves one stream
// (one after another). A stream no reachable alternative serves is left
// out, so yt-dlp chooses an equivalent one. info is yt-dlp's -J output
// (decode it with UseNumber, so its numbers come back as they were).
func Reroute(info map[string]any, alive func(host string) bool, serves func(link string, headers map[string]string) bool) Rerouted {
	// Media hosts with an mn list, and one link of each (for its
	// alternatives).
	links := map[string]*url.URL{}
	eachString(info, func(s string) {
		if u, err := url.Parse(s); err == nil && strings.HasSuffix(u.Hostname(), ".googlevideo.com") && u.Query().Has("mn") {
			if _, seen := links[u.Hostname()]; !seen {
				links[u.Hostname()] = u
			}
		}
	})
	candidates := map[string]bool{}
	for h, u := range links {
		candidates[h] = true
		for _, a := range alternatives(h, u) {
			candidates[a] = true
		}
	}
	up := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for h := range candidates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if alive(h) {
				mu.Lock()
				up[h] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	dead := map[string]bool{}
	for h, u := range links {
		if !up[h] && slices.ContainsFunc(alternatives(h, u), func(a string) bool { return up[a] }) {
			dead[h] = true
		}
	}
	formats, ok := info["formats"].([]any)
	if len(dead) == 0 || !ok {
		return Rerouted{}
	}
	// HLS formats list their pieces in a manifest fetched later, on the
	// same servers, which can't be edited here: leave them out so the
	// choice falls on direct links.
	formats = slices.DeleteFunc(formats, func(f any) bool {
		m, _ := f.(map[string]any)
		p, _ := m["protocol"].(string)
		return strings.HasPrefix(p, "m3u8")
	})
	var r Rerouted
	kept := formats[:0]
	for _, f := range formats {
		m, ok := f.(map[string]any)
		raw, _ := m["url"].(string)
		link, err := url.Parse(raw)
		if !ok || err != nil || !dead[link.Hostname()] {
			kept = append(kept, f)
			continue
		}
		host := link.Hostname()
		headers := headersOf(m)
		to := ""
		for _, a := range alternatives(host, link) {
			if !up[a] {
				continue
			}
			moved := strings.Replace(raw, "://"+host+"/", "://"+a+"/", 1)
			if serves(moved, headers) {
				to = a
				break
			}
		}
		if to == "" {
			r.LeftOut = append(r.LeftOut, describe(m))
			continue
		}
		m["url"] = strings.Replace(raw, "://"+host+"/", "://"+to+"/", 1)
		if !slices.Contains(r.Swaps, Swap{host, to}) {
			r.Swaps = append(r.Swaps, Swap{host, to})
		}
		kept = append(kept, f)
	}
	info["formats"] = kept
	// yt-dlp chooses again from formats; its earlier choice would still name
	// the dead server.
	delete(info, "requested_formats")
	return r
}
