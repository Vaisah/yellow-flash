package network

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/network/sites"
)

// UserAgent is sent when the caller supplies none.
const UserAgent = "Downwell/0.1"

// Doer executes HTTP requests. *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Prober discovers remote metadata and verifies byte-range support.
type Prober struct{ client Doer }

// NewProber returns a Prober using client.
func NewProber(client Doer) *Prober { return &Prober{client: client} }

// Probe performs a HEAD request, then a one-byte range GET. Range support is
// claimed only when the GET answers 206 with a valid "bytes 0-0/N" header and
// exactly one body byte; Accept-Ranges is never trusted on its own. A HEAD
// that fails or returns a non-2xx status is ignored, so servers without HEAD
// support still work through the GET.
func (p *Prober) Probe(ctx context.Context, rawURL string, headers http.Header) (domain.RemoteMetadata, error) {
	meta := domain.RemoteMetadata{URL: rawURL, Size: domain.UnknownSize}
	if _, err := ValidateURL(rawURL); err != nil {
		return meta, err
	}
	rawURL, err := siteURL(rawURL)
	if err != nil {
		return meta, err
	}
	meta.URL = rawURL
	headOK := false
	if res, err := p.Request(ctx, http.MethodHead, rawURL, headers, ""); err == nil {
		_ = res.Body.Close()
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			headOK = true
			meta.URL = res.Request.URL.String()
			applyHeaders(&meta, res.Header, true)
			if n, ok := parseLength(res.Header.Get("Content-Length")); ok {
				meta.Size = n
			}
		}
	} else if ctx.Err() != nil {
		return meta, ctx.Err()
	}

	res, err := p.siteGet(ctx, meta.URL, headers, RangeHeader(0, 0))
	if err != nil {
		return meta, err
	}
	defer res.Body.Close()
	meta.URL = res.Request.URL.String()
	switch res.StatusCode {
	case http.StatusPartialContent:
		applyHeaders(&meta, res.Header, false)
		cr, err := ParseContentRange(res.Header.Get("Content-Range"))
		if err == nil && cr.Start == 0 && cr.End == 0 {
			sample, readErr := io.ReadAll(io.LimitReader(res.Body, 2))
			if readErr == nil && len(sample) == 1 {
				meta.RangeSupported = true
				if cr.Total >= 0 {
					meta.Size = cr.Total
				}
			}
		}
	case http.StatusOK:
		applyHeaders(&meta, res.Header, true)
		// The GET describes what will actually be transferred, so its length
		// wins, including "unknown" (-1). Some servers answer HEAD with a
		// bogus Content-Length: 0 and then stream a chunked body.
		meta.Size = res.ContentLength
	case http.StatusRequestedRangeNotSatisfiable:
		// An empty resource cannot satisfy bytes=0-0.
		if total, ok := ParseUnsatisfiedRange(res.Header.Get("Content-Range")); ok && total == 0 {
			applyHeaders(&meta, res.Header, true)
			meta.Size = 0
			return meta, nil
		}
		if headOK && meta.Size == 0 {
			return meta, nil
		}
		return meta, NewStatusError(res)
	default:
		return meta, NewStatusError(res)
	}
	return meta, nil
}

// Open sends a download's first request and returns its metadata together
// with the open response, whose body is the file from byte 0. It asks for
// "bytes=0-": a 206 answer proves range support and gives the total size in
// the same round trip, so no separate probe requests are needed and data
// starts flowing at once (small files finish on this one request). A 200
// means the server ignores ranges. A malformed range answer falls back to a
// plain GET. resp is nil only for an empty file. The caller must close resp.
func (p *Prober) Open(ctx context.Context, rawURL string, headers http.Header) (domain.RemoteMetadata, *http.Response, error) {
	meta := domain.RemoteMetadata{URL: rawURL, Size: domain.UnknownSize}
	if _, err := ValidateURL(rawURL); err != nil {
		return meta, nil, err
	}
	res, err := p.siteGet(ctx, rawURL, headers, RangeHeader(0, -1))
	if err != nil {
		return meta, nil, err
	}
	meta.URL = res.Request.URL.String()
	switch res.StatusCode {
	case http.StatusPartialContent:
		if cr, err := ParseContentRange(res.Header.Get("Content-Range")); err == nil && cr.Start == 0 {
			applyHeaders(&meta, res.Header, cr.Total >= 0 && cr.End == cr.Total-1)
			meta.RangeSupported, meta.Size = true, cr.Total
			return meta, res, nil
		}
		_ = res.Body.Close()
	case http.StatusOK:
		applyHeaders(&meta, res.Header, true)
		meta.Size = res.ContentLength
		return meta, res, nil
	case http.StatusRequestedRangeNotSatisfiable:
		if total, ok := ParseUnsatisfiedRange(res.Header.Get("Content-Range")); ok && total == 0 {
			applyHeaders(&meta, res.Header, true)
			meta.Size = 0
			_ = res.Body.Close()
			return meta, nil, nil
		}
		_ = res.Body.Close()
	default:
		_ = res.Body.Close()
		return meta, nil, NewStatusError(res)
	}
	// The server mishandled the range request: download without ranges.
	res, err = p.Request(ctx, http.MethodGet, meta.URL, headers, "")
	if err != nil {
		return meta, nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return meta, nil, NewStatusError(res)
	}
	applyHeaders(&meta, res.Header, true)
	meta.RangeSupported, meta.Size = false, res.ContentLength
	return meta, res, nil
}

// Request issues a request with the caller's headers, identity encoding and
// an optional Range header.
func (p *Prober) Request(ctx context.Context, method, rawURL string, headers http.Header, rangeHeader string) (*http.Response, error) {
	req, err := NewRequest(ctx, method, rawURL, headers, rangeHeader)
	if err != nil {
		return nil, err
	}
	return p.client.Do(req)
}

// NewRequest builds a request with identity encoding, the caller's headers and
// an optional Range header.
func NewRequest(ctx context.Context, method, rawURL string, headers http.Header, rangeHeader string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	if req.Header.Get("User-Agent") == "" {
		// A site's hosts may want a browser's agent (Google Drive's file
		// host answers some others with 500); one the caller gave wins.
		if ua := sites.UserAgent(req.URL); ua != "" {
			req.Header.Set("User-Agent", ua)
		} else {
			req.Header.Set("User-Agent", UserAgent)
		}
	}
	req.Header.Set("Accept-Encoding", "identity")
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	return req, nil
}

// applyHeaders copies representation metadata. Values present in h replace
// earlier ones because the GET response describes what will be fetched.
// Content-MD5 and Content-Digest describe the message body, so they are only
// meaningful on full (non-partial) responses.
func applyHeaders(meta *domain.RemoteMetadata, h http.Header, fullBody bool) {
	if v := h.Get("Content-Type"); v != "" {
		meta.MIMEType = v
	}
	if v := h.Get("ETag"); v != "" {
		meta.ETag = v
	}
	if v := h.Get("Last-Modified"); v != "" {
		meta.LastModified = v
	}
	if name := FilenameFromContentDisposition(h.Get("Content-Disposition")); name != "" {
		meta.Filename = name
	}
	if sum := ChecksumFromHeaders(h, fullBody); !sum.IsZero() {
		meta.Checksum = sum
	}
}

func parseLength(v string) (int64, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n, err == nil && n >= 0
}
