//go:build !windows && !linux

package platform

import "time"

func turnOff(string, bool) error       { return ErrPowerUnsupported }
func keepAwake() func()                { return func() {} }
func wakeAt(time.Time) (func(), error) { return nil, ErrPowerUnsupported }
