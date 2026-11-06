package engine

import (
	"net/http"
	"strings"

	domain "github.com/vaisah/internal/domain/download"
)

// representationChanged reports whether remote metadata contradicts what was
// recorded when the existing bytes were downloaded. It is conservative: a
// validator that disappears counts as a change, because the bytes can no
// longer be proven to belong to the same representation.
func representationChanged(d *domain.Download, m domain.RemoteMetadata) bool {
	if d.Size >= 0 && m.Size >= 0 && d.Size != m.Size {
		return true
	}
	if d.ETag != "" {
		return m.ETag != d.ETag
	}
	if d.LastModified != "" {
		return m.LastModified != d.LastModified
	}
	return false
}

// ifRangeValidator returns the If-Range value: a strong ETag when available,
// otherwise Last-Modified. Weak ETags are not permitted in If-Range.
func ifRangeValidator(etag, lastModified string) string {
	if etag != "" && !strings.HasPrefix(etag, "W/") {
		return etag
	}
	return lastModified
}

// buildHeaders merges persisted (non-secret) headers with secrets.
func buildHeaders(public, secret map[string]string) http.Header {
	h := http.Header{}
	for k, v := range public {
		h.Set(k, v)
	}
	for k, v := range secret {
		h.Set(k, v)
	}
	return h
}
