package main

import (
	"context"
	"sync"
	"time"

	"github.com/vaisah/internal/platform"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Turning the computer off when a queue is done, after a 30-second
// countdown the user can cancel or skip; keeping it awake while a queue
// runs; and waking it for a queue's scheduled start.

const turnOffCountdown = 30 * time.Second

var power struct {
	mu      sync.Mutex
	pending chan bool // true: now, false: cancelled
}

// startTurnOff shows the countdown and turns the computer off at its end.
func (a *App) startTurnOff(ctx context.Context, mode string, force bool) {
	power.mu.Lock()
	if power.pending != nil {
		power.mu.Unlock()
		return // one countdown at a time
	}
	ch := make(chan bool, 1)
	power.pending = ch
	power.mu.Unlock()
	a.focusWindow(ctx)
	runtime.EventsEmit(ctx, "power:countdown", map[string]any{"mode": mode, "seconds": int(turnOffCountdown / time.Second)})
	go func() {
		t := time.NewTimer(turnOffCountdown)
		defer t.Stop()
		proceed := true
		select {
		case <-t.C:
		case proceed = <-ch:
		}
		power.mu.Lock()
		power.pending = nil
		power.mu.Unlock()
		if !proceed {
			runtime.EventsEmit(ctx, "power:cancelled", mode)
			return
		}
		if err := platform.TurnOff(mode, force); err != nil {
			a.notice(ctx, "error", "Could not "+mode+" the computer: "+err.Error())
		}
	}()
}

// CancelTurnOff stops the countdown to turning the computer off.
func (a *App) CancelTurnOff() { answerTurnOff(false) }

// TurnOffNow skips the rest of the countdown.
func (a *App) TurnOffNow() { answerTurnOff(true) }

func answerTurnOff(now bool) {
	power.mu.Lock()
	defer power.mu.Unlock()
	if power.pending != nil {
		select {
		case power.pending <- now:
		default:
		}
	}
}

// powerLoop keeps the computer awake while a queue runs and keeps a wake
// request one minute before the next scheduled start of a queue that asks
// for it.
func (a *App) powerLoop(ctx context.Context) {
	var release, cancelWake func()
	var wakeFor time.Time
	reported := false
	defer func() {
		if release != nil {
			release()
		}
		if cancelWake != nil {
			cancelWake()
		}
	}()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if r := a.queues.Load(); r != nil {
			switch running := r.AnyRunning(); {
			case running && release == nil:
				release = platform.KeepAwake()
			case !running && release != nil:
				release()
				release = nil
			}
			next, ok := r.NextWake(time.Now())
			if !next.Equal(wakeFor) {
				if cancelWake != nil {
					cancelWake()
					cancelWake = nil
				}
				wakeFor = next
				if ok {
					c, err := platform.WakeAt(next.Add(-time.Minute))
					switch {
					case err == nil:
						cancelWake = c
					case !reported:
						reported = true
						runtime.EventsEmit(ctx, "power:wake-error", err.Error())
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
