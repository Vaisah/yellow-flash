package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// payload returns deterministic pseudo-random bytes for a seed.
func payload(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fileServer serves one resource with switchable misbehaviour.
type fileServer struct {
	mu   sync.Mutex
	data []byte
	etag string

	noHEAD          bool // HEAD answers 405
	noRange         bool // Range ignored, always 200
	probeRangeOnly  bool // only bytes=0-0 honoured; other ranges get 200
	badContentRange bool // non-probe 206 responses carry a wrong Content-Range
	unknownLength   bool // chunked, no Content-Length, no ranges
	reprDigest      string
	disposition     string
	requireAuth     string
	chunk           int
	delay           time.Duration

	// fail may answer a request with an error status (0 = serve normally).
	fail func(r *http.Request, n int64) (status int, retryAfter string)
	// resetAt returns a body offset at which to send a TCP RST (-1 = never).
	resetAt func(r *http.Request, n int64) int64
	// gate, if set, is called before each chunk with the request's sequence
	// number and may block.
	gate func(r *http.Request, n int64)

	requests      atomic.Int64
	rangeRequests atomic.Int64
	bytesServed   atomic.Int64
	srv           *httptest.Server
}

func newFileServer(t *testing.T, data []byte, configure func(*fileServer)) *fileServer {
	t.Helper()
	fs := &fileServer{data: data, etag: `"` + digest(data)[:16] + `"`, chunk: 32 << 10}
	if configure != nil {
		configure(fs)
	}
	fs.srv = httptest.NewServer(fs)
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fileServer) url(path string) string { return fs.srv.URL + path }

// swap replaces the resource, as if it were republished.
func (fs *fileServer) swap(data []byte) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.data = data
	fs.etag = `"` + digest(data)[:16] + `"`
}

// redirectCodes are cycled through by /hop/N chains.
var redirectCodes = []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect}

func (fs *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if hop, ok := strings.CutPrefix(r.URL.Path, "/hop/"); ok {
		n, _ := strconv.Atoi(hop)
		target := fmt.Sprintf("/hop/%d", n-1)
		if n <= 1 {
			target = "/files/hopped.bin"
		}
		http.Redirect(w, r, target, redirectCodes[n%len(redirectCodes)])
		return
	}
	if r.URL.Path == "/to-ftp" {
		http.Redirect(w, r, "ftp://example.com/file.bin", http.StatusFound)
		return
	}
	n := fs.requests.Add(1)
	fs.mu.Lock()
	data, etag := fs.data, fs.etag
	fs.mu.Unlock()
	if fs.requireAuth != "" && r.Header.Get("Authorization") != fs.requireAuth {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if fs.fail != nil {
		if status, retryAfter := fs.fail(r, n); status != 0 {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			if status == http.StatusRequestedRangeNotSatisfiable {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
			}
			w.WriteHeader(status)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	if !fs.unknownLength {
		h.Set("ETag", etag)
	}
	if fs.reprDigest != "" {
		h.Set("Repr-Digest", fs.reprDigest)
	}
	if fs.disposition != "" {
		h.Set("Content-Disposition", fs.disposition)
	}
	if r.Method == http.MethodHead && fs.noHEAD {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	start, end, partial := 0, len(data)-1, false
	if rh := r.Header.Get("Range"); rh != "" {
		fs.rangeRequests.Add(1)
		a, b, ok := parseRange(rh, len(data))
		probe := a == 0 && b == 0
		ifRange := r.Header.Get("If-Range")
		honour := ok && !fs.noRange && !fs.unknownLength && (!fs.probeRangeOnly || probe) && (ifRange == "" || ifRange == etag)
		if honour {
			if a >= len(data) {
				h.Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, end, partial = a, min(b, len(data)-1), true
		}
	}
	body := data[start : end+1]
	if !fs.unknownLength {
		h.Set("Content-Length", strconv.Itoa(len(body)))
	}
	if partial {
		cr := fmt.Sprintf("bytes %d-%d/%d", start, end, len(data))
		if fs.badContentRange && !(start == 0 && end == 0) {
			cr = fmt.Sprintf("bytes %d-%d/%d", start+1, end, len(data))
		}
		h.Set("Content-Range", cr)
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}
	resetAt := int64(-1)
	if fs.resetAt != nil {
		resetAt = fs.resetAt(r, n)
	}
	flusher, _ := w.(http.Flusher)
	for off := 0; off < len(body); {
		if fs.gate != nil {
			fs.gate(r, n)
		}
		if r.Context().Err() != nil {
			return
		}
		next := min(off+fs.chunk, len(body))
		if resetAt >= 0 && int64(next) > resetAt {
			next = max(off, int(resetAt))
			if next > off {
				written, _ := w.Write(body[off:next])
				fs.bytesServed.Add(int64(written))
			}
			resetConnection(w)
			return
		}
		written, err := w.Write(body[off:next])
		fs.bytesServed.Add(int64(written))
		if err != nil {
			return
		}
		off = next
		if flusher != nil {
			flusher.Flush()
		}
		if fs.delay > 0 {
			select {
			case <-time.After(fs.delay):
			case <-r.Context().Done():
				return
			}
		}
	}
}

// resetConnection aborts the connection with a TCP RST (SO_LINGER=0).
func resetConnection(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func parseRange(v string, size int) (int, int, bool) {
	spec, ok := strings.CutPrefix(v, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	a, b, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	end := size - 1
	if b != "" {
		if end, err = strconv.Atoi(b); err != nil || end < start {
			return 0, 0, false
		}
	}
	return start, end, true
}

func reprDigest(b []byte) string {
	s := sha256.Sum256(b)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(s[:]) + ":"
}

// rangeStart extracts the first offset of a Range header (or -1).
func rangeStart(r *http.Request) int {
	a, _, ok := parseRange(r.Header.Get("Range"), 1<<62)
	if !ok {
		return -1
	}
	return a
}

// freezeGate stalls every response that began at or before a cut-off
// request number, simulating a process that stopped mid-transfer.
type freezeGate struct{ cutoff atomic.Int64 }

func (g *freezeGate) freezeUpTo(n int64) { g.cutoff.Store(n) }

func (g *freezeGate) wait(ctx context.Context, n int64) {
	if c := g.cutoff.Load(); c > 0 && n <= c {
		<-ctx.Done()
	}
}
