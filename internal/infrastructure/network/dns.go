// Ported from Mister Download Manager, crates/engine/src/dns.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Names are looked up by the system's DNS (the default) or by a DNS the
// user chose: a plain DNS server, DNS over TLS or DNS over HTTPS. The
// system's hosts file and local names (localhost, single-label names,
// .local, .lan, .home.arpa, .internal) stay with the system either way: a
// public DNS cannot know them. When the chosen DNS cannot be reached the
// lookup fails, unless the user allowed falling back to the system's DNS; a
// name it says does not exist never falls back (that would undo a
// filtering DNS).

// DNSMode is how the chosen DNS is asked.
type DNSMode int

// DNS modes.
const (
	// DNSSystem is the system's resolver (its cache, the hosts file, the
	// OS's own encrypted DNS if set up there).
	DNSSystem DNSMode = iota
	// DNSPlain asks DNS servers over TCP (never UDP: a socket waiting for
	// UDP answers counts as listening to Windows' firewall), port 53
	// unless given.
	DNSPlain
	// DNSTLS is DNS over TLS (RFC 7858), port 853 unless given.
	DNSTLS
	// DNSHTTPS is DNS over HTTPS (RFC 8484).
	DNSHTTPS
)

// DNSChoice is the DNS chosen, how strict it is, and its name for messages.
type DNSChoice struct {
	Mode DNSMode
	// Servers are asked in order, IPv4 first (DNSPlain, DNSTLS).
	Servers []netip.AddrPort
	// TLSName is what a DNSTLS server's certificate must show.
	TLSName string
	// URL is the DNSHTTPS address, https://host[:port]/path.
	URL string
	// Bootstrap are the DNSHTTPS host's addresses (empty: an IP literal
	// URL, or the system's DNS finds them).
	Bootstrap []netip.Addr
	// Fallback asks the system's DNS when the chosen one cannot be reached.
	// Never when it answered that a name does not exist.
	Fallback bool
	// Label names it in errors: "Cloudflare (DNS over HTTPS)".
	Label string
	// RootCAs verifies DNSTLS and DNSHTTPS servers; nil uses the system's.
	RootCAs *x509.CertPool
}

// IsSystem reports the system's resolver.
func (c *DNSChoice) IsSystem() bool { return c == nil || c.Mode == DNSSystem }

// DNSErrorKind says why a lookup through the chosen DNS failed.
type DNSErrorKind int

// DNS error kinds.
const (
	// DNSNoSuchName: the DNS answered that the name does not exist (or has
	// no address).
	DNSNoSuchName DNSErrorKind = iota + 1
	// DNSUnreachable: the DNS could not be asked or did not answer.
	DNSUnreachable
	// DNSSetup: the choice itself is wrong.
	DNSSetup
)

// DNSError is a failed lookup through the chosen DNS.
type DNSError struct {
	Kind DNSErrorKind
	DNS  string
	Host string
	Why  string
}

func (e *DNSError) Error() string {
	switch e.Kind {
	case DNSNoSuchName:
		return fmt.Sprintf("%s says %s doesn't exist", e.DNS, e.Host)
	case DNSUnreachable:
		return fmt.Sprintf("couldn't look up %s with %s: %s", e.Host, e.DNS, e.Why)
	}
	return e.DNS + ": " + e.Why
}

// DNSPresetMode is how a preset is asked.
type DNSPresetMode int

// Preset modes.
const (
	PresetPlain DNSPresetMode = iota
	PresetTLS
	PresetHTTPS
)

// DNSPreset is a public DNS service offered by name, with the addresses DoT
// and DoH are asked at (so finding them needs no DNS).
type DNSPreset struct {
	ID, Name string
	Plain    []string
	TLSName  string
	DoH      string
}

// DNSPresets are the public DNS services offered by name.
var DNSPresets = []DNSPreset{
	{"cloudflare", "Cloudflare", []string{"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"},
		"one.one.one.one", "https://cloudflare-dns.com/dns-query"},
	{"google", "Google", []string{"8.8.8.8", "8.8.4.4", "2001:4860:4860::8888", "2001:4860:4860::8844"},
		"dns.google", "https://dns.google/dns-query"},
	{"quad9", "Quad9", []string{"9.9.9.9", "149.112.112.112", "2620:fe::fe", "2620:fe::9"},
		"dns.quad9.net", "https://dns.quad9.net/dns-query"},
	{"adguard", "AdGuard", []string{"94.140.14.14", "94.140.15.15", "2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff"},
		"dns.adguard-dns.com", "https://dns.adguard-dns.com/dns-query"},
	{"nextdns", "NextDNS", []string{"45.90.28.0", "45.90.30.0", "2a07:a8c0::", "2a07:a8c1::"},
		"dns.nextdns.io", "https://dns.nextdns.io"},
}

// DNSPresetByID returns the preset named id.
func DNSPresetByID(id string) (DNSPreset, bool) {
	for _, p := range DNSPresets {
		if p.ID == id {
			return p, true
		}
	}
	return DNSPreset{}, false
}

// Offers reports whether the preset can be asked in mode: NextDNS's plain
// servers answer only an IP linked to an account.
func (p DNSPreset) Offers(mode DNSPresetMode) bool {
	return !(p.ID == "nextdns" && mode == PresetPlain)
}

// Choice is the preset in mode. profile is a NextDNS configuration ID (six
// hex digits), which goes into its DoT name and DoH address; empty for its
// default.
func (p DNSPreset) Choice(mode DNSPresetMode, profile string, fallback bool, label string) DNSChoice {
	var ips []netip.Addr
	for _, a := range p.Plain {
		if ip, err := netip.ParseAddr(a); err == nil {
			ips = append(ips, ip)
		}
	}
	at := func(port uint16) []netip.AddrPort {
		out := make([]netip.AddrPort, len(ips))
		for i, ip := range ips {
			out[i] = netip.AddrPortFrom(ip, port)
		}
		return out
	}
	profile = strings.TrimSpace(profile)
	c := DNSChoice{Fallback: fallback, Label: label}
	switch mode {
	case PresetPlain:
		c.Mode, c.Servers = DNSPlain, at(53)
	case PresetTLS:
		c.Mode, c.Servers, c.TLSName = DNSTLS, at(853), p.TLSName
		if p.ID == "nextdns" && profile != "" {
			c.TLSName = profile + "." + p.TLSName
		}
	case PresetHTTPS:
		c.Mode, c.URL, c.Bootstrap = DNSHTTPS, p.DoH, ips
		if p.ID == "nextdns" && profile != "" {
			c.URL = p.DoH + "/" + profile
		}
	}
	return c
}

// ValidNextDNSProfile reports a NextDNS configuration ID: six hex digits,
// or none.
func ValidNextDNSProfile(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return true
	}
	if len(id) != 6 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(id[i])) {
			return false
		}
	}
	return true
}

// ParseDNSServers reads server addresses as typed: IPv4 or IPv6, with an
// optional port ("9.9.9.9:5353", "[2620:fe::fe]:853"), separated by spaces,
// commas or semicolons. defaultPort is used for those without. The error
// names the first one that is not an address.
func ParseDNSServers(text string, defaultPort uint16) ([]netip.AddrPort, error) {
	var out []netip.AddrPort
	for _, s := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(" ,;\n\t", r) }) {
		if ap, err := netip.ParseAddrPort(s); err == nil {
			out = append(out, ap)
			continue
		}
		ip, err := netip.ParseAddr(strings.Trim(s, "[]"))
		if err != nil {
			return nil, fmt.Errorf("not a server address: %s", s)
		}
		out = append(out, netip.AddrPortFrom(ip, defaultPort))
	}
	return out, nil
}

// localName reports names a public DNS cannot know: they stay with the
// system's resolver.
func localName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if !strings.Contains(host, ".") {
		return true
	}
	for _, tld := range []string{"localhost", "local", "lan", "home.arpa", "internal"} {
		if host == tld || strings.HasSuffix(host, "."+tld) {
			return true
		}
	}
	return false
}

// dnsTimeout is how long one server gets to answer.
const dnsTimeout = 4 * time.Second

// ipv4First returns items with the IPv4 ones first, each family in the
// order given: a machine without IPv6 then does not spend a timeout on an
// IPv6 server before the IPv4 ones.
func ipv4First[T any](items []T, ip func(T) netip.Addr) []T {
	var v4, v6 []T
	for _, it := range items {
		if ip(it).Unmap().Is4() {
			v4 = append(v4, it)
		} else {
			v6 = append(v6, it)
		}
	}
	return append(v4, v6...)
}

// systemLookup is the system's resolver (a variable for tests).
var systemLookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Resolver looks names up as a DNSChoice says. A nil one, or one for the
// system's DNS, uses the system's resolver.
type Resolver struct {
	choice   DNSChoice
	setupErr error
	goRes    *net.Resolver
	doh      *http.Client
}

// NewResolver returns a resolver for choice. A choice that cannot work (a
// DoH address that is not https, no servers) gives a resolver whose every
// lookup fails with that DNSSetup error, so downloads say why.
func NewResolver(choice DNSChoice) *Resolver {
	r := &Resolver{choice: choice}
	if choice.IsSystem() {
		return r
	}
	setup := func(why string) *Resolver {
		r.setupErr = &DNSError{Kind: DNSSetup, DNS: choice.Label, Why: why}
		return r
	}
	switch choice.Mode {
	case DNSPlain, DNSTLS:
		if len(choice.Servers) == 0 {
			return setup("no server given")
		}
		r.choice.Servers = ipv4First(choice.Servers, netip.AddrPort.Addr)
	case DNSHTTPS:
		u, err := url.Parse(choice.URL)
		if err != nil {
			return setup(fmt.Sprintf("%s: %v", choice.URL, err))
		}
		if u.Scheme != "https" || u.Hostname() == "" {
			return setup(choice.URL + ": not an https:// address")
		}
		r.doh = r.dohClient(u)
	default:
		return setup("unknown DNS mode")
	}
	r.goRes = &net.Resolver{PreferGo: true, Dial: r.dialDNS}
	return r
}

// LookupIP returns host's addresses.
func (r *Resolver) LookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{ip}, nil
	}
	if r == nil || r.choice.IsSystem() || localName(host) {
		return systemLookup(ctx, host)
	}
	if r.setupErr != nil {
		return nil, r.setupErr
	}
	lctx, cancel := context.WithTimeout(ctx, dnsTimeout*time.Duration(max(1, len(r.choice.Servers))+1))
	defer cancel()
	ips, err := r.goRes.LookupNetIP(lctx, "ip", host)
	var de *net.DNSError
	switch {
	case err == nil && len(ips) > 0:
		return ips, nil
	case err == nil, errors.As(err, &de) && de.IsNotFound:
		return nil, &DNSError{Kind: DNSNoSuchName, DNS: r.choice.Label, Host: host}
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case r.choice.Fallback:
		ips, serr := systemLookup(ctx, host)
		if serr != nil {
			return nil, &DNSError{Kind: DNSUnreachable, DNS: r.choice.Label, Host: host, Why: serr.Error()}
		}
		return ips, nil
	}
	return nil, &DNSError{Kind: DNSUnreachable, DNS: r.choice.Label, Host: host, Why: err.Error()}
}

// dialDNS connects Go's resolver to the chosen DNS, whatever server address
// it asks for (that one comes from the system's configuration). The
// connection is a stream, so queries are length-prefixed, as over TCP.
func (r *Resolver) dialDNS(ctx context.Context, _, _ string) (net.Conn, error) {
	if r.choice.Mode == DNSHTTPS {
		return &dohConn{r: r}, nil
	}
	var last error
	d := &net.Dialer{Timeout: dnsTimeout}
	for _, s := range r.choice.Servers {
		c, err := d.DialContext(ctx, "tcp", s.String())
		if err != nil {
			last = err
			continue
		}
		if r.choice.Mode == DNSPlain {
			return c, nil
		}
		tc := tls.Client(c, &tls.Config{ServerName: r.choice.TLSName, RootCAs: r.choice.RootCAs, MinVersion: tls.VersionTLS12})
		hctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		err = tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			_ = c.Close()
			last = err
			continue
		}
		return tc, nil
	}
	if last == nil {
		last = errors.New("no server")
	}
	return nil, last
}

// dohClient is the HTTPS client DoH queries go through: to the bootstrap
// addresses when given (so finding the DNS needs no DNS), IPv4 first.
func (r *Resolver) dohClient(u *url.URL) *http.Client {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	boot := r.choice.Bootstrap
	if ip, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil {
		boot = []netip.Addr{ip}
	}
	boot = ipv4First(boot, func(a netip.Addr) netip.Addr { return a })
	base := &net.Dialer{Timeout: dnsTimeout}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: r.choice.RootCAs, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: dnsTimeout,
		IdleConnTimeout:     90 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if len(boot) == 0 {
				return base.DialContext(ctx, network, addr)
			}
			var last error
			for _, ip := range boot {
				c, err := base.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
	return &http.Client{Transport: tr, Timeout: dnsTimeout}
}

// dohConn adapts DNS over HTTPS to the stream Go's resolver writes to: each
// length-prefixed query written is POSTed (RFC 8484) and its answer, framed
// the same way, is what Read returns.
type dohConn struct {
	r        *Resolver
	mu       sync.Mutex
	wbuf     bytes.Buffer
	rbuf     bytes.Buffer
	deadline time.Time
}

func (c *dohConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wbuf.Write(p)
	for c.wbuf.Len() >= 2 {
		b := c.wbuf.Bytes()
		n := int(binary.BigEndian.Uint16(b))
		if len(b) < 2+n {
			break
		}
		query := append([]byte(nil), b[2:2+n]...)
		c.wbuf.Next(2 + n)
		answer, err := c.exchange(query)
		if err != nil {
			return 0, err
		}
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(answer)))
		c.rbuf.Write(l[:])
		c.rbuf.Write(answer)
	}
	return len(p), nil
}

func (c *dohConn) exchange(query []byte) ([]byte, error) {
	ctx := context.Background()
	if !c.deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, c.deadline)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.r.choice.URL, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	res, err := c.r.doh.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DNS over HTTPS answered %s", res.Status)
	}
	answer, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if len(answer) < 12 || len(answer) > 0xffff {
		return nil, errors.New("DNS over HTTPS: malformed answer")
	}
	return answer, nil
}

func (c *dohConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rbuf.Len() == 0 {
		return 0, io.EOF
	}
	return c.rbuf.Read(p)
}

func (c *dohConn) Close() error                       { return nil }
func (c *dohConn) LocalAddr() net.Addr                { return dohAddr{} }
func (c *dohConn) RemoteAddr() net.Addr               { return dohAddr{} }
func (c *dohConn) SetReadDeadline(time.Time) error    { return nil }
func (c *dohConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }
func (c *dohConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

type dohAddr struct{}

func (dohAddr) Network() string { return "doh" }
func (dohAddr) String() string  { return "doh" }
