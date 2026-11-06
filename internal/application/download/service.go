// Package download coordinates user-facing download operations. It holds
// use-case orchestration only; transfer mechanics live in the engine.
package download

import (
	"context"
	"errors"
	"fmt"
	"strings"

	domain "github.com/vaisah/internal/domain/download"
)

// Service is the entry point for delivery layers (desktop UI, CLI,
// browser bridge).
type Service struct{ port DownloadPort }

// New returns a Service backed by port.
func New(port DownloadPort) *Service { return &Service{port: port} }

// Recover restores state after a restart or crash. Call once at startup.
func (s *Service) Recover(ctx context.Context) error { return s.port.Recover(ctx) }

// Shutdown pauses active downloads and persists their progress.
func (s *Service) Shutdown(ctx context.Context) error { return s.port.Shutdown(ctx) }

// Create records a new QUEUED download without starting it.
func (s *Service) Create(ctx context.Context, req domain.CreateRequest) (*domain.Download, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, fmt.Errorf("%w: URL is required", domain.ErrInvalidRequest)
	}
	return s.port.Create(ctx, req)
}

// Add creates a download and starts it. If starting fails, the created
// record is still returned together with the error.
func (s *Service) Add(ctx context.Context, req domain.CreateRequest) (*domain.Download, error) {
	d, err := s.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	if err = s.port.Start(ctx, d.ID); err != nil {
		return d, fmt.Errorf("start download: %w", err)
	}
	return s.port.Get(ctx, d.ID)
}

// Start begins a queued, paused or failed download.
func (s *Service) Start(ctx context.Context, id string) error { return s.call(ctx, id, s.port.Start) }

// Resume continues a paused download after revalidating remote metadata.
func (s *Service) Resume(ctx context.Context, id string) error { return s.call(ctx, id, s.port.Resume) }

// Pause stops a download, keeping durable progress.
func (s *Service) Pause(ctx context.Context, id string) error { return s.call(ctx, id, s.port.Pause) }

// Cancel stops a download and discards its partial data.
func (s *Service) Cancel(ctx context.Context, id string) error { return s.call(ctx, id, s.port.Cancel) }

// Retry restarts a failed download.
func (s *Service) Retry(ctx context.Context, id string) error { return s.call(ctx, id, s.port.Retry) }

// Delete removes an inactive download record. Completed files are kept.
func (s *Service) Delete(ctx context.Context, id string) error { return s.call(ctx, id, s.port.Delete) }

// Get returns one download.
func (s *Service) Get(ctx context.Context, id string) (*domain.Download, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return s.port.Get(ctx, id)
}

// List returns all downloads, newest first.
func (s *Service) List(ctx context.Context) ([]domain.Download, error) { return s.port.List(ctx) }

// ListByStatus returns downloads in any of the given states (for views such
// as active, queued, completed and failed).
func (s *Service) ListByStatus(ctx context.Context, statuses ...domain.Status) ([]domain.Download, error) {
	all, err := s.port.List(ctx)
	if err != nil {
		return nil, err
	}
	want := make(map[domain.Status]bool, len(statuses))
	for _, st := range statuses {
		want[st] = true
	}
	out := make([]domain.Download, 0, len(all))
	for _, d := range all {
		if want[d.Status] {
			out = append(out, d)
		}
	}
	return out, nil
}

// PauseAll pauses every active download, returning the joined errors.
func (s *Service) PauseAll(ctx context.Context) error {
	active, err := s.ListByStatus(ctx, domain.Initializing, domain.Downloading)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range active {
		if err := s.port.Pause(ctx, d.ID); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
			errs = append(errs, fmt.Errorf("pause %s: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) call(ctx context.Context, id string, fn func(context.Context, string) error) error {
	if err := validID(id); err != nil {
		return err
	}
	return fn(ctx, id)
}

func validID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: download id is required", domain.ErrInvalidRequest)
	}
	return nil
}
