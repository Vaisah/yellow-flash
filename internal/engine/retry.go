package engine

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/network"
)

// errIdleTimeout is used when a response body stops delivering data.
var errIdleTimeout = errors.New("read timed out: no data received")

// ErrorKind classifies a failure for retry decisions and reporting.
type ErrorKind string

// Error kinds.
const (
	KindNone            ErrorKind = ""
	KindCanceled        ErrorKind = "canceled"
	KindTimeout         ErrorKind = "timeout"
	KindConnectionReset ErrorKind = "connection_reset"
	KindNetwork         ErrorKind = "network"
	KindHTTP            ErrorKind = "http"
	KindPermanent       ErrorKind = "permanent"
)

// Classification is the outcome of Classify.
type Classification struct {
	Kind       ErrorKind
	Retryable  bool
	StatusCode int
	// RetryAfter is the server-requested delay (429 and 503 only).
	RetryAfter time.Duration
}

// retryableStatus lists transient HTTP statuses.
var retryableStatus = map[int]bool{
	http.StatusRequestTimeout:      true,
	http.StatusTooEarly:            true,
	http.StatusTooManyRequests:     true,
	http.StatusInternalServerError: true,
	http.StatusBadGateway:          true,
	http.StatusServiceUnavailable:  true,
	http.StatusGatewayTimeout:      true,
}

// Classify distinguishes cancellation, timeouts, connection resets, other
// transient network failures, HTTP statuses and permanent errors.
func Classify(err error) Classification {
	if err == nil {
		return Classification{}
	}
	if errors.Is(err, errIdleTimeout) {
		return Classification{Kind: KindTimeout, Retryable: true}
	}
	if errors.Is(err, context.Canceled) {
		return Classification{Kind: KindCanceled}
	}
	var se *network.StatusError
	if errors.As(err, &se) {
		c := Classification{Kind: KindHTTP, StatusCode: se.Code, Retryable: retryableStatus[se.Code]}
		if se.Code == http.StatusTooManyRequests || se.Code == http.StatusServiceUnavailable {
			c.RetryAfter, _ = ParseRetryAfter(se.RetryAfter, time.Now())
		}
		return c
	}
	switch {
	case errors.Is(err, network.ErrBlockedAddress), errors.Is(err, network.ErrUnsafeURL),
		errors.Is(err, network.ErrTooManyRedirects), errors.Is(err, network.ErrInvalidContentRange),
		errors.Is(err, domain.ErrRemoteChanged), errors.Is(err, errRangeIgnored),
		errors.Is(err, errContentRangeMismatch), errors.Is(err, errRangeOverflow), errors.Is(err, ErrIntegrity):
		return Classification{Kind: KindPermanent}
	case network.IsConnectionReset(err):
		return Classification{Kind: KindConnectionReset, Retryable: true}
	case errors.Is(err, context.DeadlineExceeded):
		return Classification{Kind: KindTimeout, Retryable: true}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return Classification{Kind: KindTimeout, Retryable: true}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return Classification{Kind: KindNetwork, Retryable: dnsErr.IsTimeout || dnsErr.IsTemporary}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return Classification{Kind: KindNetwork, Retryable: true}
	}
	return Classification{Kind: KindPermanent}
}

// ParseRetryAfter parses delta-seconds or an HTTP date.
func ParseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs < 0 || secs > int64(24*time.Hour/time.Second) {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if when, err := http.ParseTime(v); err == nil {
		return max(0, when.Sub(now)), true
	}
	return 0, false
}

// RetryPolicy controls retries with exponential backoff and jitter.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt; 0 disables retries.
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Multiplier float64
	// Jitter randomises each delay by ±Jitter (0..1) to avoid thundering herds.
	Jitter float64
	// MaxRetryAfter is the longest server-requested delay honoured; longer
	// requests fail the operation instead of hammering the server early.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy returns the engine defaults.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxRetries: 5, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second, Multiplier: 2, Jitter: 0.2, MaxRetryAfter: 2 * time.Minute}
}

func (p RetryPolicy) normalized() RetryPolicy {
	d := DefaultRetryPolicy()
	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = d.BaseDelay
	}
	if p.MaxDelay < p.BaseDelay {
		p.MaxDelay = max(p.BaseDelay, d.MaxDelay)
	}
	if p.Multiplier < 1 {
		p.Multiplier = d.Multiplier
	}
	if p.Jitter < 0 || p.Jitter > 1 {
		p.Jitter = d.Jitter
	}
	if p.MaxRetryAfter <= 0 {
		p.MaxRetryAfter = d.MaxRetryAfter
	}
	return p
}

// ShouldRetry reports whether attempt (0-based count of failures so far)
// may be retried.
func (p RetryPolicy) ShouldRetry(attempt int, c Classification) bool {
	return c.Retryable && attempt < p.MaxRetries && c.RetryAfter <= p.MaxRetryAfter
}

// Delay returns the wait before the next attempt. random must be in [0,1).
// A server-provided Retry-After is a lower bound.
func (p RetryPolicy) Delay(attempt int, c Classification, random float64) time.Duration {
	d := float64(p.BaseDelay) * math.Pow(p.Multiplier, float64(attempt))
	if d > float64(p.MaxDelay) || math.IsInf(d, 0) {
		d = float64(p.MaxDelay)
	}
	d *= 1 - p.Jitter + 2*p.Jitter*random
	return max(time.Duration(d), c.RetryAfter)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
