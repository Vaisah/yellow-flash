// Ported from Mister Download Manager's queue "when done" actions and crates/app/src/wake.rs (behaviour)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package platform

import (
	"errors"
	"fmt"
	"time"
)

// ErrPowerUnsupported is returned where the system offers no way to do it.
var ErrPowerUnsupported = errors.New("not supported on this system")

// TurnOff shuts the computer down, puts it to sleep or hibernates it
// (mode "shutdown", "sleep" or "hibernate"). force makes other programs
// close rather than hold it up.
func TurnOff(mode string, force bool) error {
	switch mode {
	case "shutdown", "sleep", "hibernate":
		return turnOff(mode, force)
	}
	return fmt.Errorf("unknown power action %q", mode)
}

// KeepAwake keeps the computer from sleeping on its own until release is
// called (a queue is downloading). The display may still turn off.
func KeepAwake() (release func()) { return keepAwake() }

// WakeAt asks the system to wake the computer from sleep at t. cancel takes
// the request back.
func WakeAt(t time.Time) (cancel func(), err error) { return wakeAt(t) }
