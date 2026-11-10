//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// processTree is the helper's own process group: it and everything it
// starts (yt-dlp's ffmpeg) are ended together.
type processTree struct{}

func prepareTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func adoptTree(*exec.Cmd) *processTree { return &processTree{} }

// kill ends the helper's process group.
func (t *processTree) kill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func (t *processTree) release() {}
