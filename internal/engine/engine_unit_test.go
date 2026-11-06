package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/network"
)

func TestAllocateTilesExactly(t *testing.T) {
	cases := []struct {
		size    int64
		n       int
		min     int64
		want    int
		maxDiff int64
	}{
		{1, 8, 1, 1, 0}, {10, 3, 1, 3, 1}, {100 << 20, 4, 1, 4, 0}, {100, 8, 30, 4, 1},
		{math.MaxInt64, 16, 1, 16, 1}, {7, 7, 1, 7, 0}, {1 << 20, 8, 256 << 10, 4, 0},
	}
	for _, tc := range cases {
		parts, err := Allocate(tc.size, tc.n, tc.min)
		if err != nil || len(parts) != tc.want {
			t.Fatalf("Allocate(%d,%d,%d) = %d parts, %v", tc.size, tc.n, tc.min, len(parts), err)
		}
		var next int64
		lo, hi := parts[0].Length(), parts[0].Length()
		for _, p := range parts {
			if p.Start != next || p.End < p.Start || p.Start < 0 {
				t.Fatalf("gap/overlap at %+v in %v", p, parts)
			}
			next = p.End + 1
			lo, hi = min(lo, p.Length()), max(hi, p.Length())
		}
		if next != tc.size || hi-lo > tc.maxDiff {
			t.Fatalf("size %d covered %d, spread %d", tc.size, next, hi-lo)
		}
	}
	if parts, err := Allocate(0, 4, 1); err != nil || len(parts) != 0 {
		t.Fatal("empty resource")
	}
	for _, bad := range [][2]int64{{-1, 4}, {10, 0}} {
		if _, err := Allocate(bad[0], int(bad[1]), 1); err == nil {
			t.Fatalf("Allocate%v should fail", bad)
		}
	}
}

func TestPlanSegmentsReusesAndClamps(t *testing.T) {
	stored := []domain.Segment{
		{ID: "d-0", DownloadID: "d", Index: 0, Start: 0, End: 49, Downloaded: 50},
		{ID: "d-1", DownloadID: "d", Index: 1, Start: 50, End: 99, Downloaded: 40},
	}
	segs, reused, err := planSegments("d", 100, 8, 1, stored, 100)
	if err != nil || !reused || segs[0].Status != domain.SegmentCompleted || segs[1].Downloaded != 40 {
		t.Fatalf("reuse: %+v %v %v", segs, reused, err)
	}
	// A truncated temp file proves less than the records claim.
	segs, _, _ = planSegments("d", 100, 8, 1, stored, 60)
	if segs[0].Downloaded != 50 || segs[1].Downloaded != 10 {
		t.Fatalf("clamp: %+v", segs)
	}
	segs, _, _ = planSegments("d", 100, 8, 1, stored, 0)
	if segs[0].Downloaded != 0 || segs[1].Downloaded != 0 {
		t.Fatalf("missing file: %+v", segs)
	}
	// A layout for a different size is discarded.
	segs, reused, _ = planSegments("d", 120, 2, 1, stored, 120)
	if reused || len(segs) != 2 || segs[1].End != 119 || segs[0].ID != "d-0" {
		t.Fatalf("realloc: %+v %v", segs, reused)
	}
	gap := []domain.Segment{{Index: 0, Start: 0, End: 40}, {Index: 1, Start: 50, End: 99}}
	if validLayout(gap, 100) {
		t.Fatal("gap accepted")
	}
}

type memWriterAt struct{ buf []byte }

func (m *memWriterAt) WriteAt(p []byte, off int64) (int, error) { return copy(m.buf[off:], p), nil }

func TestRangeWriterCannotEscapeItsRange(t *testing.T) {
	mem := &memWriterAt{buf: bytes.Repeat([]byte{'.'}, 10)}
	w := &rangeWriter{w: mem, off: 2, end: 5}
	n, err := w.Write([]byte("abcdefgh"))
	if n != 4 || !errors.Is(err, errRangeOverflow) || string(mem.buf) != "..abcd...." {
		t.Fatalf("n=%d err=%v buf=%q", n, err, mem.buf)
	}
	if n, err = w.Write([]byte("x")); n != 0 || !errors.Is(err, errRangeOverflow) {
		t.Fatal("write after end accepted")
	}
}

func TestTrackerSpeedAndETA(t *testing.T) {
	t0 := time.Unix(1000, 0)
	tr := newTracker(1000, 100, t0)
	p := tr.snapshot("x", t0)
	if p.Percent != 10 || p.ETASeconds != -1 || p.Speed != 0 {
		t.Fatalf("initial: %+v", p)
	}
	tr.add(100)
	p = tr.snapshot("x", t0.Add(time.Second))
	if p.Speed != 100 || p.AverageSpeed != 100 || p.ETASeconds != 8 {
		t.Fatalf("after 1s: %+v", p)
	}
	tr.add(300)
	p = tr.snapshot("x", t0.Add(2*time.Second))
	if want := 0.3*300 + 0.7*100; math.Abs(p.Speed-want) > 1e-9 || p.AverageSpeed != 200 {
		t.Fatalf("ewma: %+v want %v", p, want)
	}
	unknown := newTracker(-1, 0, t0)
	unknown.add(50)
	if p = unknown.snapshot("x", t0.Add(time.Second)); p.Percent != -1 || p.ETASeconds != -1 || p.Total != -1 {
		t.Fatalf("unknown size must not report ETA: %+v", p)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	cases := []struct {
		err       error
		kind      ErrorKind
		retryable bool
	}{
		{context.Canceled, KindCanceled, false},
		{errIdleTimeout, KindTimeout, true},
		{timeoutErr{}, KindTimeout, true},
		{fmt.Errorf("wrapped: %w", reset), KindConnectionReset, true},
		{io.ErrUnexpectedEOF, KindNetwork, true},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, KindNetwork, true},
		{&net.DNSError{Err: "no such host", IsNotFound: true}, KindNetwork, false},
		{&network.StatusError{Code: 404}, KindHTTP, false},
		{&network.StatusError{Code: 416}, KindHTTP, false},
		{&network.StatusError{Code: 403}, KindHTTP, false},
		{&network.StatusError{Code: 500}, KindHTTP, true},
		{&network.StatusError{Code: 502}, KindHTTP, true},
		{&network.StatusError{Code: 503}, KindHTTP, true},
		{&network.StatusError{Code: 429}, KindHTTP, true},
		{fmt.Errorf("x: %w", network.ErrBlockedAddress), KindPermanent, false},
		{domain.ErrRemoteChanged, KindPermanent, false},
		{ErrIntegrity, KindPermanent, false},
		{errors.New("disk full"), KindPermanent, false},
	}
	for _, tc := range cases {
		c := Classify(tc.err)
		if c.Kind != tc.kind || c.Retryable != tc.retryable {
			t.Errorf("Classify(%v) = %+v, want %s retryable=%v", tc.err, c, tc.kind, tc.retryable)
		}
	}
	c := Classify(&network.StatusError{Code: 429, RetryAfter: "3"})
	if c.RetryAfter != 3*time.Second {
		t.Fatalf("Retry-After not parsed: %+v", c)
	}
	if c = Classify(&network.StatusError{Code: 500, RetryAfter: "3"}); c.RetryAfter != 0 {
		t.Fatal("Retry-After only applies to 429/503")
	}
}

func TestRetryPolicy(t *testing.T) {
	p := RetryPolicy{MaxRetries: 3, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second, Multiplier: 2, Jitter: 0.5}.normalized()
	if d := p.Delay(0, Classification{}, 0.5); d != 100*time.Millisecond {
		t.Fatalf("base delay %v", d)
	}
	if d := p.Delay(10, Classification{}, 0.5); d != time.Second {
		t.Fatalf("capped delay %v", d)
	}
	if lo, hi := p.Delay(1, Classification{}, 0), p.Delay(1, Classification{}, 0.999); lo != 100*time.Millisecond || hi < 299*time.Millisecond {
		t.Fatalf("jitter range %v..%v", lo, hi)
	}
	if d := p.Delay(0, Classification{RetryAfter: 5 * time.Second}, 0.5); d != 5*time.Second {
		t.Fatalf("Retry-After must be a lower bound, got %v", d)
	}
	retryable := Classification{Retryable: true}
	if !p.ShouldRetry(2, retryable) || p.ShouldRetry(3, retryable) || p.ShouldRetry(0, Classification{}) {
		t.Fatal("retry budget")
	}
	if p.ShouldRetry(0, Classification{Retryable: true, RetryAfter: time.Hour}) {
		t.Fatal("excessive Retry-After must not be retried early")
	}
	if (RetryPolicy{MaxRetries: 0}).normalized().ShouldRetry(0, retryable) {
		t.Fatal("MaxRetries 0 must disable retries")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if d, ok := ParseRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now); !ok || d != 90*time.Second {
		t.Fatalf("http-date Retry-After: %v %v", d, ok)
	}
	if _, ok := ParseRetryAfter("soon", now); ok {
		t.Fatal("garbage Retry-After accepted")
	}
}

func TestRepresentationChangedAndIfRange(t *testing.T) {
	d := &domain.Download{Size: 10, ETag: `"a"`}
	if representationChanged(d, domain.RemoteMetadata{Size: 10, ETag: `"a"`}) {
		t.Fatal("same representation flagged")
	}
	for _, m := range []domain.RemoteMetadata{{Size: 11, ETag: `"a"`}, {Size: 10, ETag: `"b"`}, {Size: 10}} {
		if !representationChanged(d, m) {
			t.Fatalf("change not detected: %+v", m)
		}
	}
	lm := &domain.Download{Size: -1, LastModified: "Mon"}
	if !representationChanged(lm, domain.RemoteMetadata{Size: 5, LastModified: "Tue"}) {
		t.Fatal("Last-Modified change not detected")
	}
	if ifRangeValidator(`W/"weak"`, "Mon") != "Mon" || ifRangeValidator(`"strong"`, "Mon") != `"strong"` {
		t.Fatal("If-Range must use strong ETags only")
	}
	if durableOffset(500, 300, 1000) != 300 || durableOffset(500, 900, 1000) != 500 || durableOffset(500, 900, 400) != 0 {
		t.Fatal("durable offset")
	}
}

func TestSplitHeaders(t *testing.T) {
	pub, sec, err := splitHeaders(map[string]string{"referer": "r", "authorization": "Bearer x", "Cookie": "c"})
	if err != nil || pub["Referer"] != "r" || sec["Authorization"] != "Bearer x" || len(sec) != 2 || len(pub) != 1 {
		t.Fatalf("split: %v %v %v", pub, sec, err)
	}
	for _, bad := range []map[string]string{{"Range": "bytes=0-"}, {"X-Evil": "a\r\nInjected: 1"}, {"Bad Name": "v"}, {"Host": "x"}} {
		if _, _, err := splitHeaders(bad); !errors.Is(err, domain.ErrInvalidRequest) {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestVerifyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	data := []byte("integrity")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	ok := domain.RemoteMetadata{Size: int64(len(data)), ChecksumAlgorithm: "sha-256", Checksum: sum[:]}
	if n, err := verifyFile(context.Background(), path, ok); err != nil || n != int64(len(data)) {
		t.Fatalf("verify: %d %v", n, err)
	}
	for _, bad := range []domain.RemoteMetadata{{Size: 3}, {Size: -1, ChecksumAlgorithm: "sha-256", Checksum: make([]byte, 32)}} {
		if _, err := verifyFile(context.Background(), path, bad); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("bad file accepted: %+v %v", bad, err)
		}
	}
}
