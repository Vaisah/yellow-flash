// Ported from Mister Download Manager, crates/engine/src/limiter.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// burstSeconds of budget may accumulate while no connection is reading
// (all between requests, say), so short gaps do not pull the average below
// the limit.
const burstSeconds = 1.0

// Limiter is a token bucket shared by the connections it limits: one per
// download (IDM's "maximum download speed for one file") and one for all
// downloads together. Throttling socket reads makes TCP slow the server
// down. The rate can change while downloads run.
type Limiter struct {
	rate  atomic.Int64 // bytes per second; 0 is unlimited
	taken atomic.Int64 // every byte through it, limited or not, less refunds

	mu     sync.Mutex
	tokens float64 // may go negative: debt to wait off
	last   time.Time
	now    func() time.Time
}

// NewLimiter returns a limiter at bytesPerSecond (0 or less: unlimited).
func NewLimiter(bytesPerSecond int64) *Limiter {
	l := &Limiter{now: time.Now}
	l.last = l.now()
	l.Set(bytesPerSecond)
	return l
}

// Set changes the rate; 0 or less removes the limit.
func (l *Limiter) Set(bytesPerSecond int64) { l.rate.Store(max(0, bytesPerSecond)) }

// Rate returns the limit in bytes per second (0: none).
func (l *Limiter) Rate() int64 { return l.rate.Load() }

// Taken returns the bytes that went through so far, less those refunded.
func (l *Limiter) Taken() int64 { return l.taken.Load() }

// take accounts for n received bytes and returns how long to wait first.
func (l *Limiter) take(n int64) time.Duration {
	l.taken.Add(n)
	rate := float64(l.rate.Load())
	if rate == 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	elapsed := now.Sub(l.last).Seconds()
	l.last = now
	l.tokens = min(l.tokens+elapsed*rate, rate*burstSeconds)
	l.tokens -= float64(n)
	if l.tokens >= 0 {
		return 0
	}
	return time.Duration(-l.tokens / rate * float64(time.Second))
}

// Refund gives back budget for n bytes paid for but not kept.
func (l *Limiter) Refund(n int64) {
	for {
		t := l.taken.Load()
		if l.taken.CompareAndSwap(t, max(0, t-n)) {
			break
		}
	}
	if l.rate.Load() > 0 {
		l.mu.Lock()
		l.tokens += float64(n)
		l.mu.Unlock()
	}
}

// Wait blocks until n more bytes fit under the limit, or ctx ends.
func (l *Limiter) Wait(ctx context.Context, n int) error {
	if l == nil {
		return nil
	}
	wait := l.take(int64(n))
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
