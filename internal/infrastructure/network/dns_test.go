// Ported from Mister Download Manager, crates/engine/src/dns.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestAListeningAddressWinsOverADeadOne(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	// 192.0.2.1 is TEST-NET-1: never routed (silently dropped or unreachable).
	dead, live := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("127.0.0.1")
	d := &hostDialer{base: &net.Dialer{Timeout: 5 * time.Second}}
	start := time.Now()
	c, i, err := d.race(context.Background(), "tcp", []netip.Addr{dead, live}, port)
	if err != nil || i != 1 {
		t.Fatalf("winner %d: %v", i, err)
	}
	_ = c.Close()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("took %v", took)
	}
	// Every address dead: the errors come back.
	if _, _, err = d.race(context.Background(), "tcp", []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("127.0.0.2")}, "1"); err == nil {
		t.Fatal("dialled a closed port")
	}
}

func TestLocalNamesStayWithTheSystem(t *testing.T) {
	for _, n := range []string{"localhost", "nas", "printer.local", "a.b.lan", "x.home.arpa", "svc.internal", "app.localhost"} {
		if !localName(n) {
			t.Errorf("%s is local", n)
		}
	}
	for _, n := range []string{"github.com", "example.org", "local.example.com", "internal.net"} {
		if localName(n) {
			t.Errorf("%s is not local", n)
		}
	}
}

func TestServersParseWithAndWithoutPorts(t *testing.T) {
	got, err := ParseDNSServers("9.9.9.9, 1.1.1.1:5353; [2620:fe::fe]:853 2620:fe::9", 53)
	want := []string{"9.9.9.9:53", "1.1.1.1:5353", "[2620:fe::fe]:853", "[2620:fe::9]:53"}
	if err != nil || len(got) != len(want) {
		t.Fatalf("%v %v", got, err)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("%d: %s, want %s", i, got[i], want[i])
		}
	}
	if _, err = ParseDNSServers("9.9.9.9 dns.google", 53); err == nil || !strings.Contains(err.Error(), "dns.google") {
		t.Fatalf("names the bad one: %v", err)
	}
	if got, err = ParseDNSServers("", 53); err != nil || len(got) != 0 {
		t.Fatal("empty")
	}
}

func TestPresetsMakeTheirChoices(t *testing.T) {
	n, _ := DNSPresetByID("nextdns")
	if n.Offers(PresetPlain) || !n.Offers(PresetHTTPS) {
		t.Fatal("NextDNS is not offered plain")
	}
	c := n.Choice(PresetHTTPS, "abc123", false, "NextDNS")
	if c.Mode != DNSHTTPS || c.URL != "https://dns.nextdns.io/abc123" || len(c.Bootstrap) != 4 {
		t.Fatalf("%+v", c)
	}
	c = n.Choice(PresetTLS, "abc123", false, "")
	if c.TLSName != "abc123.dns.nextdns.io" {
		t.Fatalf("%+v", c)
	}
	for _, s := range c.Servers {
		if s.Port() != 853 {
			t.Fatalf("port %d", s.Port())
		}
	}
	if !ValidNextDNSProfile("") || !ValidNextDNSProfile("a1b2c3") || ValidNextDNSProfile("xyz") || ValidNextDNSProfile("a1b2c3d") {
		t.Fatal("profiles")
	}
}

/* ---------- fake DNS servers ---------- */

// answer builds the reply to query: A 127.0.0.1 for files.example., no
// such name for gone.example., an empty answer otherwise.
func answer(t *testing.T, query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		t.Errorf("query: %v", err)
		return nil
	}
	q, err := p.Question()
	if err != nil {
		t.Errorf("question: %v", err)
		return nil
	}
	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired, RecursionAvailable: true}
	if strings.EqualFold(q.Name.String(), "gone.example.") {
		rh.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	if strings.EqualFold(q.Name.String(), "files.example.") && q.Type == dnsmessage.TypeA {
		_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60},
			dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}})
	}
	msg, err := b.Finish()
	if err != nil {
		t.Errorf("finish: %v", err)
	}
	return msg
}

// serveStream answers length-prefixed queries on ln (plain DNS over TCP, or
// DNS over TLS when ln is a TLS listener).
func serveStream(t *testing.T, ln net.Listener, asked *atomic.Int64) {
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					var l [2]byte
					if _, err := io.ReadFull(c, l[:]); err != nil {
						return
					}
					q := make([]byte, binary.BigEndian.Uint16(l[:]))
					if _, err := io.ReadFull(c, q); err != nil {
						return
					}
					asked.Add(1)
					a := answer(t, q)
					binary.BigEndian.PutUint16(l[:], uint16(len(a)))
					_, _ = c.Write(append(l[:], a...))
				}
			}()
		}
	}()
}

func plainDNS(t *testing.T) (netip.AddrPort, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	asked := &atomic.Int64{}
	serveStream(t, ln, asked)
	return netip.MustParseAddrPort(ln.Addr().String()), asked
}

func noSystem(t *testing.T) *atomic.Int64 {
	t.Helper()
	asked := &atomic.Int64{}
	old := systemLookup
	systemLookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		asked.Add(1)
		if host == "localhost" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("198.51.100.9")}, nil
	}
	t.Cleanup(func() { systemLookup = old })
	return asked
}

func TestPlainDNSAnswersAndNoSuchNameNeverFallsBack(t *testing.T) {
	server, asked := plainDNS(t)
	system := noSystem(t)
	r := NewResolver(DNSChoice{Mode: DNSPlain, Servers: []netip.AddrPort{server}, Fallback: true, Label: "Test DNS"})
	ips, err := r.LookupIP(context.Background(), "files.example")
	if err != nil || len(ips) != 1 || ips[0].String() != "127.0.0.1" {
		t.Fatalf("%v %v", ips, err)
	}
	_, err = r.LookupIP(context.Background(), "gone.example")
	var de *DNSError
	if !errors.As(err, &de) || de.Kind != DNSNoSuchName || !strings.Contains(err.Error(), "Test DNS says gone.example doesn't exist") {
		t.Fatalf("no such name: %v", err)
	}
	if system.Load() != 0 || asked.Load() == 0 {
		t.Fatalf("system asked %d times, DNS %d", system.Load(), asked.Load())
	}
	// Local names never go to it.
	if _, err = r.LookupIP(context.Background(), "localhost"); err != nil || system.Load() != 1 {
		t.Fatalf("localhost: %v (system %d)", err, system.Load())
	}
}

func TestAnUnreachableDNSFailsWithoutAskingTheSystemUnlessAllowed(t *testing.T) {
	system := noSystem(t)
	dead := []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")} // nothing listens
	strict := NewResolver(DNSChoice{Mode: DNSPlain, Servers: dead, Label: "Dead DNS"})
	_, err := strict.LookupIP(context.Background(), "github.com")
	var de *DNSError
	if !errors.As(err, &de) || de.Kind != DNSUnreachable || de.DNS != "Dead DNS" || de.Host != "github.com" || system.Load() != 0 {
		t.Fatalf("strict: %v (system %d)", err, system.Load())
	}
	lenient := NewResolver(DNSChoice{Mode: DNSPlain, Servers: dead, Fallback: true, Label: "Dead DNS"})
	ips, err := lenient.LookupIP(context.Background(), "github.com")
	if err != nil || len(ips) != 1 || system.Load() != 1 {
		t.Fatalf("fallback: %v %v", ips, err)
	}
	bad := NewResolver(DNSChoice{Mode: DNSHTTPS, URL: "http://dns.example/q", Label: "Bad"})
	if _, err = bad.LookupIP(context.Background(), "github.com"); !errors.As(err, &de) || de.Kind != DNSSetup {
		t.Fatalf("setup: %v", err)
	}
}

func TestDNSOverTLSAndHTTPS(t *testing.T) {
	noSystem(t)
	var dohAsked atomic.Int64
	doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/dns-message" || r.URL.Path != "/dns-query" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		q, _ := io.ReadAll(r.Body)
		dohAsked.Add(1)
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(answer(t, q))
	}))
	defer doh.Close()
	roots := x509.NewCertPool()
	roots.AddCert(doh.Certificate())

	// DNS over TLS with the same certificate (valid for example.com and 127.0.0.1).
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dotLn := tls.NewListener(tcp, &tls.Config{Certificates: doh.TLS.Certificates})
	defer dotLn.Close()
	var dotAsked atomic.Int64
	serveStream(t, dotLn, &dotAsked)

	for _, c := range []struct {
		name   string
		choice DNSChoice
		asked  *atomic.Int64
	}{
		{"DoT", DNSChoice{Mode: DNSTLS, Servers: []netip.AddrPort{netip.MustParseAddrPort(tcp.Addr().String())},
			TLSName: "example.com", RootCAs: roots, Label: "DoT"}, &dotAsked},
		{"DoH", DNSChoice{Mode: DNSHTTPS, URL: doh.URL + "/dns-query", RootCAs: roots, Label: "DoH"}, &dohAsked},
	} {
		r := NewResolver(c.choice)
		ips, err := r.LookupIP(context.Background(), "files.example")
		if err != nil || len(ips) != 1 || ips[0].String() != "127.0.0.1" || c.asked.Load() == 0 {
			t.Fatalf("%s: %v %v (asked %d)", c.name, ips, err, c.asked.Load())
		}
		var de *DNSError
		if _, err = r.LookupIP(context.Background(), "gone.example"); !errors.As(err, &de) || de.Kind != DNSNoSuchName {
			t.Fatalf("%s gone: %v", c.name, err)
		}
	}
	// A DoT server whose certificate does not show the name is not trusted.
	wrong := NewResolver(DNSChoice{Mode: DNSTLS, Servers: []netip.AddrPort{netip.MustParseAddrPort(tcp.Addr().String())},
		TLSName: "dns.other.test", RootCAs: roots, Label: "DoT"})
	var de *DNSError
	if _, err = wrong.LookupIP(context.Background(), "files.example"); !errors.As(err, &de) || de.Kind != DNSUnreachable {
		t.Fatalf("wrong certificate name: %v", err)
	}
}

func TestADownloadResolvesItsHostThroughTheChosenDNS(t *testing.T) {
	server, asked := plainDNS(t)
	noSystem(t)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "from "+r.Host)
	}))
	defer files.Close()
	_, port, _ := net.SplitHostPort(files.Listener.Addr().String())
	c := NewClient(Options{AllowPrivate: true, DNS: &DNSChoice{Mode: DNSPlain, Servers: []netip.AddrPort{server}, Label: "Test DNS"}})
	res, err := c.Get("http://files.example:" + port + "/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.HasPrefix(string(body), "from files.example") || asked.Load() == 0 {
		t.Fatalf("%q (asked %d)", body, asked.Load())
	}
	// The SSRF policy still checks the resolved address.
	strict := NewClient(Options{DNS: &DNSChoice{Mode: DNSPlain, Servers: []netip.AddrPort{server}, Label: "Test DNS"}})
	if _, err = strict.Get("http://files.example:" + port + "/x"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("loopback through the chosen DNS: %v", err)
	}
}
