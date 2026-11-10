package engine

import (
	"math/rand/v2"
	"testing"

	domain "github.com/vaisah/internal/domain/download"
)

func fresh(size int64) []domain.Segment {
	return []domain.Segment{{Start: 0, End: size - 1}}
}

func TestPlannerSplitsLargestRemainderInHalf(t *testing.T) {
	const size = 1 << 20
	p := newPlanner(fresh(size), 4096)
	a := p.next(0)
	if a == nil || a.start != 0 || a.end != size {
		t.Fatalf("first worker should own the file: %+v", a)
	}
	b := p.next(1)
	if b == nil || b.start != size/2 || a.end != size/2 {
		t.Fatalf("second worker should take the upper half: a=%+v b=%+v", a, b)
	}
	// Worker 0 has written 100 KiB; a split is measured from its claim.
	if got := p.claim(a, 100<<10); got != 100<<10 {
		t.Fatalf("claim %d", got)
	}
	p.commit(a, 100<<10)
	// The largest unclaimed remainder is now worker 1's untouched half.
	c := p.next(2)
	if c == nil || c.start != size/2+size/4 || b.end != c.start {
		t.Fatalf("third split should halve worker 1's part: b=%+v c=%+v", b, c)
	}
	// Next, worker 0's remainder (412 KiB) beats the 256 KiB quarters; the
	// split point is aligned.
	d := p.next(3)
	if d == nil || d.start%splitAlign != 0 || d.start <= 100<<10 || d.start >= size/2 || a.end != d.start {
		t.Fatalf("fourth split should halve worker 0's remainder: a=%+v d=%+v", a, d)
	}
	if err := p.check(size); err != nil {
		t.Fatal(err)
	}
	// A worker whose part shrank is stopped exactly at the new end.
	left := a.end - a.reserved
	if got := p.claim(a, left+500); got != left {
		t.Fatalf("claim past end allowed %d of %d", got, left)
	}
	if got := p.claim(a, 1); got != 0 {
		t.Fatal("claim beyond a finished part")
	}
}

func TestPlannerRespectsMinimumSplit(t *testing.T) {
	p := newPlanner(fresh(300<<10), 256<<10)
	if p.next(0) == nil {
		t.Fatal("first worker must get the file")
	}
	if p.next(1) != nil {
		t.Fatal("a 300 KiB file must not be split into 256 KiB minimum halves")
	}
	if len(p.snapshot("d")) != 1 {
		t.Fatal("layout changed")
	}
}

func TestPlannerResumeAndRelease(t *testing.T) {
	segs := []domain.Segment{{Start: 0, End: 99, Downloaded: 100}, {Start: 100, End: 199, Downloaded: 40}}
	p := newPlanner(segs, 1)
	pt := p.next(0)
	if pt == nil || pt.start != 100 || pt.cursor != 140 {
		t.Fatalf("should resume the unfinished part at its cursor: %+v", pt)
	}
	p.claim(pt, 10)
	p.release(pt) // throttled: given back without committing the claim
	again := p.next(1)
	if again != pt || again.cursor != 140 || again.reserved != 140 {
		t.Fatalf("released part not handed over cleanly: %+v", again)
	}
	if p.written() != 140 {
		t.Fatalf("written %d", p.written())
	}
}

// TestPlannerRandomSimulation drives many workers through random claims,
// commits, releases and abandons, and checks that every byte is written
// exactly once and the parts always tile the file.
func TestPlannerRandomSimulation(t *testing.T) {
	for seed := uint64(1); seed <= 200; seed++ {
		r := rand.New(rand.NewPCG(seed, seed*7))
		size := int64(1 + r.IntN(3<<20))
		minSplit := int64(1 + r.IntN(64<<10))
		p := newPlanner(fresh(size), minSplit)
		written := make([]bool, size)
		workers := 1 + r.IntN(16)
		own := make([]*part, workers)
		for steps := 0; !p.finished(); steps++ {
			if steps > 2_000_000 {
				t.Fatalf("seed %d: no progress", seed)
			}
			w := r.IntN(workers)
			if own[w] == nil || p.done(own[w]) {
				own[w] = p.next(w)
				if own[w] == nil {
					continue
				}
			}
			pt := own[w]
			switch r.IntN(20) {
			case 0:
				p.release(pt)
				own[w] = nil
				continue
			case 1:
				p.claim(pt, int64(1+r.IntN(1000)))
				p.abandon(pt)
				continue
			}
			from, _ := p.span(pt)
			n := p.claim(pt, int64(1+r.IntN(64<<10)))
			for i := from; i < from+n; i++ {
				if written[i] {
					t.Fatalf("seed %d: byte %d written twice", seed, i)
				}
				written[i] = true
			}
			p.commit(pt, n)
			if err := p.check(size); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
		}
		for i, ok := range written {
			if !ok {
				t.Fatalf("seed %d: byte %d never written", seed, i)
			}
		}
		if p.written() != size {
			t.Fatalf("seed %d: written %d of %d", seed, p.written(), size)
		}
	}
}
