package engine

import (
	"fmt"
	"sort"
	"sync"

	domain "github.com/vaisah/internal/domain/download"
)

// splitAlign rounds split points down to a filesystem-friendly boundary
// whenever that keeps both halves at least minSplit long.
const splitAlign = 4096

// part is one contiguous byte range [start, end) of the file.
//
//	[start, cursor)    written to disk (committed)
//	[cursor, reserved) claimed by its owner and being written
//	[reserved, end)    not yet claimed; a split may take the upper half
type part struct {
	start, end       int64
	cursor, reserved int64
	owner            int // worker id, or -1 when unassigned
	retries          int
}

// planner hands out work and rebalances it while a download runs. A free
// connection first takes an unassigned unfinished part (after a resume, or
// one a throttled connection gave back); otherwise it splits the largest
// unclaimed remainder of a running part and takes the upper half. Workers
// claim bytes before writing and commit after; splits are measured from the
// claimed position, so a split can never cut into bytes being written, and
// a worker can never write past its part's (possibly shrunken) end.
type planner struct {
	mu       sync.Mutex
	parts    []*part // ordered by start; together they tile [0, size)
	minSplit int64
}

// newPlanner builds a planner from a segment layout (inclusive End). The
// layout must tile the file (see validLayout).
func newPlanner(segs []domain.Segment, minSplit int64) *planner {
	if minSplit < 1 {
		minSplit = 1
	}
	p := &planner{minSplit: minSplit, parts: make([]*part, 0, len(segs))}
	for _, s := range segs {
		cur := s.Start + s.Downloaded
		p.parts = append(p.parts, &part{start: s.Start, end: s.End + 1, cursor: cur, reserved: cur, owner: -1, retries: s.Retries})
	}
	return p
}

// next gives worker a part to download, or nil when there is nothing left
// worth taking (every remainder is claimed or too small to split).
func (p *planner) next(worker int) *part {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pt := range p.parts {
		if pt.owner < 0 && pt.cursor < pt.end {
			pt.owner, pt.reserved = worker, pt.cursor
			return pt
		}
	}
	var best *part
	var bestFree int64
	bestIdx := -1
	for i, pt := range p.parts {
		if pt.owner < 0 {
			continue
		}
		if free := pt.end - pt.reserved; free > bestFree {
			best, bestFree, bestIdx = pt, free, i
		}
	}
	if best == nil || bestFree < 2*p.minSplit {
		return nil
	}
	mid := best.reserved + bestFree/2
	if aligned := mid - mid%splitAlign; aligned-best.reserved >= p.minSplit {
		mid = aligned
	}
	np := &part{start: mid, end: best.end, cursor: mid, reserved: mid, owner: worker}
	best.end = mid
	p.parts = append(p.parts, nil)
	copy(p.parts[bestIdx+2:], p.parts[bestIdx+1:])
	p.parts[bestIdx+1] = np
	return np
}

// span returns where pt's owner should fetch from and the current
// exclusive end (which may shrink later).
func (p *planner) span(pt *part) (from, end int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pt.cursor, pt.end
}

// claim reserves up to n bytes at pt's write position and returns how many
// may be written; fewer than n means the part ended (it was split).
func (p *planner) claim(pt *part, n int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	allowed := min(n, pt.end-pt.reserved)
	if allowed <= 0 {
		return 0
	}
	pt.reserved += allowed
	return allowed
}

// commit records n claimed bytes as written.
func (p *planner) commit(pt *part, n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pt.cursor += n
}

// abandon drops claims that were not written (a write or read failed).
func (p *planner) abandon(pt *part) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pt.reserved = pt.cursor
}

// release gives pt back so another worker can continue it.
func (p *planner) release(pt *part) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pt.reserved, pt.owner = pt.cursor, -1
}

// retried counts a retry of pt for the persisted segment record.
func (p *planner) retried(pt *part) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pt.retries++
}

// done reports whether pt has been fully written.
func (p *planner) done(pt *part) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pt.cursor >= pt.end
}

// finished reports whether every byte of the file has been written.
func (p *planner) finished() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pt := range p.parts {
		if pt.cursor < pt.end {
			return false
		}
	}
	return true
}

// written returns the number of committed bytes.
func (p *planner) written() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int64
	for _, pt := range p.parts {
		n += pt.cursor - pt.start
	}
	return n
}

// snapshot returns the layout as persistable segments. Only committed
// bytes count, so persisting it after a sync never claims unwritten data.
func (p *planner) snapshot(downloadID string) []domain.Segment {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]domain.Segment, len(p.parts))
	for i, pt := range p.parts {
		status := domain.SegmentPending
		if pt.cursor >= pt.end {
			status = domain.SegmentCompleted
		}
		out[i] = domain.Segment{ID: fmt.Sprintf("%s-%d", downloadID, i), DownloadID: downloadID, Index: i,
			Start: pt.start, End: pt.end - 1, Downloaded: pt.cursor - pt.start, Status: status, Retries: pt.retries}
	}
	return out
}

// check verifies the tiling invariant; used by tests.
func (p *planner) check(size int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !sort.SliceIsSorted(p.parts, func(i, j int) bool { return p.parts[i].start < p.parts[j].start }) {
		return fmt.Errorf("parts out of order")
	}
	var next int64
	for i, pt := range p.parts {
		if pt.start != next || pt.end < pt.start || pt.cursor < pt.start || pt.reserved < pt.cursor || pt.reserved > pt.end {
			return fmt.Errorf("part %d broken: %+v (expected start %d)", i, *pt, next)
		}
		next = pt.end
	}
	if next != size {
		return fmt.Errorf("parts cover %d of %d bytes", next, size)
	}
	return nil
}
