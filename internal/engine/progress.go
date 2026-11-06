package engine

import (
	"sync"
	"sync/atomic"
	"time"

	domain "github.com/vaisah/internal/domain/download"
)

const (
	// speedAlpha weights the newest sample in the exponential moving average.
	speedAlpha = 0.3
	// minSampleInterval avoids wild instantaneous speeds from tiny windows.
	minSampleInterval = 200 * time.Millisecond
)

// tracker aggregates transfer statistics. Workers only call add, which is a
// single atomic operation; speed bookkeeping happens in snapshot, called by
// the throttled progress loop.
type tracker struct {
	downloaded atomic.Int64
	active     atomic.Int32
	total      atomic.Int64

	mu           sync.Mutex
	sessionStart time.Time
	sessionBase  int64
	lastAt       time.Time
	lastBytes    int64
	speed        float64
	sampled      bool
}

func newTracker(total, base int64, now time.Time) *tracker {
	t := &tracker{}
	t.total.Store(total)
	t.reset(base, now)
	return t
}

func (t *tracker) add(n int64) { t.downloaded.Add(n) }

func (t *tracker) bytes() int64 { return t.downloaded.Load() }

// reset restarts the session, e.g. when a transfer starts over from base.
func (t *tracker) reset(base int64, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.downloaded.Store(base)
	t.sessionStart, t.sessionBase = now, base
	t.lastAt, t.lastBytes = now, base
	t.speed, t.sampled = 0, false
}

// snapshot computes progress at now. Speed is an exponential moving average
// of per-interval throughput; average speed covers the current session; ETA
// is reported only when both size and speed are known.
func (t *tracker) snapshot(id string, now time.Time) domain.Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur, total := t.downloaded.Load(), t.total.Load()
	if dt := now.Sub(t.lastAt); dt >= minSampleInterval {
		inst := max(0, float64(cur-t.lastBytes)/dt.Seconds())
		if t.sampled {
			t.speed = speedAlpha*inst + (1-speedAlpha)*t.speed
		} else {
			t.speed, t.sampled = inst, true
		}
		t.lastAt, t.lastBytes = now, cur
	}
	p := domain.Progress{DownloadID: id, Downloaded: cur, Total: total, Percent: -1, Speed: t.speed, ETASeconds: -1, ActiveConnections: int(t.active.Load())}
	if elapsed := now.Sub(t.sessionStart).Seconds(); elapsed > 0 {
		p.AverageSpeed = float64(cur-t.sessionBase) / elapsed
	}
	switch {
	case total > 0:
		p.Percent = min(100, float64(cur)*100/float64(total))
		if cur >= total {
			p.ETASeconds = 0
		} else if t.speed > 0 {
			p.ETASeconds = float64(total-cur) / t.speed
		}
	case total == 0:
		p.Percent, p.ETASeconds = 100, 0
	}
	return p
}
