//go:build !windows

package browser

import "os/exec"

// installedBrowsers finds each browser's command on the PATH (Linux; on
// macOS none of these commands is usually there, so none is found).
func installedBrowsers() []Installed {
	var out []Installed
	for _, b := range Browsers {
		for _, cmd := range b.LinuxCmds {
			if p, err := exec.LookPath(cmd); err == nil {
				out = append(out, Installed{Name: b.Name, Program: p})
				break
			}
		}
	}
	return out
}
