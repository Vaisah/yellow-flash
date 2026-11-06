package network

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://example.com/file.zip":   true,
		"HTTP://example.com:8080/a?b=c":  true,
		"ftp://example.com/file":         false,
		"file:///etc/passwd":             false,
		"https://user:pass@example.com/": false,
		"/relative/path":                 false,
		"https:///nohost":                false,
		"javascript:alert(1)":            false,
		"":                               false,
	} {
		_, err := ValidateURL(raw)
		if (err == nil) != ok {
			t.Errorf("ValidateURL(%q) err=%v want ok=%v", raw, err, ok)
		}
		if err != nil && !errors.Is(err, ErrUnsafeURL) {
			t.Errorf("ValidateURL(%q) error not wrapped: %v", raw, err)
		}
	}
}

func TestUnwrapLink(t *testing.T) {
	img := "https://static0.xdaimages.com/wp/Valve-SteamDeck.jpg"
	for in, want := range map[string]string{
		"https://www.google.com/imgres?q=steamdeck&imgurl=" + url.QueryEscape(img) + "&w=1200": img,
		"https://www.google.co.uk/url?sa=t&url=" + url.QueryEscape(img):                        img,
		"https://l.facebook.com/l.php?u=" + url.QueryEscape(img) + "&h=AT0":                    img,
		"https://www.youtube.com/redirect?event=video&q=" + url.QueryEscape(img):               img,
		"https://www.google.com/imgres?imgurl=" + url.QueryEscape("javascript:alert(1)"):       "https://www.google.com/imgres?imgurl=" + url.QueryEscape("javascript:alert(1)"),
		"https://example.com/imgres?imgurl=" + url.QueryEscape(img):                            "https://example.com/imgres?imgurl=" + url.QueryEscape(img),
		"https://notgoogle.com/url?q=" + url.QueryEscape(img):                                  "https://notgoogle.com/url?q=" + url.QueryEscape(img),
		"https://www.youtube.com/watch?v=abc":                                                  "https://www.youtube.com/watch?v=abc",
	} {
		if got := UnwrapLink(in); got != want {
			t.Errorf("UnwrapLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsPublicAddress(t *testing.T) {
	for addr, public := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fd00::1": false, "::ffff:127.0.0.1": false, "224.0.0.1": false,
		"255.255.255.255": false, "64:ff9b::a00:1": false,
	} {
		if got := IsPublicAddress(netip.MustParseAddr(addr)); got != public {
			t.Errorf("%s public=%v want %v", addr, got, public)
		}
	}
}

func TestPrivateDestinationsBlockedAtDialTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := NewClient(Options{}).Get(srv.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("loopback should be blocked, got %v", err)
	}
	res, err := NewClient(Options{AllowPrivate: true}).Get(srv.URL)
	if err != nil {
		t.Fatalf("AllowPrivate should permit loopback: %v", err)
	}
	res.Body.Close()
}

func TestParseContentRange(t *testing.T) {
	valid := map[string]ContentRange{
		"bytes 0-0/10":                          {0, 0, 10},
		"bytes 5-9/10":                          {5, 9, 10},
		"bytes 0-99/*":                          {0, 99, -1},
		"bytes 0-9223372036854775806/" + maxI64: {0, 9223372036854775806, 9223372036854775807},
	}
	for v, want := range valid {
		got, err := ParseContentRange(v)
		if err != nil || got != want {
			t.Errorf("%q => %+v, %v", v, got, err)
		}
	}
	for _, v := range []string{"", "bytes 9-5/10", "bytes 0-10/10", "bytes -1-5/10", "bytes +1-5/10",
		"items 0-1/2", "bytes 0-1", "bytes 0-99999999999999999999/1", "bytes */10"} {
		if _, err := ParseContentRange(v); !errors.Is(err, ErrInvalidContentRange) {
			t.Errorf("%q should be invalid, got %v", v, err)
		}
	}
	if total, ok := ParseUnsatisfiedRange("bytes */0"); !ok || total != 0 {
		t.Error("unsatisfied range parse failed")
	}
	if RangeHeader(5, -1) != "bytes=5-" || RangeHeader(0, 9) != "bytes=0-9" {
		t.Error("range header format")
	}
}

const maxI64 = "9223372036854775807"

type probeServer struct {
	data        []byte
	noHEAD      bool
	noRange     bool
	badRange    bool
	status      int
	disposition string
}

func (p probeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.status != 0 {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(p.status)
		return
	}
	if r.Header.Get("Accept-Encoding") != "identity" {
		http.Error(w, "identity expected", http.StatusBadRequest)
		return
	}
	w.Header().Set("ETag", `"v1"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	if p.disposition != "" {
		w.Header().Set("Content-Disposition", p.disposition)
	}
	if r.Method == http.MethodHead {
		if p.noHEAD {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(p.data)))
		w.Header().Set("Accept-Ranges", "bytes")
		return
	}
	if r.Header.Get("Range") != "" && !p.noRange {
		if p.badRange {
			w.Header().Set("Content-Range", "bytes 0-0/oops")
		} else {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(p.data)))
			// Body digest of a partial response must not be mistaken for the file's.
			sum := md5.Sum(p.data[:1])
			w.Header().Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(p.data[:1])
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(p.data)))
	_, _ = w.Write(p.data)
}

func probe(t *testing.T, h http.Handler) (string, func(), error) {
	t.Helper()
	srv := httptest.NewServer(h)
	meta, err := NewProber(NewClient(Options{AllowPrivate: true})).Probe(context.Background(), srv.URL+"/files/report.pdf", nil)
	return fmt.Sprintf("size=%d range=%v name=%q etag=%s sum=%d", meta.Size, meta.RangeSupported, meta.Filename, meta.ETag, len(meta.Checksum)), srv.Close, err
}

func TestProbeScenarios(t *testing.T) {
	data := []byte("0123456789abcdef")
	cases := []struct {
		name   string
		server probeServer
		want   string
	}{
		{"range", probeServer{data: data}, `size=16 range=true name="" etag="v1" sum=0`},
		{"head unsupported", probeServer{data: data, noHEAD: true}, `size=16 range=true name="" etag="v1" sum=0`},
		{"accept-ranges lies", probeServer{data: data, noRange: true}, `size=16 range=false name="" etag="v1" sum=0`},
		{"invalid content-range", probeServer{data: data, badRange: true}, `size=16 range=false name="" etag="v1" sum=0`},
		{"content-disposition", probeServer{data: data, disposition: `attachment; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`}, `size=16 range=true name="résumé.pdf" etag="v1" sum=0`},
	}
	for _, tc := range cases {
		got, done, err := probe(t, tc.server)
		done()
		if err != nil || got != tc.want {
			t.Errorf("%s: got %s err=%v, want %s", tc.name, got, err, tc.want)
		}
	}
}

func TestProbeErrorStatuses(t *testing.T) {
	for _, code := range []int{403, 404, 416, 429, 500, 502, 503} {
		_, done, err := probe(t, probeServer{status: code})
		done()
		var se *StatusError
		if !errors.As(err, &se) || se.Code != code || se.RetryAfter != "7" {
			t.Errorf("status %d: got %v", code, err)
		}
	}
}

// Regression: a HEAD reporting Content-Length: 0 followed by a chunked GET
// (as served by e.g. video-site watch pages) must not be treated as empty.
func TestProbeGETLengthOverridesBogusHEAD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "0")
			return
		}
		_, _ = w.Write([]byte("<html>page</html>"))
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()
	meta, err := NewProber(NewClient(Options{AllowPrivate: true})).Probe(context.Background(), srv.URL+"/watch?v=x", nil)
	if err != nil || meta.Size != -1 || meta.RangeSupported || !strings.HasPrefix(meta.MIMEType, "text/html") {
		t.Fatalf("meta=%+v err=%v", meta, err)
	}
}

func TestProbeEmptyFile(t *testing.T) {
	_, done, err := probe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes */0")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", "0")
	}))
	defer done()
	if err != nil {
		t.Fatalf("empty file probe: %v", err)
	}
}

func TestRedirectsFollowedAndValidated(t *testing.T) {
	data := []byte("redirected")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			http.Redirect(w, r, "/b", http.StatusMovedPermanently)
		case "/b":
			http.Redirect(w, r, "/c", http.StatusTemporaryRedirect)
		case "/c":
			http.Redirect(w, r, "/file", http.StatusPermanentRedirect)
		case "/ftp":
			http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			probeServer{data: data}.ServeHTTP(w, r)
		}
	}))
	defer srv.Close()
	p := NewProber(NewClient(Options{AllowPrivate: true, MaxRedirects: 5}))
	meta, err := p.Probe(context.Background(), srv.URL+"/a", nil)
	if err != nil || meta.URL != srv.URL+"/file" || meta.Size != int64(len(data)) {
		t.Fatalf("redirect chain: %+v %v", meta, err)
	}
	if _, err = p.Probe(context.Background(), srv.URL+"/ftp", nil); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("ftp redirect must be rejected: %v", err)
	}
	if _, err = p.Probe(context.Background(), srv.URL+"/loop", nil); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("redirect loop must be bounded: %v", err)
	}
}

func TestChecksumHeaders(t *testing.T) {
	sum := md5.Sum([]byte("x"))
	h := http.Header{}
	h.Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
	if alg, got := ChecksumFromHeaders(h, true); alg != "md5" || len(got) != 16 {
		t.Fatal("content-md5 not parsed")
	}
	if _, got := ChecksumFromHeaders(h, false); got != nil {
		t.Fatal("content-md5 must be ignored on partial responses")
	}
	h = http.Header{}
	h.Set("Repr-Digest", "sha-256=:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=:")
	if alg, got := ChecksumFromHeaders(h, false); alg != "sha-256" || len(got) != 32 {
		t.Fatal("repr-digest not parsed")
	}
	h.Set("Repr-Digest", "sha-256=:not-base64:")
	if _, got := ChecksumFromHeaders(h, true); got != nil {
		t.Fatal("malformed digest must be ignored")
	}
}

// TestConnectionResetClassification uses SO_LINGER=0 so the peer sends a
// genuine TCP RST mid-body on every platform.
func TestConnectionResetClassification(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = http.ReadRequest(bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 1000000\r\n\r\n"+strings.Repeat("x", 1000))
		time.Sleep(50 * time.Millisecond)
		_ = conn.(*net.TCPConn).SetLinger(0)
		_ = conn.Close()
	}()
	res, err := NewClient(Options{AllowPrivate: true}).Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, err = io.Copy(io.Discard, res.Body)
	if err == nil {
		t.Fatal("expected a read error")
	}
	if !IsConnectionReset(err) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reset not recognised: %v (%T)", err, err)
	}
	t.Logf("reset surfaced as: %v (reset=%v)", err, IsConnectionReset(err))
}
