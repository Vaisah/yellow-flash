//go:build windows

package platform

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                    = windows.NewLazySystemDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
	procCreateWaitableTimerW    = kernel32.NewProc("CreateWaitableTimerW")
	procSetWaitableTimer        = kernel32.NewProc("SetWaitableTimer")
	procCancelWaitableTimer     = kernel32.NewProc("CancelWaitableTimer")
	procSetSuspendState         = windows.NewLazySystemDLL("powrprof.dll").NewProc("SetSuspendState")
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

func turnOff(mode string, force bool) error {
	if mode == "shutdown" {
		args := []string{"/s", "/t", "0"}
		if force {
			args = append(args, "/f")
		}
		return exec.Command("shutdown.exe", args...).Run()
	}
	hibernate, forced := uintptr(0), uintptr(0)
	if mode == "hibernate" {
		hibernate = 1
	}
	if force {
		forced = 1
	}
	if err := procSetSuspendState.Find(); err != nil {
		return ErrPowerUnsupported
	}
	if r, _, err := procSetSuspendState.Call(hibernate, forced, 0); r == 0 {
		return fmt.Errorf("SetSuspendState: %w", err)
	}
	return nil
}

// keepAwake sets the execution state on a goroutine locked to its thread:
// the state belongs to the thread that set it.
func keepAwake() func() {
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, _, _ = procSetThreadExecutionState.Call(esContinuous | esSystemRequired)
		<-done
		_, _, _ = procSetThreadExecutionState.Call(esContinuous)
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

func wakeAt(t time.Time) (func(), error) {
	h, _, err := procCreateWaitableTimerW.Call(0, 1, 0)
	if h == 0 {
		return nil, fmt.Errorf("CreateWaitableTimer: %w", err)
	}
	// A positive due time is absolute: 100 ns since 1601, UTC.
	ft := windows.NsecToFiletime(t.UnixNano())
	due := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	// fResume = TRUE: wake the computer for it.
	if r, _, err := procSetWaitableTimer.Call(h, uintptr(unsafe.Pointer(&due)), 0, 0, 0, 1); r == 0 {
		_ = windows.CloseHandle(windows.Handle(h))
		return nil, fmt.Errorf("SetWaitableTimer (are wake timers allowed in the power plan?): %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_, _, _ = procCancelWaitableTimer.Call(h)
			_ = windows.CloseHandle(windows.Handle(h))
		})
	}, nil
}
