// Ported from Mister Download Manager, crates/engine/src/dns.rs (the address race)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// A dead address must not stall every new connection. Go's dialer tries a
// host's addresses of one family one after another, each with a share of
// the timeout, so a host whose first address is dead costs seconds per new
// connection. When a host has several addresses, connects race, staggered
// as in RFC 8305, and the first that answers is used; the winner is
// remembered for a while so later connections go to it first. MDM probes
// and reorders; here the winning connection is used directly.

const (
	// stagger is the head start each address gets before the next one is
	// tried too (RFC 8305: 250 ms).
	stagger = 250 * time.Millisecond
	// rememberWinner is how long a winner is tried first without racing.
	rememberWinner = 5 * time.Minute
)

type winner struct {
	ip netip.Addr
	at time.Time
}

var (
	winnersMu sync.Mutex
	winners   = map[string]winner{}
)

// hostDialer resolves names through a Resolver (nil: the system's) and
// races a host's addresses. base dials each address, so its Control (the
// SSRF policy) checks every one.
type hostDialer struct {
	base *net.Dialer
	dns  *Resolver
}

func (d *hostDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return d.base.DialContext(ctx, network, addr)
	}
	ips, err := d.dns.LookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	ips = forNetwork(ips, network)
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no address for this network", Name: host, IsNotFound: true}
	}
	key := strings.ToLower(host)
	winnersMu.Lock()
	if w, ok := winners[key]; ok && time.Since(w.at) < rememberWinner {
		for i, ip := range ips {
			if ip == w.ip {
				ips[0], ips[i] = ips[i], ips[0]
				break
			}
		}
	}
	winnersMu.Unlock()
	conn, i, err := d.race(ctx, network, ips, port)
	if err != nil {
		return nil, err
	}
	if len(ips) > 1 {
		winnersMu.Lock()
		winners[key] = winner{ip: ips[i], at: time.Now()}
		winnersMu.Unlock()
	}
	return conn, nil
}

func forNetwork(ips []netip.Addr, network string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		switch {
		case network == "tcp4" && !ip.Is4(), network == "tcp6" && !ip.Is6():
		default:
			out = append(out, ip)
		}
	}
	return out
}

type attempt struct {
	conn net.Conn
	i    int
	err  error
}

// closeLate closes the connections of attempts still running once the race
// is decided.
func closeLate(results <-chan attempt, left int) {
	for ; left > 0; left-- {
		if late := <-results; late.conn != nil {
			_ = late.conn.Close()
		}
	}
}

// race connects to ips, each attempt getting a stagger head start; a failed
// attempt starts the next one at once. The first connection made wins; the
// others are closed. It returns the winner's index.
func (d *hostDialer) race(ctx context.Context, network string, ips []netip.Addr, port string) (net.Conn, int, error) {
	if len(ips) == 1 {
		c, err := d.base.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		return c, 0, err
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan attempt, len(ips))
	next, running := 0, 0
	var errs []error
	timer := time.NewTimer(stagger)
	defer timer.Stop()
	for {
		if next < len(ips) {
			i := next
			go func() {
				c, err := d.base.DialContext(rctx, network, net.JoinHostPort(ips[i].String(), port))
				results <- attempt{c, i, err}
			}()
			next++
			running++
			timer.Reset(stagger)
		}
		if running == 0 {
			return nil, 0, errors.Join(errs...)
		}
		var headStart <-chan time.Time
		if next < len(ips) {
			headStart = timer.C
		}
		select {
		case a := <-results:
			running--
			if a.err == nil {
				cancel()
				go closeLate(results, running)
				return a.conn, a.i, nil
			}
			errs = append(errs, a.err) // failed: the next address starts at once
		case <-headStart: // the head start is over: the next address joins the race
		case <-ctx.Done():
			go closeLate(results, running)
			return nil, 0, ctx.Err()
		}
	}
}
