package datacap

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
)

type fakeEngine struct {
	mu      sync.Mutex
	bytes   int64
	status  map[string]domain.Status
	resumed []string
}

func (f *fakeEngine) BytesReceived() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.bytes }
func (f *fakeEngine) List(context.Context) ([]domain.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Download
	for id, s := range f.status {
		out = append(out, domain.Download{ID: id, Status: s})
	}
	return out, nil
}
func (f *fakeEngine) Pause(_ context.Context, id string) error {
	f.mu.Lock()
	f.status[id] = domain.Paused
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) Start(_ context.Context, id string) error {
	f.mu.Lock()
	f.status[id] = domain.Downloading
	f.resumed = append(f.resumed, id)
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) get(id string) domain.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[id]
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTheCapPausesUntilThePeriodEnds(t *testing.T) {
	eng := &fakeEngine{bytes: 1000, status: map[string]domain.Status{
		"a": domain.Downloading, "b": domain.Downloading, "c": domain.Paused, "d": domain.Completed}}
	w := New(eng, nil)
	clock := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }
	var changes []bool
	w.OnChange = func(blocked bool, _ time.Time) { changes = append(changes, blocked) }
	w.Set(200<<20, 5*time.Hour)

	eng.mu.Lock()
	eng.bytes += 199 << 20
	eng.mu.Unlock()
	w.Tick(context.Background())
	if b, _ := w.Status(); b {
		t.Fatal("blocked under the cap")
	}
	eng.mu.Lock()
	eng.bytes += 1 << 20
	eng.mu.Unlock()
	w.Tick(context.Background())
	waitFor(t, func() bool { return eng.get("a") == domain.Paused && eng.get("b") == domain.Paused })
	if b, until := w.Status(); !b || !until.Equal(clock.Add(5*time.Hour)) {
		t.Fatalf("status %v %v", b, until)
	}
	// One started meanwhile is held too.
	eng.mu.Lock()
	eng.status["e"] = domain.Downloading
	eng.mu.Unlock()
	w.Tick(context.Background())
	waitFor(t, func() bool { return eng.get("e") == domain.Paused })

	clock = clock.Add(5 * time.Hour)
	w.Tick(context.Background())
	sort.Strings(eng.resumed)
	if b, _ := w.Status(); b || len(eng.resumed) != 3 || eng.resumed[0] != "a" || eng.resumed[2] != "e" {
		t.Fatalf("after the period: %v resumed %v", b, eng.resumed)
	}
	if eng.get("c") != domain.Paused {
		t.Fatal("a download the user paused was resumed")
	}
	if len(changes) != 2 || !changes[0] || changes[1] {
		t.Fatalf("changes %v", changes)
	}
}

func TestRemovingTheCapResumes(t *testing.T) {
	eng := &fakeEngine{status: map[string]domain.Status{"a": domain.Downloading}}
	w := New(eng, nil)
	w.Set(10, time.Hour)
	eng.mu.Lock()
	eng.bytes = 10
	eng.mu.Unlock()
	w.Tick(context.Background())
	waitFor(t, func() bool { return eng.get("a") == domain.Paused })
	w.Set(0, 0)
	if eng.get("a") != domain.Downloading {
		t.Fatal("not resumed when the cap was removed")
	}
}
