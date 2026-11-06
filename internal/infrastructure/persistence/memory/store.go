// Package memory is a non-durable domain.Store for tests and for running the
// engine without a database.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	domain "github.com/vaisah/internal/domain/download"
)

// Store keeps downloads and segments in memory. It is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	downloads map[string]*domain.Download
	segments  map[string]map[int]domain.Segment
}

// New returns an empty Store.
func New() *Store {
	return &Store{downloads: map[string]*domain.Download{}, segments: map[string]map[int]domain.Segment{}}
}

// Save upserts a copy of d.
func (s *Store) Save(_ context.Context, d *domain.Download) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.downloads[d.ID] = d.Clone()
	return nil
}

// Get returns a copy of the download or an error wrapping ErrNotFound.
func (s *Store) Get(_ context.Context, id string) (*domain.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.downloads[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, id)
	}
	return d.Clone(), nil
}

// List returns all downloads, newest first.
func (s *Store) List(_ context.Context) ([]domain.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Download, 0, len(s.downloads))
	for _, d := range s.downloads {
		out = append(out, *d.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Delete removes a download and its segments.
func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.downloads, id)
	delete(s.segments, id)
	return nil
}

// SaveSegments upserts segments by index.
func (s *Store) SaveSegments(_ context.Context, downloadID string, segments []domain.Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.downloads[downloadID]; !ok {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, downloadID)
	}
	m := s.segments[downloadID]
	if m == nil {
		m = map[int]domain.Segment{}
		s.segments[downloadID] = m
	}
	for _, seg := range segments {
		m[seg.Index] = seg
	}
	return nil
}

// ListSegments returns segments ordered by index.
func (s *Store) ListSegments(_ context.Context, downloadID string) ([]domain.Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Segment, 0, len(s.segments[downloadID]))
	for _, seg := range s.segments[downloadID] {
		out = append(out, seg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// DeleteSegments removes all segments of a download.
func (s *Store) DeleteSegments(_ context.Context, downloadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.segments, downloadID)
	return nil
}
