//go:build linux

package platform

import (
	"fmt"
	"os/exec"
	"strings"
)

// trash runs gio trash (GLib's, on every common desktop).
func trash(files []string) error {
	gio, err := exec.LookPath("gio")
	if err != nil {
		return errNoTrash
	}
	out, err := exec.Command(gio, append([]string{"trash", "--"}, files...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gio trash: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
