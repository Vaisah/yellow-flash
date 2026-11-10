// Ported from Mister Download Manager, crates/tools/src/proc.rs (new_job)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree is a job of its own for one helper and every process it
// starts: yt-dlp.exe is a launcher whose child does the work, and ffmpeg
// runs under that child. Ending the job ends them all; ending the launcher
// alone left the download running, holding its file and the bandwidth. The
// job also ends them when Yellow Flash ends in any way (kill on close).
type processTree struct {
	mu  sync.Mutex
	job windows.Handle
}

func prepareTree(*exec.Cmd) {}

// adoptTree puts the started helper in a new job. Go can't start a process
// inside one, so it joins just after starting, as MDM's did.
func adoptTree(cmd *exec.Cmd) *processTree {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &processTree{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return &processTree{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return &processTree{}
	}
	err = windows.AssignProcessToJobObject(job, h)
	_ = windows.CloseHandle(h)
	if err != nil {
		_ = windows.CloseHandle(job)
		return &processTree{}
	}
	return &processTree{job: job}
}

// kill ends the helper and everything in its job.
func (t *processTree) kill(cmd *exec.Cmd) {
	t.mu.Lock()
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
	t.mu.Unlock()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// release closes the job once the helper has ended: what it left running
// ends with it.
func (t *processTree) release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
		t.job = 0
	}
}
