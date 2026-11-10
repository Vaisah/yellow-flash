// Ported from Mister Download Manager, crates/engine/src/ftp.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/infrastructure/network/ftptest"
)

func TestFTPAddressesNameTheServerFileAndLogin(t *testing.T) {
	site := func(raw string, h http.Header) ftpSite {
		t.Helper()
		u, _ := url.Parse(raw)
		s, err := ftpSiteOf(u, h)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return s
	}
	s := site("ftp://ftp.example.com/pub/a%20b.iso;type=i", nil)
	if s.host != "ftp.example.com" || s.port != "21" || s.path != "pub/a b.iso" || s.user != "anonymous" || s.pass != ftpAnonPass {
		t.Fatalf("%+v", s)
	}
	s = site("ftps://me:p%40ss@[::1]/%2Fabs/x", nil)
	if s.host != "::1" || s.port != "990" || s.security != ftpImplicit || s.user != "me" || s.pass != "p@ss" || s.path != "/abs/x" {
		t.Fatalf("%+v", s)
	}
	s = site("ftpes://host:2121/x", nil)
	if s.port != "2121" || s.security != ftpExplicit {
		t.Fatalf("%+v", s)
	}
	u, _ := url.Parse("ftp://h/a%0D%0ADELE%20x")
	if _, err := ftpSiteOf(u, nil); !errors.Is(err, errFTPUnsafe) {
		t.Fatalf("command injection: %v", err)
	}
}

func TestABasicHeaderLogsInWhenTheAddressHasNoPassword(t *testing.T) {
	h := http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("bob:secret"))}}
	u, _ := url.Parse("ftp://bob@host/x")
	if s, _ := ftpSiteOf(u, h); s.user != "bob" || s.pass != "secret" {
		t.Fatalf("%+v", s)
	}
	// The address's own password wins.
	u, _ = url.Parse("ftp://ann:pw@host/x")
	if s, _ := ftpSiteOf(u, h); s.user != "ann" || s.pass != "pw" {
		t.Fatalf("%+v", s)
	}
	// And Create moves it out of the address.
	stripped, login := FTPLoginHeader(u)
	if stripped.String() != "ftp://ann@host/x" || login != "Basic "+base64.StdEncoding.EncodeToString([]byte("ann:pw")) {
		t.Fatalf("%s %s", stripped, login)
	}
}

func payloadBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func get(t *testing.T, c *http.Client, raw string, h http.Header) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, raw, nil)
	for k, v := range h {
		req.Header[k] = v
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

func TestFTPServedAsHTTP(t *testing.T) {
	data := payloadBytes(300 << 10)
	srv := ftptest.New(t, map[string][]byte{"pub/file.bin": data}, nil)
	c := NewClient(Options{AllowPrivate: true})
	base := "ftp://" + srv.Addr + "/pub/file.bin"

	res, body := get(t, c, base, nil)
	if res.StatusCode != 200 || !bytes.Equal(body, data) || res.ContentLength != int64(len(data)) {
		t.Fatalf("whole: %d %d bytes", res.StatusCode, len(body))
	}
	if res.Header.Get("Last-Modified") != "Thu, 01 Oct 2026 12:30:45 GMT" || res.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("headers %v", res.Header)
	}
	res, body = get(t, c, base, http.Header{"Range": {"bytes=1000-1999"}})
	if res.StatusCode != 206 || !bytes.Equal(body, data[1000:2000]) ||
		res.Header.Get("Content-Range") != "bytes 1000-1999/307200" {
		t.Fatalf("range: %d %q %d bytes", res.StatusCode, res.Header.Get("Content-Range"), len(body))
	}
	res, body = get(t, c, base, http.Header{"Range": {"bytes=307000-"}})
	if res.StatusCode != 206 || !bytes.Equal(body, data[307000:]) {
		t.Fatalf("open range: %d %d bytes", res.StatusCode, len(body))
	}
	// A stale If-Range: the whole file, not a part of a changed one.
	res, _ = get(t, c, base, http.Header{"Range": {"bytes=10-20"}, "If-Range": {"Mon, 01 Jan 2024 00:00:00 GMT"}})
	if res.StatusCode != 200 {
		t.Fatalf("stale If-Range: %d", res.StatusCode)
	}
	res, _ = get(t, c, "ftp://"+srv.Addr+"/pub/missing.bin", nil)
	if res.StatusCode != 404 {
		t.Fatalf("missing: %d", res.StatusCode)
	}
	head, err := c.Head(base)
	if err != nil || head.StatusCode != 200 || head.ContentLength != int64(len(data)) {
		t.Fatalf("HEAD: %v %v", head, err)
	}
	// Sessions were reused across these requests, including after a part
	// that ended before the file.
	if n := srv.Logins.Load(); n > 2 {
		t.Fatalf("%d logins for 6 requests", n)
	}
	// PASV's private address (10.9.8.7) was not used: EPSV off, still works.
	plain := ftptest.New(t, map[string][]byte{"f": data[:5000]}, func(s *ftptest.Server) { s.NoEPSV = true })
	if res, body = get(t, c, "ftp://"+plain.Addr+"/f", nil); res.StatusCode != 200 || len(body) != 5000 {
		t.Fatalf("PASV: %d %d", res.StatusCode, len(body))
	}
}

func TestFTPWithoutRESTAndBusyAndLogins(t *testing.T) {
	data := payloadBytes(64 << 10)
	norest := ftptest.New(t, map[string][]byte{"f": data}, func(s *ftptest.Server) { s.NoRest = true })
	c := NewClient(Options{AllowPrivate: true})
	res, body := get(t, c, "ftp://"+norest.Addr+"/f", http.Header{"Range": {"bytes=100-"}})
	if res.StatusCode != 200 || !bytes.Equal(body, data) || res.Header.Get("Accept-Ranges") != "" {
		t.Fatalf("no REST: a range is answered with the whole file: %d", res.StatusCode)
	}

	busy := ftptest.New(t, map[string][]byte{"f": data}, func(s *ftptest.Server) {
		s.MaxConns, s.Delay = 1, 20*time.Millisecond
	})
	slow, err := c.Get("ftp://" + busy.Addr + "/f") // holds the only connection
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Body.Close()
	c2 := NewClient(Options{AllowPrivate: true})
	if res, _ = get(t, c2, "ftp://"+busy.Addr+"/f", nil); res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("421 → %d", res.StatusCode)
	}

	locked := ftptest.New(t, map[string][]byte{"f": data}, func(s *ftptest.Server) { s.User, s.Pass = "bob", "secret" })
	if res, _ = get(t, c, "ftp://"+locked.Addr+"/f", nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous at a locked server: %d", res.StatusCode)
	}
	login := http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("bob:secret"))}}
	if res, body = get(t, c, "ftp://bob@"+locked.Addr+"/f", login); res.StatusCode != 200 || len(body) != len(data) {
		t.Fatalf("login: %d", res.StatusCode)
	}
}

func TestFTPSExplicitAndImplicit(t *testing.T) {
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS() // only for its certificate (127.0.0.1)
	defer ts.Close()
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	cfg := &tls.Config{Certificates: ts.TLS.Certificates}
	data := payloadBytes(100 << 10)
	c := NewClient(Options{AllowPrivate: true, RootCAs: roots})
	for _, tc := range []struct {
		scheme   string
		implicit bool
	}{{"ftpes", false}, {"ftps", true}} {
		srv := ftptest.New(t, map[string][]byte{"f": data}, func(s *ftptest.Server) { s.TLS, s.Implicit = cfg, tc.implicit })
		res, body := get(t, c, tc.scheme+"://"+srv.Addr+"/f", http.Header{"Range": {"bytes=5-"}})
		if res.StatusCode != 206 || !bytes.Equal(body, data[5:]) {
			t.Fatalf("%s: %d %d bytes", tc.scheme, res.StatusCode, len(body))
		}
	}
	// An untrusted certificate is refused.
	srv := ftptest.New(t, map[string][]byte{"f": data}, func(s *ftptest.Server) { s.TLS = cfg })
	if _, err := NewClient(Options{AllowPrivate: true}).Get("ftpes://" + srv.Addr + "/f"); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("untrusted: %v", err)
	}
}

func TestFTPActiveMode(t *testing.T) {
	data := payloadBytes(200 << 10)
	srv := ftptest.New(t, map[string][]byte{"f": data}, nil)
	c := NewClient(Options{AllowPrivate: true, FTPActive: true})
	res, body := get(t, c, "ftp://"+srv.Addr+"/f", http.Header{"Range": {"bytes=100-"}})
	if res.StatusCode != 206 || !bytes.Equal(body, data[100:]) {
		t.Fatalf("active: %d %d bytes", res.StatusCode, len(body))
	}
	if res, body = get(t, c, "ftp://"+srv.Addr+"/f", nil); res.StatusCode != 200 || !bytes.Equal(body, data) {
		t.Fatalf("active, again on the pooled session: %d", res.StatusCode)
	}
}

func TestFTPKeepsTheDialPolicy(t *testing.T) {
	srv := ftptest.New(t, map[string][]byte{"f": []byte("x")}, nil)
	_, err := NewClient(Options{}).Get("ftp://" + srv.Addr + "/f")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("loopback FTP from an untrusted client: %v", err)
	}
}
