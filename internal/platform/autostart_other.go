//go:build !windows && !linux

package platform

import "errors"

// Autostart reports whether name starts at sign-in: never here.
func Autostart(string) bool { return false }

// SetAutostart isn't available on this system.
func SetAutostart(string, string, bool) error {
	return errors.New("starting at sign-in isn't available on this system")
}
