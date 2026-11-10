// Ported from Mister Download Manager, crates/engine/src/ftp.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FTP and FTPS: ftp://, ftpes:// (explicit TLS: AUTH TLS on port 21) and
// ftps:// (implicit TLS, port 990). They are served to the engine as HTTP:
// an http.RoundTripper answers GET and HEAD, a Range with REST + RETR and
// 206 Partial Content, SIZE as the length and MDTM as Last-Modified (so a
// resumed download checks the file did not change). The engine's
// segmentation, resume and retries then work for FTP unchanged; a server's
// "421 too many connections" becomes 503, which the engine's adaptive
// concurrency already handles.
//
// Each connection is a control session with its own data connection,
// passive (EPSV when FEAT lists it, else PASV, connecting to the control
// connection's address: a NAT'd server often answers PASV with its private
// one). A part that ends before the file does drops its data connection;
// its session goes back to the pool only when a NOOP shows the control
// channel is in step again.
//
// The login comes from the address's user and an Authorization: Basic
// header (the engine moves a password out of the address into that
// header, so it is never saved), else anonymous. The path is relative to
// the login folder (RFC 1738; %2F makes it absolute). FTP does not go
// through HTTP proxies.

const (
	ftpIdle     = 30 * time.Second // an idle pooled session older than this is closed
	ftpTimeout  = 30 * time.Second // each command's answer
	ftpPoolSize = 16               // idle sessions kept per site
	ftpAnonPass = "yellowflash@"
)

// IsFTP reports an FTP address.
func IsFTP(u *url.URL) bool {
	switch strings.ToLower(u.Scheme) {
	case "ftp", "ftps", "ftpes":
		return true
	}
	return false
}

// FTPLoginHeader moves a password out of an FTP address: it returns the
// address with the user name only and an Authorization: Basic value for
// the login ("" when the address had no password).
func FTPLoginHeader(u *url.URL) (*url.URL, string) {
	if u.User == nil {
		return u, ""
	}
	pass, ok := u.User.Password()
	if !ok {
		return u, ""
	}
	user := u.User.Username()
	c := *u
	c.User = url.User(user)
	if user == "" {
		c.User = nil
	}
	return &c, "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

type ftpSecurity int

const (
	ftpPlain ftpSecurity = iota
	ftpExplicit
	ftpImplicit
)

type ftpSite struct {
	security   ftpSecurity
	host, port string
	user, pass string
	path       string
}

func (s ftpSite) key() string {
	return fmt.Sprint(s.security, "|", s.host, "|", s.port, "|", s.user, "|", s.pass)
}

var errFTPUnsafe = errors.New("ftp: line breaks are not allowed in a path or login")

func ftpSiteOf(u *url.URL, h http.Header) (ftpSite, error) {
	s := ftpSite{host: u.Hostname(), port: u.Port()}
	switch strings.ToLower(u.Scheme) {
	case "ftps":
		s.security = ftpImplicit
	case "ftpes":
		s.security = ftpExplicit
	}
	if s.port == "" {
		s.port = "21"
		if s.security == ftpImplicit {
			s.port = "990"
		}
	}
	if u.User != nil {
		s.user = u.User.Username()
		s.pass, _ = u.User.Password()
	}
	if s.pass == "" {
		if user, pass, ok := basicLogin(h.Get("Authorization")); ok && (s.user == "" || s.user == user) {
			s.user, s.pass = user, pass
		}
	}
	switch s.user {
	case "", "anonymous", "ftp":
		if s.pass == "" {
			s.user, s.pass = "anonymous", ftpAnonPass
		}
	}
	// The leading slash only separates the path from the server; ";type=i"
	// is a transfer type (always binary here).
	p := strings.TrimPrefix(u.Path, "/")
	if i := strings.LastIndex(p, ";type="); i >= 0 {
		p = p[:i]
	}
	s.path = p
	for _, v := range []string{s.path, s.user, s.pass} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return ftpSite{}, errFTPUnsafe
		}
	}
	if s.path == "" {
		return ftpSite{}, fmt.Errorf("%w: no file in the address", ErrUnsafeURL)
	}
	return s, nil
}

func basicLogin(v string) (user, pass string, ok bool) {
	token, found := strings.CutPrefix(v, "Basic ")
	if !found {
		token, found = strings.CutPrefix(v, "basic ")
	}
	if !found {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(raw), ":")
}

/* ---------- one control session ---------- */

type ftpConn struct {
	conn    net.Conn // the control connection (TLS once secured)
	tp      *textproto.Conn
	site    ftpSite
	tlsCfg  *tls.Config
	prot    bool // data connections are TLS too
	epsv    bool
	rest    bool
	active  bool   // data connections come from the server (PORT/EPRT)
	remote  string // the control connection's peer IP, for PASV
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	stopped bool
}

// cmd sends a command and reads its answer, checking the code (expect 0:
// any; 2: any 2xx; 150: exactly 150).
func (c *ftpConn) cmd(expect int, format string, args ...any) (int, string, error) {
	_ = c.conn.SetDeadline(time.Now().Add(ftpTimeout))
	if _, err := c.tp.Cmd(format, args...); err != nil {
		return 0, "", err
	}
	return c.tp.ReadResponse(expect)
}

func (c *ftpConn) close() {
	if !c.stopped {
		c.stopped = true
		_ = c.conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = c.tp.Cmd("QUIT")
		_ = c.conn.Close()
	}
}

func (t *ftpTransport) connect(ctx context.Context, s ftpSite) (*ftpConn, error) {
	raw, err := t.dial(ctx, "tcp", net.JoinHostPort(s.host, s.port))
	if err != nil {
		return nil, err
	}
	c := &ftpConn{conn: raw, site: s, dial: t.dial, active: t.active}
	if host, _, err := net.SplitHostPort(raw.RemoteAddr().String()); err == nil {
		c.remote = host
	}
	ok := false
	defer func() {
		if !ok {
			_ = raw.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	secure := func(conn net.Conn) error {
		c.tlsCfg = t.tlsConfig(s.host)
		tc := tls.Client(conn, c.tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("ftp TLS: %w", err)
		}
		c.conn = tc
		c.tp = textproto.NewConn(tc)
		return nil
	}
	if s.security == ftpImplicit {
		if err := secure(raw); err != nil {
			return nil, err
		}
	} else {
		c.tp = textproto.NewConn(raw)
	}
	_ = c.conn.SetDeadline(time.Now().Add(ftpTimeout))
	if _, _, err := c.tp.ReadResponse(220); err != nil {
		return nil, err
	}
	if s.security == ftpExplicit {
		if _, _, err := c.cmd(234, "AUTH TLS"); err != nil {
			return nil, err
		}
		if err := secure(raw); err != nil {
			return nil, err
		}
	}
	if s.security != ftpPlain {
		if _, _, err := c.cmd(200, "PBSZ 0"); err != nil {
			return nil, err
		}
		if _, _, err := c.cmd(200, "PROT P"); err != nil {
			return nil, err
		}
		c.prot = true
	}
	code, _, err := c.cmd(0, "USER %s", s.user)
	if err == nil && code == 331 {
		code, _, err = c.cmd(0, "PASS %s", s.pass)
	}
	if err != nil {
		return nil, err
	}
	if code != 230 && code != 202 {
		return nil, &textproto.Error{Code: 530, Msg: "login refused"}
	}
	if _, _, err := c.cmd(200, "TYPE I"); err != nil {
		return nil, err
	}
	// Not every server has FEAT; without it, nothing is assumed.
	if _, msg, err := c.cmd(211, "FEAT"); err == nil {
		for _, line := range strings.Split(msg, "\n") {
			f := strings.ToUpper(strings.TrimSpace(line))
			c.epsv = c.epsv || f == "EPSV"
			c.rest = c.rest || f == "REST STREAM"
		}
	}
	ok = true
	return c, nil
}

func (c *ftpConn) size() (int64, error) {
	_, msg, err := c.cmd(213, "SIZE %s", c.site.path)
	if err != nil {
		return -1, err
	}
	return strconv.ParseInt(strings.TrimSpace(msg), 10, 64)
}

func (c *ftpConn) modified() (time.Time, bool) {
	_, msg, err := c.cmd(213, "MDTM %s", c.site.path)
	msg = strings.TrimSpace(msg)
	if err != nil || len(msg) < 14 {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102150405", msg[:14])
	return t.UTC(), err == nil
}

// openData asks for a passive data connection and connects to it.
func (c *ftpConn) openData(ctx context.Context) (net.Conn, error) {
	port := ""
	if c.epsv {
		_, msg, err := c.cmd(229, "EPSV")
		if err == nil {
			// (|||6446|)
			if i, j := strings.IndexByte(msg, '('), strings.LastIndexByte(msg, ')'); i >= 0 && j > i {
				if f := strings.Split(msg[i+1:j], "|"); len(f) >= 4 {
					port = f[3]
				}
			}
		} else {
			c.epsv = false
		}
	}
	if port == "" {
		_, msg, err := c.cmd(227, "PASV")
		if err != nil {
			return nil, err
		}
		i, j := strings.IndexByte(msg, '('), strings.LastIndexByte(msg, ')')
		if i < 0 || j < i {
			return nil, fmt.Errorf("ftp: unreadable PASV answer %q", msg)
		}
		f := strings.Split(msg[i+1:j], ",")
		if len(f) != 6 {
			return nil, fmt.Errorf("ftp: unreadable PASV answer %q", msg)
		}
		p1, e1 := strconv.Atoi(strings.TrimSpace(f[4]))
		p2, e2 := strconv.Atoi(strings.TrimSpace(f[5]))
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("ftp: unreadable PASV answer %q", msg)
		}
		port = strconv.Itoa(p1*256 + p2)
	}
	// The control connection's own address, never the one in the answer.
	return c.dial(ctx, "tcp", net.JoinHostPort(c.remote, port))
}

// listenActive opens a listener for an active data connection on the
// control connection's own local address and tells the server where it is
// (PORT for IPv4, EPRT for IPv6).
func (c *ftpConn) listenActive() (*net.TCPListener, error) {
	host, _, err := net.SplitHostPort(c.conn.LocalAddr().String())
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ip := net.ParseIP(host)
	if v4 := ip.To4(); v4 != nil {
		_, _, err = c.cmd(200, "PORT %d,%d,%d,%d,%d,%d", v4[0], v4[1], v4[2], v4[3], port/256, port%256)
	} else {
		_, _, err = c.cmd(200, "EPRT |2|%s|%d|", host, port)
	}
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln.(*net.TCPListener), nil
}

// acceptActive takes the server's data connection, only from the control
// connection's peer: no other host can inject a data stream.
func (c *ftpConn) acceptActive(ctx context.Context, ln *net.TCPListener) (net.Conn, error) {
	defer ln.Close()
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	_ = ln.SetDeadline(time.Now().Add(ftpTimeout))
	for {
		dc, err := ln.Accept()
		if err != nil {
			return nil, fmt.Errorf("ftp: the server did not connect back (active mode): %w", err)
		}
		if host, _, _ := net.SplitHostPort(dc.RemoteAddr().String()); host == c.remote {
			return dc, nil
		}
		_ = dc.Close()
	}
}

// retr starts sending the file from offset on a new data connection.
func (c *ftpConn) retr(ctx context.Context, offset int64) (net.Conn, error) {
	if c.active {
		return c.retrActive(ctx, offset)
	}
	dc, err := c.openData(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		_ = dc.Close()
		return nil, err
	}
	if offset > 0 {
		if _, _, err := c.cmd(350, "REST %d", offset); err != nil {
			return fail(err)
		}
	}
	if _, _, err := c.cmd(1, "RETR %s", c.site.path); err != nil {
		return fail(err)
	}
	if c.prot {
		tc := tls.Client(dc, c.tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return fail(fmt.Errorf("ftp data TLS: %w", err))
		}
		dc = tc
	}
	return dc, nil
}

// retrActive is retr with an active data connection: the server connects
// to us after RETR.
func (c *ftpConn) retrActive(ctx context.Context, offset int64) (net.Conn, error) {
	ln, err := c.listenActive()
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if _, _, err := c.cmd(350, "REST %d", offset); err != nil {
			_ = ln.Close()
			return nil, err
		}
	}
	if _, _, err := c.cmd(1, "RETR %s", c.site.path); err != nil {
		_ = ln.Close()
		return nil, err
	}
	dc, err := c.acceptActive(ctx, ln)
	if err != nil {
		return nil, err
	}
	if c.prot {
		tcl := tls.Client(dc, c.tlsCfg)
		if err := tcl.HandshakeContext(ctx); err != nil {
			_ = dc.Close()
			return nil, fmt.Errorf("ftp data TLS: %w", err)
		}
		dc = tcl
	}
	return dc, nil
}

// finish reads the answer to a transfer whose data connection is closed:
// 226 after the whole file; 426 or similar when it was dropped early. It
// reports whether the session can be used again.
func (c *ftpConn) finish(whole bool) bool {
	_ = c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	code, _, err := c.tp.ReadResponse(0)
	var te *textproto.Error
	if err != nil && !errors.As(err, &te) {
		return false
	}
	if whole && code/100 == 2 {
		return true
	}
	// Dropped early: some servers answer twice (426, then 226). A NOOP that
	// reads back its own 200 shows the channel is in step.
	_, _, err = c.cmd(200, "NOOP")
	return err == nil
}

/* ---------- the RoundTripper ---------- */

type idleFTP struct {
	c  *ftpConn
	at time.Time
}

// ftpTransport serves ftp://, ftps:// and ftpes:// requests as HTTP.
type ftpTransport struct {
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	roots   *x509.CertPool
	tickets tls.ClientSessionCache // data connections resume the control's TLS session
	active  bool                   // active data connections (PORT/EPRT)

	mu   sync.Mutex
	idle map[string][]idleFTP
}

func newFTPTransport(dial func(ctx context.Context, network, addr string) (net.Conn, error), roots *x509.CertPool) *ftpTransport {
	return &ftpTransport{dial: dial, roots: roots, tickets: tls.NewLRUClientSessionCache(64), idle: map[string][]idleFTP{}}
}

func (t *ftpTransport) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, RootCAs: t.roots, MinVersion: tls.VersionTLS12, ClientSessionCache: t.tickets}
}

func (t *ftpTransport) take(key string) *ftpConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	list := t.idle[key]
	for len(list) > 0 {
		last := list[len(list)-1]
		list = list[:len(list)-1]
		if time.Since(last.at) < ftpIdle {
			t.idle[key] = list
			return last.c
		}
		last.c.close()
	}
	delete(t.idle, key)
	return nil
}

func (t *ftpTransport) put(c *ftpConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := c.site.key()
	if len(t.idle[key]) >= ftpPoolSize {
		c.close()
		return
	}
	t.idle[key] = append(t.idle[key], idleFTP{c: c, at: time.Now()})
}

// session returns a logged-in session: a pooled one that still answers, or
// a new one.
func (t *ftpTransport) session(ctx context.Context, s ftpSite) (*ftpConn, error) {
	for {
		c := t.take(s.key())
		if c == nil {
			return t.connect(ctx, s)
		}
		if _, _, err := c.cmd(200, "NOOP"); err == nil {
			c.site = s // pooled per server and login: this request's file
			return c, nil
		}
		c.close() // the server closed it meanwhile
	}
}

func (t *ftpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return ftpStatus(req, http.StatusMethodNotAllowed, "FTP serves GET and HEAD only"), nil
	}
	site, err := ftpSiteOf(req.URL, req.Header)
	if err != nil {
		return nil, err
	}
	ctx := req.Context()
	c, err := t.session(ctx, site)
	if err != nil {
		return ftpFailure(req, err)
	}
	size, err := c.size()
	if err != nil {
		var te *textproto.Error
		if !errors.As(err, &te) || te.Code == 550 {
			c.close()
			return ftpFailure(req, err)
		}
		size = -1 // SIZE not offered
	}
	h := http.Header{"Content-Type": {"application/octet-stream"}}
	lastMod := ""
	if m, ok := c.modified(); ok {
		lastMod = m.Format(http.TimeFormat)
		h.Set("Last-Modified", lastMod)
	}
	ranges := c.rest && size >= 0
	if ranges {
		h.Set("Accept-Ranges", "bytes")
	}
	if req.Method == http.MethodHead {
		t.put(c)
		return ftpResponse(req, http.StatusOK, h, size, http.NoBody), nil
	}
	start, end, partial := int64(0), size-1, false
	if r := req.Header.Get("Range"); r != "" && ranges {
		if ifRange := req.Header.Get("If-Range"); ifRange == "" || ifRange == lastMod {
			a, b, ok := parseByteRange(r, size)
			switch {
			case !ok:
			case a >= size:
				t.put(c)
				h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				return ftpResponse(req, http.StatusRequestedRangeNotSatisfiable, h, 0, http.NoBody), nil
			default:
				start, end, partial = a, b, true
			}
		}
	}
	dc, err := c.retr(ctx, start)
	if err != nil {
		c.close()
		return ftpFailure(req, err)
	}
	length := size
	if partial {
		length = end - start + 1
	}
	body := &ftpBody{t: t, c: c, data: dc, remaining: length, toEOF: !partial || end == size-1}
	body.stop = context.AfterFunc(ctx, func() { _ = dc.Close(); _ = c.conn.Close() })
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		return ftpResponse(req, http.StatusPartialContent, h, length, body), nil
	}
	return ftpResponse(req, http.StatusOK, h, length, body), nil
}

// parseByteRange reads "bytes=a-b" or "bytes=a-" against size.
func parseByteRange(v string, size int64) (int64, int64, bool) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(v), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	as, bs, ok := strings.Cut(spec, "-")
	a, err := strconv.ParseInt(strings.TrimSpace(as), 10, 64)
	if !ok || err != nil || a < 0 {
		return 0, 0, false
	}
	b := size - 1
	if bs = strings.TrimSpace(bs); bs != "" {
		if b, err = strconv.ParseInt(bs, 10, 64); err != nil || b < a {
			return 0, 0, false
		}
		b = min(b, size-1)
	}
	return a, b, true
}

// ftpBody is a transfer's data, at most remaining bytes (-1: to the end).
type ftpBody struct {
	t         *ftpTransport
	c         *ftpConn
	data      net.Conn
	remaining int64
	toEOF     bool // the range runs to the end of the file
	eof       bool
	stop      func() bool
	once      sync.Once
}

func (b *ftpBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if b.remaining > 0 && int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.data.Read(p)
	if b.remaining > 0 {
		b.remaining -= int64(n)
	}
	if errors.Is(err, io.EOF) {
		b.eof = true
		if b.remaining > 0 {
			return n, io.ErrUnexpectedEOF
		}
	}
	if b.remaining == 0 && err == nil {
		return n, io.EOF
	}
	return n, err
}

func (b *ftpBody) Close() error {
	b.once.Do(func() {
		cancelled := !b.stop()
		_ = b.data.Close()
		whole := b.toEOF && (b.eof || b.remaining == 0)
		if !cancelled && b.c.finish(whole) {
			b.t.put(b.c)
		} else {
			b.c.close()
		}
	})
	return nil
}

func ftpResponse(req *http.Request, code int, h http.Header, length int64, body io.ReadCloser) *http.Response {
	if length >= 0 {
		h.Set("Content-Length", strconv.FormatInt(length, 10))
	}
	return &http.Response{
		Status: fmt.Sprintf("%d %s", code, http.StatusText(code)), StatusCode: code,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: h, Body: body, ContentLength: length, Request: req,
	}
}

func ftpStatus(req *http.Request, code int, msg string) *http.Response {
	h := http.Header{"Content-Type": {"text/plain; charset=utf-8"}}
	return ftpResponse(req, code, h, int64(len(msg)), io.NopCloser(bytes.NewReader([]byte(msg))))
}

// ftpFailure turns a server's refusal into the HTTP status the engine
// already handles (404 not found, 403 login, 503 busy: retried, and fewer
// connections); network errors stay errors.
func ftpFailure(req *http.Request, err error) (*http.Response, error) {
	var te *textproto.Error
	if !errors.As(err, &te) {
		return nil, err
	}
	msg := fmt.Sprintf("FTP %d %s", te.Code, te.Msg)
	switch {
	case te.Code == 550 || te.Code == 551 || te.Code == 553:
		return ftpStatus(req, http.StatusNotFound, msg), nil
	case te.Code == 530 || te.Code == 532:
		return ftpStatus(req, http.StatusForbidden, msg), nil
	case te.Code/100 == 4:
		return ftpStatus(req, http.StatusServiceUnavailable, msg), nil
	}
	return ftpStatus(req, http.StatusNotImplemented, msg), nil
}
