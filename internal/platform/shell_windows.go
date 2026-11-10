//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

func openPath(path string) error {
	// No shell is involved; the path is passed as a single argument.
	return start(exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path))
}

func revealPath(path string) error {
	// explorer.exe needs the literal form /select,"path"; checkPath has
	// already rejected quotes, so the path cannot terminate the argument.
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return start(cmd)
}

// HideConsole keeps a console program run from the app (the native host's
// --register) from opening a console window.
func HideConsole(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// start launches without waiting: explorer.exe exits with code 1 even on
// success, so its exit status is meaningless.
func start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
