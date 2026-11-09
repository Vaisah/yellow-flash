package queues

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
)

// fakeEngine holds download statuses; Start makes one Downloading.
type fakeEngine struct {
	mu       sync.Mutex
	status   map[string]domain.Status
	started  []string
	paused   []string
	maxAlive int
}

func (f *fakeEngine) Get(_ context.Context, id string) (*domain.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.status[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.Download{ID: id, Status: s}, nil
}

func (f *fakeEngine) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = domain.Downloading
	f.started = append(f.started, id)
	alive := 0
	for _, s := range f.status {
		if s.IsActive() {
			alive++
		}
	}
	f.maxAlive = max(f.maxAlive, alive)
	return nil
}

func (f *fakeEngine) Pause(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = domain.Paused
	f.paused = append(f.paused, id)
	return nil
}

func (f *fakeEngine) set(id string, s domain.Status) {
	f.mu.Lock()
	f.status[id] = s
	f.mu.Unlock()
}

type memStore struct {
	mu sync.Mutex
	qs map[string]queue.Queue
}

func (m *memStore) List(context.Context) ([]queue.Queue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []queue.Queue
	for _, q := range m.qs {
		out = append(out, q.Clone())
	}
	return out, nil
}
func (m *memStore) Save(_ context.Context, q queue.Queue) error {
	m.mu.Lock()
	m.qs[q.ID] = q.Clone()
	m.mu.Unlock()
	return nil
}
func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.qs, id)
	m.mu.Unlock()
	return nil
}

var ctx = context.Background()

func setup(t *testing.T, ids ...string) (*Runner, *fakeEngine, *memStore, *time.Time) {
	t.Helper()
	eng := &fakeEngine{status: map[string]domain.Status{}}
	for _, id := range ids {
		eng.status[id] = domain.Queued
	}
	store := &memStore{qs: map[string]queue.Queue{}}
	r, err := New(ctx, eng, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 9, 22, 59, 58, 0, time.Local) // a Monday
	r.now = func() time.Time { return clock }
	return r, eng, store, &clock
}

func TestAQueueRunsItsFilesAFewAtATimeInOrder(t *testing.T) {
	r, eng, store, _ := setup(t, "a", "b", "c", "d", "e")
	if err := r.SetFilesAtOnce(ctx, queue.MainID, 2); err != nil {
		t.Fatal(err)
	}
	_ = r.Add(ctx, queue.MainID, "a", "b", "c", "d", "e")
	var done []string
	r.OnDone = func(q queue.Queue) { done = append(done, q.ID) }
	r.Tick(ctx) // not running yet: nothing starts
	if len(eng.started) != 0 {
		t.Fatalf("started %v before the queue ran", eng.started)
	}
	_ = r.Start(ctx, queue.MainID)
	r.Tick(ctx)
	if fmt.Sprint(eng.started) != "[a b]" {
		t.Fatalf("started %v", eng.started)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		eng.set(id, domain.Completed)
		r.Tick(ctx)
	}
	r.Tick(ctx)
	if eng.maxAlive > 2 || len(eng.started) != 5 {
		t.Fatalf("max %d at once, started %v", eng.maxAlive, eng.started)
	}
	if fmt.Sprint(done) != "[main]" {
		t.Fatalf("done %v", done)
	}
	q := store.qs[queue.MainID]
	if len(q.Items) != 0 || r.Queues()[0].Running {
		t.Fatalf("finished files leave the main queue, which stops: %+v", q)
	}
}

func TestAFailedFileMovesToTheEndAndIsRetriedLater(t *testing.T) {
	r, eng, _, clock := setup(t, "a", "b")
	_ = r.SetFilesAtOnce(ctx, queue.MainID, 1)
	_ = r.Add(ctx, queue.MainID, "a", "b")
	_ = r.Start(ctx, queue.MainID)
	r.Tick(ctx)
	eng.set("a", domain.Failed)
	r.Tick(ctx)
	if q := r.Queues()[0]; fmt.Sprint(q.Items) != "[b a]" || fmt.Sprint(eng.started) != "[a b]" {
		t.Fatalf("items %v started %v", q.Items, eng.started)
	}
	eng.set("b", domain.Completed)
	r.Tick(ctx)
	if len(eng.started) != 2 {
		t.Fatalf("retried before 30 s: %v", eng.started)
	}
	*clock = clock.Add(queue.RetryDelay)
	r.Tick(ctx)
	if fmt.Sprint(eng.started) != "[a b a]" {
		t.Fatalf("not retried: %v", eng.started)
	}
	// With a retry limit, a file that keeps failing ends the queue.
	one := 1
	s := queue.DefaultSchedule()
	s.Retries = &one
	_ = r.SetSchedule(ctx, queue.MainID, s)
	eng.set("a", domain.Failed)
	r.Tick(ctx)
	if r.Queues()[0].Running {
		t.Fatal("a queue whose last file used up its retries is done")
	}
}

func TestTheSchedulerStartsAndStopsByTheClock(t *testing.T) {
	r, eng, _, clock := setup(t, "a")
	_ = r.Add(ctx, queue.MainID, "a")
	s := queue.DefaultSchedule()
	s.StartAt, s.StopAt = &queue.TimeOfDay{Hour: 23}, &queue.TimeOfDay{Hour: 23, Second: 5}
	_ = r.SetSchedule(ctx, queue.MainID, s)
	r.Tick(ctx) // 22:59:58
	*clock = clock.Add(time.Second)
	r.Tick(ctx) // 22:59:59
	if len(eng.started) != 0 {
		t.Fatal("started early")
	}
	*clock = clock.Add(time.Second)
	r.Tick(ctx) // 23:00:00
	if fmt.Sprint(eng.started) != "[a]" {
		t.Fatalf("not started at 23:00: %v", eng.started)
	}
	*clock = clock.Add(5 * time.Second)
	r.Tick(ctx) // 23:00:05
	deadline := time.Now().Add(time.Second)
	for {
		eng.mu.Lock()
		paused := len(eng.paused)
		eng.mu.Unlock()
		if paused == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not stopped at the stop time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Not on an unchecked day.
	r2, eng2, _, clock2 := setup(t, "x")
	_ = r2.Add(ctx, queue.MainID, "x")
	s.Days[time.Monday] = false
	_ = r2.SetSchedule(ctx, queue.MainID, s)
	r2.Tick(ctx)
	*clock2 = clock2.Add(2 * time.Second)
	r2.Tick(ctx)
	if len(eng2.started) != 0 {
		t.Fatal("started on an unchecked day")
	}
}

func TestTheNextWakeIsTheEarliestScheduledStart(t *testing.T) {
	r, _, _, clock := setup(t) // Monday 22:59:58
	if _, ok := r.NextWake(*clock); ok {
		t.Fatal("no queue asks to wake the computer")
	}
	s := queue.DefaultSchedule()
	s.Wake, s.StartAt = true, &queue.TimeOfDay{Hour: 7, Minute: 30}
	s.Days[time.Tuesday] = false
	_ = r.SetSchedule(ctx, queue.MainID, s)
	at, ok := r.NextWake(*clock)
	if !ok || at.Weekday() != time.Wednesday || at.Hour() != 7 || at.Minute() != 30 {
		t.Fatalf("next wake %v %v (Tuesday unchecked)", at, ok)
	}
	later := queue.DefaultSchedule()
	later.Wake, later.StartAt = true, &queue.TimeOfDay{Hour: 23}
	_ = r.SetSchedule(ctx, queue.SyncID, later)
	if at, _ = r.NextWake(*clock); at.Day() != clock.Day() || at.Hour() != 23 {
		t.Fatalf("tonight's 23:00 comes first: %v", at)
	}
	if r.AnyRunning() {
		t.Fatal("nothing runs")
	}
}

func TestQueuesHoldEachDownloadOnceAndKeepTheirOwnRules(t *testing.T) {
	r, eng, _, _ := setup(t, "a", "b", "c")
	if err := r.Delete(ctx, queue.MainID); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("built-in deleted: %v", err)
	}
	night, err := r.Create(ctx, "Night")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Create(ctx, "night"); err == nil {
		t.Fatal("two queues with one name")
	}
	_ = r.Add(ctx, queue.MainID, "a", "b", "c")
	_ = r.Add(ctx, night.ID, "b")
	_ = r.Move(ctx, "c", -5)
	byID := map[string]queue.Queue{}
	for _, q := range r.Queues() {
		byID[q.ID] = q
	}
	if fmt.Sprint(byID[queue.MainID].Items) != "[c a]" || fmt.Sprint(byID[night.ID].Items) != "[b]" {
		t.Fatalf("main %v night %v", byID[queue.MainID].Items, byID[night.ID].Items)
	}
	// The sync queue keeps its finished files.
	_ = r.Add(ctx, queue.SyncID, "a")
	eng.set("a", domain.Completed)
	_ = r.Start(ctx, queue.SyncID)
	r.Tick(ctx)
	for _, q := range r.Queues() {
		if q.ID == queue.SyncID && fmt.Sprint(q.Items) != "[a]" {
			t.Fatalf("sync queue dropped a finished file: %v", q.Items)
		}
	}
	// A deleted download leaves its queue.
	eng.mu.Lock()
	delete(eng.status, "b")
	eng.mu.Unlock()
	r.Tick(ctx)
	for _, q := range r.Queues() {
		if q.ID == night.ID && len(q.Items) != 0 {
			t.Fatalf("a deleted download stayed: %v", q.Items)
		}
	}
	if err = r.Delete(ctx, night.ID); err != nil {
		t.Fatal(err)
	}
}
