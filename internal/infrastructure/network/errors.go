package network

import (
	"errors"
	"fmt"
	"net/http"
	"syscall"
)

// StatusError is an unexpected HTTP response status.
type StatusError struct {
	Code       int
	Status     string
	RetryAfter string
}

// NewStatusError builds a StatusError from a response.
func NewStatusError(res *http.Response) *StatusError {
	status := res.Status
	if status == "" {
		status = fmt.Sprintf("%d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	return &StatusError{Code: res.StatusCode, Status: status, RetryAfter: res.Header.Get("Retry-After")}
}

func (e *StatusError) Error() string { return "HTTP " + e.Status }

// IsConnectionReset reports whether err is a peer reset or aborted connection.
func IsConnectionReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) || platformConnectionReset(err)
}
