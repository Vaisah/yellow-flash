//go:build windows

package extract

import (
	"os/exec"
	"syscall"
)

// hideWindow runs 7-Zip without a console window flashing up.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
