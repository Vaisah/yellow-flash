package engine

import (
	"context"
	"errors"
	"os"

	domain "github.com/vaisah/internal/domain/download"
)

// durableOffset is the single-stream resume point: never beyond what was
// persisted (which was synced first), the file length, or the resource.
func durableOffset(persisted, fileSize, remoteSize int64) int64 {
	off := max(0, min(persisted, fileSize))
	if remoteSize >= 0 && off > remoteSize {
		return 0
	}
	return off
}

// resetProgress discards all partial data so the next transfer starts from
// zero. It is used when the remote representation changed.
func (m *Manager) resetProgress(ctx context.Context, e *entry) error {
	m.mu.Lock()
	temp, id := e.d.TempPath, e.d.ID
	e.d.Downloaded = 0
	m.mu.Unlock()
	if err := os.Remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return m.store.DeleteSegments(ctx, id)
}

// reconcile aligns a persisted download with what is actually on disk after
// a restart or crash, reporting whether anything changed. Segment progress
// is clamped to the temporary file's length; a missing temporary file means
// nothing survived.
func (m *Manager) reconcile(ctx context.Context, d *domain.Download) (bool, error) {
	if d.Status == domain.Queued || d.Status.IsTerminal() {
		return false, nil
	}
	before := d.Downloaded
	info, err := os.Stat(d.TempPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		d.Downloaded = 0
		return before != 0, m.store.DeleteSegments(ctx, d.ID)
	}
	segs, err := m.store.ListSegments(ctx, d.ID)
	if err != nil {
		return false, err
	}
	switch {
	case len(segs) > 0 && validLayout(segs, d.Size):
		planned, _, err := planSegments(d.ID, d.Size, d.Connections, m.cfg.MinSegmentSize, segs, info.Size())
		if err != nil {
			return false, err
		}
		var sum int64
		for _, s := range planned {
			sum += s.Downloaded
		}
		if err = m.store.SaveSegments(ctx, d.ID, planned); err != nil {
			return false, err
		}
		d.Downloaded = sum
	case len(segs) > 0:
		if err = m.store.DeleteSegments(ctx, d.ID); err != nil {
			return false, err
		}
		d.Downloaded = 0
	case d.RangeSupported && d.Size > 0:
		// Segmented transfer without durable records: nothing is provable.
		d.Downloaded = 0
	default:
		d.Downloaded = durableOffset(d.Downloaded, info.Size(), d.Size)
	}
	return d.Downloaded != before, nil
}
