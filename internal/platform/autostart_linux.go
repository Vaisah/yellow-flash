//go:build linux

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// autostartFile is the freedesktop autostart entry for name.
func autostartFile(name string) (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", strings.ToLower(strings.ReplaceAll(name, " ", "-"))+".desktop"), nil
}

// Autostart reports whether name starts at sign-in.
func Autostart(name string) bool {
	p, err := autostartFile(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// SetAutostart starts exe as name at sign-in, or no longer.
func SetAutostart(name, exe string, on bool) error {
	p, err := autostartFile(name)
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	entry := "[Desktop Entry]\nType=Application\nName=" + name + "\nExec=\"" + exe + "\"\nX-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(p, []byte(entry), 0o644)
}
