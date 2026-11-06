package download

import (
	"context"
	"errors"
	"testing"

	domain "github.com/vaisah/internal/domain/download"
)

type fakePort struct {
	items   map[string]*domain.Download
	calls   []string
	startEr error
}

func newFake() *fakePort { return &fakePort{items: map[string]*domain.Download{}} }

func (f *fakePort) record(op, id string) error {
	f.calls = append(f.calls, op+":"+id)
	if _, ok := f.items[id]; !ok {
		return domain.ErrNotFound
	}
	return nil
}
func (f *fakePort) Recover(context.Context) error  { f.calls = append(f.calls, "recover"); return nil }
func (f *fakePort) Shutdown(context.Context) error { f.calls = append(f.calls, "shutdown"); return nil }
func (f *fakePort) Create(_ context.Context, r domain.CreateRequest) (*domain.Download, error) {
	d := &domain.Download{ID: "id-1", URL: r.URL, Status: domain.Queued}
	f.items[d.ID] = d
	return d, nil
}
func (f *fakePort) Start(_ context.Context, id string) error {
	if f.startEr != nil {
		return f.startEr
	}
	if err := f.record("start", id); err != nil {
		return err
	}
	f.items[id].Status = domain.Downloading
	return nil
}
func (f *fakePort) Inspect(_ context.Context, url string, _ map[string]string) (*domain.LinkInfo, error) {
	return &domain.LinkInfo{URL: url, Filename: "file.bin", Size: -1}, nil
}
func (f *fakePort) Resume(_ context.Context, id string) error { return f.record("resume", id) }
func (f *fakePort) Pause(_ context.Context, id string) error  { return f.record("pause", id) }
func (f *fakePort) Cancel(_ context.Context, id string) error { return f.record("cancel", id) }
func (f *fakePort) Retry(_ context.Context, id string) error  { return f.record("retry", id) }
func (f *fakePort) Delete(_ context.Context, id string) error { return f.record("delete", id) }
func (f *fakePort) Get(_ context.Context, id string) (*domain.Download, error) {
	if d, ok := f.items[id]; ok {
		c := *d
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakePort) List(context.Context) ([]domain.Download, error) {
	out := []domain.Download{}
	for _, d := range f.items {
		out = append(out, *d)
	}
	return out, nil
}

func TestAddCreatesAndStarts(t *testing.T) {
	port := newFake()
	s := New(port)
	d, err := s.Add(context.Background(), domain.CreateRequest{URL: "https://example.test/a"})
	if err != nil || d.Status != domain.Downloading {
		t.Fatalf("add: %+v %v", d, err)
	}
	port.startEr = errors.New("boom")
	d, err = s.Add(context.Background(), domain.CreateRequest{URL: "https://example.test/b"})
	if err == nil || d == nil {
		t.Fatal("start failure must return the created record and an error")
	}
}

func TestValidationAndDelegation(t *testing.T) {
	port := newFake()
	s := New(port)
	ctx := context.Background()
	if _, err := s.Create(ctx, domain.CreateRequest{URL: "  "}); !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("empty URL: %v", err)
	}
	for _, op := range []func(context.Context, string) error{s.Start, s.Resume, s.Pause, s.Cancel, s.Retry, s.Delete} {
		if err := op(ctx, ""); !errors.Is(err, domain.ErrInvalidRequest) {
			t.Fatalf("empty id accepted: %v", err)
		}
	}
	if len(port.calls) != 0 {
		t.Fatalf("invalid calls reached the engine: %v", port.calls)
	}
	_, _ = s.Create(ctx, domain.CreateRequest{URL: "https://example.test/a"})
	for _, op := range []func(context.Context, string) error{s.Resume, s.Pause, s.Cancel, s.Retry, s.Delete} {
		if err := op(ctx, "id-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Pause(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("not found should propagate: %v", err)
	}
}

func TestListByStatusAndPauseAll(t *testing.T) {
	port := newFake()
	port.items["a"] = &domain.Download{ID: "a", Status: domain.Downloading}
	port.items["b"] = &domain.Download{ID: "b", Status: domain.Completed}
	port.items["c"] = &domain.Download{ID: "c", Status: domain.Initializing}
	s := New(port)
	active, err := s.ListByStatus(context.Background(), domain.Downloading, domain.Initializing)
	if err != nil || len(active) != 2 {
		t.Fatalf("active: %+v %v", active, err)
	}
	if err = s.PauseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(port.calls) != 2 {
		t.Fatalf("pause calls: %v", port.calls)
	}
	port.items["p"] = &domain.Download{ID: "p", Status: domain.Paused}
	port.items["q"] = &domain.Download{ID: "q", Status: domain.Queued}
	port.calls = nil
	if err = s.ResumeAll(context.Background()); err != nil || len(port.calls) != 2 {
		t.Fatalf("resume all: %v %v", port.calls, err)
	}
	if _, err = s.Inspect(context.Background(), " ", nil); !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatal("empty inspect URL accepted")
	}
}
