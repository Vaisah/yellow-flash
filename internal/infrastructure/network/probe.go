package network

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	domain "github.com/vaisah/internal/domain/download"
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

	res, err := p.Request(ctx, http.MethodGet, meta.URL, headers, RangeHeader(0, 0))
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
		if res.ContentLength >= 0 {
			meta.Size = res.ContentLength
		}
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
		req.Header.Set("User-Agent", UserAgent)
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
	if algorithm, sum := ChecksumFromHeaders(h, fullBody); len(sum) > 0 {
		meta.ChecksumAlgorithm, meta.Checksum = algorithm, sum
	}
}

// FilenameFromContentDisposition extracts the (unsanitized) filename,
// decoding RFC 5987 filename* values. It returns "" when absent or malformed.
func FilenameFromContentDisposition(value string) string {
	if value == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["filename"])
}

// ChecksumFromHeaders returns a whole-file digest advertised by the server.
// Repr-Digest and Digest describe the full representation; Content-MD5 and
// Content-Digest describe the message body and are used only when fullBody
// is true. Malformed values are ignored rather than trusted.
func ChecksumFromHeaders(h http.Header, fullBody bool) (string, []byte) {
	if fullBody {
		if v := strings.TrimSpace(h.Get("Content-MD5")); v != "" {
			if sum, err := base64.StdEncoding.DecodeString(v); err == nil && len(sum) == md5.Size {
				return "md5", sum
			}
		}
	}
	names := []string{"Repr-Digest", "Digest"}
	if fullBody {
		names = append(names, "Content-Digest")
	}
	for _, header := range names {
		for _, part := range strings.Split(h.Get(header), ",") {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok {
				continue
			}
			algorithm := strings.ToLower(strings.TrimSpace(name))
			if algorithm == "sha256" {
				algorithm = "sha-256"
			}
			if algorithm != "sha-256" && algorithm != "md5" {
				continue
			}
			sum, err := base64.StdEncoding.DecodeString(strings.Trim(strings.TrimSpace(value), ":\""))
			if err != nil {
				continue
			}
			if (algorithm == "md5" && len(sum) == md5.Size) || (algorithm == "sha-256" && len(sum) == sha256.Size) {
				return algorithm, sum
			}
		}
	}
	return "", nil
}

func parseLength(v string) (int64, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return n, err == nil && n >= 0
}
