//go:build !windows

package network

// platformConnectionReset has nothing to add beyond the POSIX errnos.
func platformConnectionReset(error) bool { return false }
