//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func turnOff(mode string, force bool) error {
	action := map[string]string{"shutdown": "poweroff", "sleep": "suspend", "hibernate": "hibernate"}[mode]
	args := []string{action}
	if force {
		args = append(args, "-i") // ignore inhibitors
	}
	return exec.Command("systemctl", args...).Run()
}

// keepAwake holds a systemd sleep inhibitor while it lasts.
func keepAwake() func() {
	cmd := exec.Command("systemd-inhibit", "--what=sleep:idle", "--who=Yellow Flash",
		"--why=Downloading", "--mode=block", "sleep", "infinity")
	if cmd.Start() != nil {
		return func() {}
	}
	go func() { _ = cmd.Wait() }()
	return func() { _ = cmd.Process.Kill() }
}

// The RTC's wake alarm: an administrator lets a group write it (see
// HANDOFF.md); there is one alarm for the whole system.
const wakeAlarm = "/sys/class/rtc/rtc0/wakealarm"

func wakeAt(t time.Time) (func(), error) {
	if err := os.WriteFile(wakeAlarm, []byte("0"), 0); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("only an administrator may set the wake alarm (%s): %w", wakeAlarm, ErrPowerUnsupported)
		}
		return nil, err
	}
	if err := os.WriteFile(wakeAlarm, []byte(strconv.FormatInt(t.Unix(), 10)), 0); err != nil {
		return nil, err
	}
	return func() { _ = os.WriteFile(wakeAlarm, []byte("0"), 0) }, nil
}
