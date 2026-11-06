//go:build windows

package network

import (
	"errors"
	"syscall"
)

// platformConnectionReset recognises Winsock reset and abort codes.
func platformConnectionReset(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED)
}
