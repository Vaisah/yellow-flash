package platform

import (
	"testing"
	"time"
)

// Turning the computer off is not tested, for obvious reasons.

func TestKeepAwakeAndAWakeRequestCanBeTakenBack(t *testing.T) {
	release := KeepAwake()
	release()
	release() // twice is harmless

	if err := TurnOff("reboot", false); err == nil {
		t.Fatal("an unknown action was accepted")
	}
	cancel, err := WakeAt(time.Now().Add(time.Hour))
	if err != nil {
		t.Skipf("this system does not let us set a wake: %v", err)
	}
	cancel()
	cancel()
}
