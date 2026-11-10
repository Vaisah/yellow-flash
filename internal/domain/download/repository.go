package download

import (
	"context"
	"time"
)

// HostLimit is the number of connections a server was learned to allow.
type HostLimit struct {
	Host           string
	MaxConnections int
	// Seen counts the refusals the limit was learned from.
	Seen      int
	UpdatedAt time.Time
}

// HostLimitStore persists learned per-host connection limits, so a restart
// does not have to learn them again.
type HostLimitStore interface {
	LoadHostLimits(ctx context.Context) ([]HostLimit, error)
	SaveHostLimit(ctx context.Context, l HostLimit) error
}

// Repository persists download records. Get returns an error wrapping
// ErrNotFound when the record does not exist.
type Repository interface {
	Save(ctx context.Context, d *Download) error
	Get(ctx context.Context, id string) (*Download, error)
	List(ctx context.Context) ([]Download, error)
	Delete(ctx context.Context, id string) error
}

// SegmentRepository persists byte-range progress so interrupted downloads can
// resume without trusting bytes that were never synced to disk.
type SegmentRepository interface {
	// SaveSegments upserts the given segments atomically.
	SaveSegments(ctx context.Context, downloadID string, segments []Segment) error
	ListSegments(ctx context.Context, downloadID string) ([]Segment, error)
	DeleteSegments(ctx context.Context, downloadID string) error
}

// Store is the persistence port required by the engine.
type Store interface {
	Repository
	SegmentRepository
}
