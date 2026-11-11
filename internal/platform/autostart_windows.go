//go:build windows

package platform

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// runKey is where Windows starts this user's programs at sign-in.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Autostart reports whether name starts at sign-in.
func Autostart(name string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(name)
	return err == nil
}

// SetAutostart starts exe as name at sign-in, or no longer.
func SetAutostart(name, exe string, on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(name, `"`+exe+`"`)
	}
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
