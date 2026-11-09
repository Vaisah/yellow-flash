// Ported from Mister Download Manager's Scheduler "Download limits" (docs/queues-and-scheduler.md)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package datacap enforces "Download no more than N MBytes every H hours"
// for internet plans with a data cap. When the period's bytes reach the cap
// every running download is paused, and any that starts meanwhile too;
// when the period ends, the downloads it paused are resumed. Pausing,
// unlike throttling, never trips a connection's idle timeout.
package datacap

import (
	"context"
	"log/slog"
	"sync"
	"time"

	domain "github.com/vaisah/internal/domain/download"
)

// Port is what the watcher needs of the engine.
type Port interface {
	BytesReceived() int64
	List(ctx context.Context) ([]domain.Download, error)
	Pause(ctx context.Context, id string) error
	Start(ctx context.Context, id string) error
}

// Watcher watches the bytes received against the cap.
type Watcher struct {
	port Port
	now  func() time.Time
	log  *slog.Logger
	// OnChange is called when downloads stop (blocked, until when) or go
	// on again.
	OnChange func(blocked bool, until time.Time)

	mu      sync.Mutex
	cap     int64
	period  time.Duration
	start   time.Time
	base    int64
	blocked bool
	paused  map[string]bool
}

// New returns a watcher with no cap.
func New(port Port, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Watcher{port: port, now: time.Now, log: log, paused: map[string]bool{}}
}

// Set changes the cap (bytes per period); a zero cap or period removes it,
// resuming what it paused. A new period starts now.
func (w *Watcher) Set(capBytes int64, period time.Duration) {
	w.mu.Lock()
	w.cap, w.period = capBytes, period
	w.start, w.base = w.now(), w.port.BytesReceived()
	w.mu.Unlock()
	w.Tick(context.Background())
}

// Status reports whether downloads are held, and until when.
func (w *Watcher) Status() (blocked bool, until time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.blocked, w.start.Add(w.period)
}

// Run ticks every few seconds until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Tick(ctx)
		}
	}
}

// Tick checks the cap.
func (w *Watcher) Tick(ctx context.Context) {
	w.mu.Lock()
	now, taken := w.now(), w.port.BytesReceived()
	var resume, pause []string
	var changed bool
	switch {
	case w.cap <= 0 || w.period <= 0:
		if w.blocked {
			resume, changed = w.release(), true
		}
	case !now.Before(w.start.Add(w.period)):
		// A new period.
		w.start, w.base = now, taken
		if w.blocked {
			resume, changed = w.release(), true
		}
	case !w.blocked && taken-w.base >= w.cap:
		w.blocked, changed = true, true
	}
	blocked, until := w.blocked, w.start.Add(w.period)
	w.mu.Unlock()

	if blocked {
		// Everything running now, including what started meanwhile.
		if list, err := w.port.List(ctx); err == nil {
			w.mu.Lock()
			for _, d := range list {
				if d.Status.IsActive() && d.Status != domain.Pausing && !w.paused[d.ID] {
					w.paused[d.ID] = true
					pause = append(pause, d.ID)
				}
			}
			w.mu.Unlock()
		}
	}
	for _, id := range pause {
		go func() {
			pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = w.port.Pause(pctx, id)
		}()
	}
	for _, id := range resume {
		if err := w.port.Start(ctx, id); err != nil {
			w.log.Debug("data cap: could not resume", "id", id, "err", err)
		}
	}
	if changed && w.OnChange != nil {
		w.OnChange(blocked, until)
	}
}

// release clears the block and returns what it paused. Call with mu held.
func (w *Watcher) release() []string {
	w.blocked = false
	out := make([]string, 0, len(w.paused))
	for id := range w.paused {
		out = append(out, id)
	}
	w.paused = map[string]bool{}
	return out
}
