package network

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidContentRange is returned for malformed Content-Range headers.
var ErrInvalidContentRange = errors.New("invalid Content-Range")

// ContentRange is a parsed "bytes start-end/total" header. Total is -1 when
// the server reports "*".
type ContentRange struct {
	Start, End, Total int64
}

// Length is the number of bytes the range covers.
func (c ContentRange) Length() int64 { return c.End - c.Start + 1 }

// ParseContentRange strictly parses a satisfied byte Content-Range header.
func ParseContentRange(value string) (ContentRange, error) {
	value = strings.TrimSpace(value)
	rest, ok := strings.CutPrefix(value, "bytes ")
	if !ok {
		return ContentRange{}, fmt.Errorf("%w: %q", ErrInvalidContentRange, value)
	}
	span, total, ok := strings.Cut(strings.TrimSpace(rest), "/")
	if !ok {
		return ContentRange{}, fmt.Errorf("%w: %q", ErrInvalidContentRange, value)
	}
	first, last, ok := strings.Cut(span, "-")
	if !ok {
		return ContentRange{}, fmt.Errorf("%w: %q", ErrInvalidContentRange, value)
	}
	cr := ContentRange{Total: -1}
	var err error
	if cr.Start, err = parseOffset(first); err != nil {
		return ContentRange{}, fmt.Errorf("%w: %q", ErrInvalidContentRange, value)
	}
	if cr.End, err = parseOffset(last); err != nil || cr.End < cr.Start {
		return ContentRange{}, fmt.Errorf("%w: %q", ErrInvalidContentRange, value)
	}
	if total != "*" {
		if cr.Total, err = parseOffset(total); err != nil || cr.End >= cr.Total {
			return ContentRange{}, fmt.Errorf("%w: %q", ErrInvalidContentRange, value)
		}
	}
	return cr, nil
}

// ParseUnsatisfiedRange parses the "bytes */total" form sent with HTTP 416.
func ParseUnsatisfiedRange(value string) (int64, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(value), "bytes */")
	if !ok {
		return 0, false
	}
	total, err := parseOffset(rest)
	return total, err == nil
}

// RangeHeader formats an inclusive Range request header. An end below zero
// requests everything from start onward.
func RangeHeader(start, end int64) string {
	if end < 0 {
		return fmt.Sprintf("bytes=%d-", start)
	}
	return fmt.Sprintf("bytes=%d-%d", start, end)
}

// parseOffset accepts only ASCII digits and rejects overflow.
func parseOffset(s string) (int64, error) {
	if s == "" || len(s) > 19 {
		return 0, ErrInvalidContentRange
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrInvalidContentRange
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
