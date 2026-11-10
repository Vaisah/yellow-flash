//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
)

func openPath(path string) error { return start(exec.Command("xdg-open", path)) }

// revealPath opens the containing folder; xdg-open has no portable
// "select this file" form.
func revealPath(path string) error {
	dir := path
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		dir = filepath.Dir(path)
	}
	return start(exec.Command("xdg-open", dir))
}

// HideConsole does nothing here: only Windows opens a console window.
func HideConsole(*exec.Cmd) {}

func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
