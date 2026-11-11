// Ported from Mister Download Manager, crates/engine/src/server.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

// Where a download's answers come from: the address that answered (the
// server's, or the proxy's when one carried the request), the round trip
// to it, and the CDN its headers name. A download's context carries a
// ServerSeen (WithServerSeen); the client fills it as its connections open
// and its answers come. The round trip is the quickest TCP connect: one
// round trip, the name lookup left out.

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Server is what a download learnt of its server.
type Server struct {
	// IP is the address that answered: the server's, or the proxy's.
	IP netip.Addr
	// RTT is the round trip to it (0: not measured, a reused connection).
	RTT time.Duration
	// Proxied: a proxy carried the requests; IP and RTT are the proxy's.
	Proxied bool
	// CDN is the CDN the answer names (its headers): the address is one of
	// its servers near the downloader, not the site owner's.
	CDN string
}

// ServerSeen is a download's Server, filled as its connections open and
// its answers come.
type ServerSeen struct {
	mu sync.Mutex
	s  Server
}

// Get is what was seen; false when nothing was.
func (s *ServerSeen) Get() (Server, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s, s.s.IP.IsValid() || s.s.RTT > 0
}

func (s *ServerSeen) connected(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d > 0 && (s.s.RTT == 0 || d < s.s.RTT) {
		s.s.RTT = d
	}
}

func (s *ServerSeen) answeredBy(conn net.Conn) {
	addr := conn.RemoteAddr()
	if tc, ok := conn.(*tls.Conn); ok {
		addr = tc.NetConn().RemoteAddr()
	}
	if addr == nil {
		return
	}
	if ap, err := netip.ParseAddrPort(addr.String()); err == nil {
		s.mu.Lock()
		s.s.IP = ap.Addr().Unmap()
		s.mu.Unlock()
	}
}

func (s *ServerSeen) setProxied() {
	s.mu.Lock()
	s.s.Proxied = true
	s.mu.Unlock()
}

func (s *ServerSeen) answered(h http.Header) {
	if cdn := CDNOf(h); cdn != "" {
		s.mu.Lock()
		s.s.CDN = cdn
		s.mu.Unlock()
	}
}

type serverSeenKey struct{}

// WithServerSeen makes the requests made with ctx tell s where they went.
func WithServerSeen(ctx context.Context, s *ServerSeen) context.Context {
	return context.WithValue(ctx, serverSeenKey{}, s)
}

func serverSeenOf(ctx context.Context) *ServerSeen {
	s, _ := ctx.Value(serverSeenKey{}).(*ServerSeen)
	return s
}

// seenProxy wraps a proxy choice: a request that goes through one says so.
func seenProxy(proxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		u, err := proxy(req)
		if u != nil && err == nil {
			if s := serverSeenOf(req.Context()); s != nil {
				s.setProxied()
			}
		}
		return u, err
	}
}

// seenTripper times each request's connections and notes who answered.
type seenTripper struct{ base http.RoundTripper }

func (t seenTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s := serverSeenOf(req.Context())
	if s == nil {
		return t.base.RoundTrip(req)
	}
	var (
		mu     sync.Mutex
		starts = map[string]time.Time{}
	)
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, addr string) {
			mu.Lock()
			starts[addr] = time.Now()
			mu.Unlock()
		},
		ConnectDone: func(_, addr string, err error) {
			mu.Lock()
			start, ok := starts[addr]
			mu.Unlock()
			if ok && err == nil {
				s.connected(time.Since(start))
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Conn != nil {
				s.answeredBy(info.Conn)
			}
		},
	}
	res, err := t.base.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	if err == nil {
		s.answered(res.Header)
	}
	return res, err
}

// CDNOf is the CDN an answer's headers name, by the headers each one sets
// on what it serves ("": none named).
func CDNOf(h http.Header) string {
	has := func(name string) bool { return h.Get(name) != "" }
	says := func(name, what string) bool { return strings.Contains(strings.ToLower(h.Get(name)), what) }
	switch {
	case has("Cf-Ray") || says("Server", "cloudflare"):
		return "Cloudflare"
	case has("X-Amz-Cf-Id") || says("Via", "cloudfront"):
		return "Amazon CloudFront"
	case says("X-Served-By", "cache-") || has("X-Fastly-Request-Id"):
		return "Fastly"
	case says("Server", "akamai") || has("X-Akamai-Transformed") || has("Akamai-Grn"):
		return "Akamai"
	case has("X-Azure-Ref") || has("X-Msedge-Ref"):
		return "Azure Front Door"
	case has("X-Goog-Generation") || says("Server", "gws") || says("Via", "google"):
		return "Google"
	case has("X-Bunny-Cache") || says("Server", "bunnycdn") || has("Cdn-Requestid"):
		return "Bunny CDN"
	}
	return ""
}
