// Ported from Mister Download Manager, crates/remote/src/tls.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package remote

// The remote control's certificate and its HTTPS listener. A self-signed
// certificate is made once (remote/cert.der, its key remote/key.der,
// readable by the user only) and kept: its fingerprint, shown in Options,
// is how a device can tell it talks to this Yellow Flash the first time its
// browser warns about it, and the same certificate every time is what lets
// the browser remember that choice. It is made again only when it runs out
// (800 days, under the limit Apple's systems accept) or is deleted.

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vaisah/internal/ipc"
)

const (
	// validDays is how long the certificate lasts; renewDays how long
	// before its end a new one is made.
	validDays = 800
	renewDays = 30
	// maxConnections are the connections open at once on one listener;
	// more wait to be accepted.
	maxConnections = 64
	// handshake is the time a client gets for the TLS handshake (or a plain
	// HTTP request's head).
	handshake = 10 * time.Second
	// idle: a connection that sends and receives nothing this long is
	// closed, so connections held open doing nothing can't take every place
	// from the devices using the page.
	idle = 60 * time.Second
)

// Identity is the certificate in use.
type Identity struct {
	Config *tls.Config
	// Fingerprint is the certificate's SHA-256, as AB:CD:… (what browsers
	// show).
	Fingerprint string
}

type made struct {
	// Until is the last day it is valid, in days since 1970-01-01.
	Until int64 `json:"until"`
}

func today() int64 { return time.Now().Unix() / 86400 }

// identity is the certificate kept in dir, or a new one for names (the
// computer's name, localhost and its addresses) when there is none or it
// runs out soon.
func identity(dir string, names []string) (Identity, error) {
	if err := ipc.PrivateDir(dir); err != nil {
		return Identity{}, err
	}
	certPath, keyPath, madePath := filepath.Join(dir, "cert.der"), filepath.Join(dir, "key.der"), filepath.Join(dir, "cert.json")
	cert, key, ok := kept(certPath, keyPath, madePath)
	if !ok {
		var err error
		if cert, key, err = makeCert(names); err != nil {
			return Identity{}, err
		}
		m, _ := json.Marshal(made{Until: today() + validDays})
		for _, w := range []struct {
			path string
			b    []byte
		}{{keyPath, key}, {certPath, cert}, {madePath, m}} {
			if err := ipc.PrivateWrite(w.path, w.b); err != nil {
				return Identity{}, err
			}
		}
	}
	pk, err := x509.ParsePKCS8PrivateKey(key)
	if err != nil {
		return Identity{}, err
	}
	sum := sha256.Sum256(cert)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return Identity{
		Config: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{{Certificate: [][]byte{cert}, PrivateKey: pk}},
		},
		Fingerprint: strings.Join(parts, ":"),
	}, nil
}

func kept(certPath, keyPath, madePath string) (cert, key []byte, ok bool) {
	b, err := os.ReadFile(madePath)
	if err != nil {
		return nil, nil, false
	}
	var m made
	if json.Unmarshal(b, &m) != nil || m.Until-today() <= renewDays {
		return nil, nil, false
	}
	if cert, err = os.ReadFile(certPath); err != nil {
		return nil, nil, false
	}
	if key, err = os.ReadFile(keyPath); err != nil {
		return nil, nil, false
	}
	return cert, key, true
}

// makeCert is a new self-signed certificate for names: (certificate, key),
// both DER (the key PKCS #8).
func makeCert(names []string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Yellow Flash"},
		NotBefore:    time.Unix((today()-1)*86400, 0).UTC(),
		NotAfter:     time.Unix((today()+validDays)*86400, 0).UTC(),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip, err := netip.ParseAddr(n); err == nil {
			tpl.IPAddresses = append(tpl.IPAddresses, net.IP(ip.AsSlice()))
		} else if n != "" {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	cert, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return cert, der, nil
}

// listener is the HTTPS listener the server reads from: connections are
// accepted and their handshakes done on goroutines of their own (a slow
// client holds up no other), at most maxConnections at once. A plain
// http:// request is answered with the https:// address (redirect).
type listener struct {
	tcp    net.Listener
	conns  chan net.Conn
	done   chan struct{}
	once   sync.Once
	slots  chan struct{}
	conf   *tls.Config
	hostOK func(string) bool
}

func newListener(tcp net.Listener, conf *tls.Config, hostOK func(string) bool) *listener {
	l := &listener{tcp: tcp, conns: make(chan net.Conn), done: make(chan struct{}),
		slots: make(chan struct{}, maxConnections), conf: conf, hostOK: hostOK}
	go l.run()
	return l
}

func (l *listener) run() {
	for {
		select {
		case l.slots <- struct{}{}:
		case <-l.done:
			return
		}
		c, err := l.tcp.Accept()
		if err != nil {
			<-l.slots
			select {
			case <-l.done:
				return
			default:
			}
			// Out of file handles and the like: try again in a moment.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go l.handshake(c)
	}
}

func (l *listener) release() { <-l.slots }

func (l *listener) handshake(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(handshake))
	r := bufio.NewReader(c)
	first, err := r.Peek(1)
	if err != nil {
		c.Close()
		l.release()
		return
	}
	pc := &peeked{Conn: c, r: r}
	// TLS starts with a handshake record (22); anything else is plain HTTP.
	if first[0] != 22 {
		l.redirect(pc)
		c.Close()
		l.release()
		return
	}
	tc := tls.Server(pc, l.conf)
	if err := tc.Handshake(); err != nil {
		tc.Close()
		l.release()
		return
	}
	_ = c.SetDeadline(time.Time{})
	conn := &slotConn{Conn: tc, release: l.release}
	select {
	case l.conns <- conn:
	case <-l.done:
		conn.Close()
	}
}

// redirect answers a plain request with a redirect to the same address
// over HTTPS (to a name this server answers to only).
func (l *listener) redirect(c *peeked) {
	var head []byte
	buf := make([]byte, 1024)
	for len(head) < 8192 && !bytes.Contains(head, []byte("\r\n\r\n")) {
		n, err := c.Read(buf)
		head = append(head, buf[:n]...)
		if err != nil {
			break
		}
	}
	answer := "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	for _, line := range strings.Split(string(head), "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "host") {
			continue
		}
		if host := HostOf(value); l.hostOK(host) {
			port := l.tcp.Addr().(*net.TCPAddr).Port
			answer = fmt.Sprintf("HTTP/1.1 301 Moved Permanently\r\nLocation: https://%s:%d/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", host, port)
		}
		break
	}
	_, _ = c.Write([]byte(answer))
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.tcp.Close()
}

func (l *listener) Addr() net.Addr { return l.tcp.Addr() }

// peeked is a connection whose first bytes were looked at.
type peeked struct {
	net.Conn
	r *bufio.Reader
}

func (p *peeked) Read(b []byte) (int, error) { return p.r.Read(b) }

// slotConn gives its place back when closed.
type slotConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *slotConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// addrInUse reports "the port is taken", on every system.
func addrInUse(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "address already in use") || strings.Contains(s, "only one usage of each socket address")
}
