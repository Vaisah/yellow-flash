package engine

import (
	"fmt"
	"sync/atomic"

	domain "github.com/vaisah/internal/domain/download"
)

// segmentState tracks one segment during a transfer. Start, End and Index
// never change; done is the count of contiguous bytes written from Start.
type segmentState struct {
	seg     domain.Segment
	done    atomic.Int64
	retries atomic.Int32
}

func (s *segmentState) next() int64 { return s.seg.Start + s.done.Load() }

func (s *segmentState) complete() bool { return s.done.Load() >= s.seg.Length() }

// snapshot returns the segment as it should be persisted. Callers must only
// persist a snapshot after syncing the file (see checkpoints in worker.go).
func (s *segmentState) snapshot() domain.Segment {
	seg := s.seg
	seg.Downloaded = s.done.Load()
	seg.Retries = int(s.retries.Load())
	seg.Status = domain.SegmentPending
	if seg.Downloaded >= seg.Length() {
		seg.Status = domain.SegmentCompleted
	}
	return seg
}

func newSegmentStates(segs []domain.Segment) []*segmentState {
	out := make([]*segmentState, len(segs))
	for i, seg := range segs {
		st := &segmentState{seg: seg}
		st.done.Store(seg.Downloaded)
		st.retries.Store(int32(seg.Retries))
		out[i] = st
	}
	return out
}

// planSegments decides the segment layout for a transfer. A stored layout
// is reused only if it exactly tiles [0, size) and its progress is
// plausible; per-segment progress is clamped to what the temporary file can
// actually contain. Otherwise a fresh layout is allocated. The boolean
// reports whether stored progress was reused.
func planSegments(downloadID string, size int64, connections int, minSize int64, stored []domain.Segment, fileSize int64) ([]domain.Segment, bool, error) {
	if validLayout(stored, size) {
		out := make([]domain.Segment, len(stored))
		for i, seg := range stored {
			limit := max(0, fileSize-seg.Start)
			seg.Downloaded = min(seg.Downloaded, limit)
			seg.Status = domain.SegmentPending
			if seg.Downloaded == seg.Length() {
				seg.Status = domain.SegmentCompleted
			}
			out[i] = seg
		}
		return out, true, nil
	}
	ranges, err := Allocate(size, connections, minSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]domain.Segment, len(ranges))
	for i, r := range ranges {
		out[i] = domain.Segment{ID: fmt.Sprintf("%s-%d", downloadID, i), DownloadID: downloadID, Index: i, Start: r.Start, End: r.End, Status: domain.SegmentPending}
	}
	return out, false, nil
}

func validLayout(segs []domain.Segment, size int64) bool {
	if len(segs) == 0 || size <= 0 {
		return false
	}
	var next int64
	for i, seg := range segs {
		if seg.Index != i || seg.Start != next || seg.End < seg.Start || seg.Downloaded < 0 || seg.Downloaded > seg.Length() {
			return false
		}
		next = seg.End + 1
	}
	return next == size
}
