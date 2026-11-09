// Ported from Mister Download Manager, crates/app/src/queue.rs and the queue and scheduler parts of crates/app/src/app.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package queues runs download queues and their schedules. It starts a
// running queue's downloads in order, a few at a time, through a small port
// (the engine stays unaware of queues), moves a failed one to the end and
// tries it again 30 s later, removes finished ones from download queues,
// starts and stops queues by the clock, and reports a queue that is done
// so the app can do what its schedule asks (open a file, exit, ...).
package queues

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
)

// Port is what the runner needs of the download engine.
type Port interface {
	Get(ctx context.Context, id string) (*domain.Download, error)
	Start(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
}

// Store persists queues.
type Store interface {
	List(ctx context.Context) ([]queue.Queue, error)
	Save(ctx context.Context, q queue.Queue) error
	Delete(ctx context.Context, id string) error
}

var (
	// ErrNoQueue is returned for an unknown queue.
	ErrNoQueue = errors.New("no such queue")
	// ErrBuiltin is returned when deleting a built-in queue.
	ErrBuiltin = errors.New("the built-in queues cannot be deleted")
)

type failure struct {
	at    time.Time
	tries int
}

// Runner runs the queues. Its methods are safe for concurrent use.
type Runner struct {
	port  Port
	store Store
	now   func() time.Time
	log   *slog.Logger
	// OnDone is called (outside the runner's lock) when a running queue
	// has nothing left to do.
	OnDone func(queue.Queue)

	mu       sync.Mutex
	queues   []queue.Queue
	failures map[string]failure
	seen     map[string]domain.Status
	lastTick time.Time
	wake     chan struct{}
}

// New loads the queues, adding the built-in Main and Synchronization
// queues when missing.
func New(ctx context.Context, port Port, store Store, log *slog.Logger) (*Runner, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	list, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	r := &Runner{port: port, store: store, now: time.Now, log: log, queues: list,
		failures: map[string]failure{}, seen: map[string]domain.Status{}, wake: make(chan struct{}, 1)}
	for _, b := range []queue.Queue{
		{ID: queue.MainID, Name: "Main download queue"},
		{ID: queue.SyncID, Name: "Synchronization queue", Sync: true},
	} {
		if r.find(b.ID) < 0 {
			b.FilesAtOnce, b.Schedule, b.CreatedAt = queue.DefaultFilesAtOnce, queue.DefaultSchedule(), time.Now().UTC()
			r.queues = append(r.queues, b)
			if err := store.Save(ctx, b); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

func (r *Runner) find(id string) int {
	for i := range r.queues {
		if r.queues[i].ID == id {
			return i
		}
	}
	return -1
}

// FindByName returns a queue's ID by its name (any case).
func (r *Runner) FindByName(name string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.queues {
		if strings.EqualFold(q.Name, strings.TrimSpace(name)) {
			return q.ID, true
		}
	}
	return "", false
}

// Queues returns copies of every queue, in order.
func (r *Runner) Queues() []queue.Queue {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]queue.Queue, len(r.queues))
	for i, q := range r.queues {
		out[i] = q.Clone()
	}
	return out
}

// edit applies fn to the queue with id and saves it.
func (r *Runner) edit(ctx context.Context, id string, fn func(*queue.Queue) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.find(id)
	if i < 0 {
		return fmt.Errorf("%w: %s", ErrNoQueue, id)
	}
	if err := fn(&r.queues[i]); err != nil {
		return err
	}
	err := r.store.Save(ctx, r.queues[i])
	r.Notify()
	return err
}

// Create adds a queue named name.
func (r *Runner) Create(ctx context.Context, name string) (queue.Queue, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return queue.Queue{}, errors.New("a queue needs a name")
	}
	if _, taken := r.FindByName(name); taken {
		return queue.Queue{}, fmt.Errorf("a queue named %q exists", name)
	}
	q := queue.Queue{ID: uuid.NewString(), Name: name, FilesAtOnce: queue.DefaultFilesAtOnce,
		Schedule: queue.DefaultSchedule(), CreatedAt: time.Now().UTC()}
	r.mu.Lock()
	r.queues = append(r.queues, q)
	r.mu.Unlock()
	return q.Clone(), r.store.Save(ctx, q)
}

// Delete removes one of the user's queues; its downloads stay in the list.
func (r *Runner) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	i := r.find(id)
	switch {
	case i < 0:
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNoQueue, id)
	case r.queues[i].Builtin():
		r.mu.Unlock()
		return ErrBuiltin
	}
	r.queues = append(r.queues[:i], r.queues[i+1:]...)
	r.mu.Unlock()
	return r.store.Delete(ctx, id)
}

// Add puts downloads at the end of a queue, taking them out of any other
// (a download is in one queue at most).
func (r *Runner) Add(ctx context.Context, queueID string, ids ...string) error {
	r.mu.Lock()
	if r.find(queueID) < 0 {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNoQueue, queueID)
	}
	var changed []queue.Queue
	for i := range r.queues {
		q := &r.queues[i]
		before := len(q.Items)
		q.Items = without(q.Items, ids)
		if q.ID == queueID {
			q.Items = append(q.Items, ids...)
		}
		if q.ID == queueID || len(q.Items) != before {
			changed = append(changed, q.Clone())
		}
	}
	r.Notify()
	r.mu.Unlock()
	return r.save(ctx, changed)
}

// Remove takes a download out of its queue; it stays in the list.
func (r *Runner) Remove(ctx context.Context, downloadID string) error {
	r.mu.Lock()
	var changed []queue.Queue
	for i := range r.queues {
		before := len(r.queues[i].Items)
		r.queues[i].Items = without(r.queues[i].Items, []string{downloadID})
		if len(r.queues[i].Items) != before {
			changed = append(changed, r.queues[i].Clone())
		}
	}
	r.mu.Unlock()
	return r.save(ctx, changed)
}

// Move moves a download delta places within its queue (negative: earlier).
func (r *Runner) Move(ctx context.Context, downloadID string, delta int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.queues {
		q := &r.queues[i]
		for j, id := range q.Items {
			if id != downloadID {
				continue
			}
			rest := append(q.Items[:j:j], q.Items[j+1:]...)
			k := min(max(j+delta, 0), len(rest))
			q.Items = append(rest[:k:k], append([]string{downloadID}, rest[k:]...)...)
			return r.store.Save(ctx, *q)
		}
	}
	return nil
}

// SetFilesAtOnce sets "Download N files at the same time".
func (r *Runner) SetFilesAtOnce(ctx context.Context, id string, n int) error {
	if n < 1 {
		return errors.New("a queue downloads at least one file at a time")
	}
	return r.edit(ctx, id, func(q *queue.Queue) error { q.FilesAtOnce = n; return nil })
}

// SetSchedule replaces a queue's schedule.
func (r *Runner) SetSchedule(ctx context.Context, id string, s queue.Schedule) error {
	for _, t := range []*queue.TimeOfDay{s.StartAt, s.StopAt} {
		if t != nil && (t.Hour < 0 || t.Hour > 23 || t.Minute < 0 || t.Minute > 59 || t.Second < 0 || t.Second > 59) {
			return fmt.Errorf("not a time of day: %02d:%02d:%02d", t.Hour, t.Minute, t.Second)
		}
	}
	if s.Retries != nil && *s.Retries < 0 {
		return errors.New("retries must not be negative")
	}
	return r.edit(ctx, id, func(q *queue.Queue) error { q.Schedule = s; return nil })
}

// Start runs a queue now.
func (r *Runner) Start(ctx context.Context, id string) error {
	return r.edit(ctx, id, func(q *queue.Queue) error { q.Running = true; return nil })
}

// Stop halts a queue: its running downloads are paused.
func (r *Runner) Stop(ctx context.Context, id string) error {
	var running []string
	err := r.edit(ctx, id, func(q *queue.Queue) error {
		q.Running = false
		running = r.runningLocked(ctx, *q)
		return nil
	})
	r.pauseAll(running)
	return err
}

// Startup starts the queues whose schedule says "on startup".
func (r *Runner) Startup(ctx context.Context) {
	for _, q := range r.Queues() {
		if q.Schedule.StartOnStartup {
			_ = r.Start(ctx, q.ID)
		}
	}
}

// Notify asks for a tick soon (a download changed). It never blocks.
func (r *Runner) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run ticks every second and whenever notified, until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
		r.Tick(ctx)
	}
}

// NextWake returns the earliest start time after now among the queues
// that should wake the computer for it ("Wake the computer to run this
// queue"), looking a week ahead.
func (r *Runner) NextWake(now time.Time) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best time.Time
	for _, q := range r.queues {
		s := q.Schedule
		if !s.Wake || s.StartAt == nil {
			continue
		}
		for d := 0; d <= 7; d++ {
			day := now.AddDate(0, 0, d)
			at := time.Date(day.Year(), day.Month(), day.Day(), s.StartAt.Hour, s.StartAt.Minute, s.StartAt.Second, 0, now.Location())
			if at.After(now) && s.StartsOn(queue.DateOf(at), at.Weekday()) {
				if best.IsZero() || at.Before(best) {
					best = at
				}
				break
			}
		}
	}
	return best, !best.IsZero()
}

// AnyRunning reports whether a queue is running.
func (r *Runner) AnyRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.queues {
		if q.Running {
			return true
		}
	}
	return false
}

func secondsOfDay(t time.Time) int { h, m, s := t.Clock(); return h*3600 + m*60 + s }

// Tick does one round: the schedules' starts and stops, then each queue's
// downloads.
func (r *Runner) Tick(ctx context.Context) {
	now := r.now()
	r.mu.Lock()
	var pause []string
	if !r.lastTick.IsZero() {
		prev, cur := secondsOfDay(r.lastTick), secondsOfDay(now)
		for i := range r.queues {
			q := &r.queues[i]
			s := q.Schedule
			if s.StartAt != nil && queue.Crossed(*s.StartAt, prev, cur) && s.StartsOn(queue.DateOf(now), now.Weekday()) {
				q.Running = true
			}
			if s.StopAt != nil && queue.Crossed(*s.StopAt, prev, cur) && q.Running {
				q.Running = false
				pause = append(pause, r.runningLocked(ctx, *q)...)
			}
		}
	}
	r.lastTick = now
	var done, changed []queue.Queue
	for i := range r.queues {
		q := &r.queues[i]
		before := append([]string(nil), q.Items...)
		member := r.memberFunc(ctx, q, now)
		// Members first (failures requeued, finished files dropped), then starts.
		for _, id := range before {
			member(id)
		}
		if q.Running {
			for _, id := range queue.ToStart(*q, member, now) {
				if err := r.port.Start(ctx, id); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
					r.log.Warn("queue could not start a download", "queue", q.Name, "id", id, "err", err)
				}
				r.seen[id] = domain.Initializing
			}
			if queue.Finished(*q, member) {
				q.Running = false
				done = append(done, q.Clone())
			}
		}
		if !sameItems(before, q.Items) {
			changed = append(changed, q.Clone())
		}
	}
	r.mu.Unlock()
	_ = r.save(ctx, changed)
	r.pauseAll(pause)
	for _, q := range done {
		r.log.Info("queue done", "queue", q.Name)
		if r.OnDone != nil {
			r.OnDone(q)
		}
	}
}

// memberFunc returns how each of q's downloads stands, recording failures
// (a file that just failed moves to the end of a running queue) and
// dropping downloads that are gone or, outside a sync queue, finished.
func (r *Runner) memberFunc(ctx context.Context, q *queue.Queue, now time.Time) func(string) queue.Member {
	cache := map[string]queue.Member{}
	return func(id string) queue.Member {
		if m, ok := cache[id]; ok {
			return m
		}
		var m queue.Member
		d, err := r.port.Get(ctx, id)
		switch {
		case errors.Is(err, domain.ErrNotFound), err == nil && d.Status == domain.Cancelled:
			q.Items = without(q.Items, []string{id})
			delete(r.seen, id)
			m.State = queue.Complete
		case err != nil:
			m.State = queue.Running // unknown for now: neither start it nor count it done
		case d.Status == domain.Completed:
			m.State = queue.Complete
			if !q.Sync {
				q.Items = without(q.Items, []string{id})
			}
		case d.Status == domain.Failed:
			f := r.failures[id]
			if r.seen[id] != domain.Failed {
				f = failure{at: now, tries: f.tries + 1}
				r.failures[id] = f
				if q.Running {
					queue.RequeueFailed(q, id)
				}
			}
			m = queue.Member{State: queue.Failed, At: f.at, Tries: f.tries}
		case d.Status == domain.Queued || d.Status == domain.Paused:
			m.State = queue.Waiting
		default:
			m.State = queue.Running
		}
		if err == nil {
			r.seen[id] = d.Status
		}
		cache[id] = m
		return m
	}
}

// runningLocked lists q's downloads that are running now.
func (r *Runner) runningLocked(ctx context.Context, q queue.Queue) []string {
	var out []string
	for _, id := range q.Items {
		if d, err := r.port.Get(ctx, id); err == nil && d.Status.IsActive() {
			out = append(out, id)
		}
	}
	return out
}

// pauseAll pauses downloads off the caller's goroutine (Pause waits for
// the download's workers).
func (r *Runner) pauseAll(ids []string) {
	for _, id := range ids {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := r.port.Pause(ctx, id); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
				r.log.Warn("queue could not pause a download", "id", id, "err", err)
			}
		}()
	}
}

func (r *Runner) save(ctx context.Context, qs []queue.Queue) error {
	var errs []error
	for _, q := range qs {
		errs = append(errs, r.store.Save(ctx, q))
	}
	return errors.Join(errs...)
}

func without(items, drop []string) []string {
	out := items[:0:0]
	for _, id := range items {
		keep := true
		for _, d := range drop {
			keep = keep && id != d
		}
		if keep {
			out = append(out, id)
		}
	}
	return out
}

func sameItems(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
