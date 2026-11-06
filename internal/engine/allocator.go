package engine

import "fmt"

// ByteRange is an inclusive [Start, End] byte range, matching HTTP Range
// semantics.
type ByteRange struct {
	Start, End int64
}

// Length is the number of bytes in the range.
func (r ByteRange) Length() int64 { return r.End - r.Start + 1 }

// Allocate splits [0, size) into at most n contiguous, non-overlapping
// inclusive ranges, none smaller than minSize except when size itself is
// smaller. Remainder bytes go to the first ranges, so lengths differ by at
// most one. All arithmetic stays within [0, size], so it cannot overflow.
func Allocate(size int64, n int, minSize int64) ([]ByteRange, error) {
	if size < 0 || n < 1 {
		return nil, fmt.Errorf("allocate: invalid size %d or count %d", size, n)
	}
	if size == 0 {
		return nil, nil
	}
	if minSize < 1 {
		minSize = 1
	}
	parts := int64(n)
	if maxParts := (size-1)/minSize + 1; parts > maxParts {
		parts = maxParts
	}
	q, r := size/parts, size%parts
	out := make([]ByteRange, 0, parts)
	var start int64
	for i := int64(0); i < parts; i++ {
		length := q
		if i < r {
			length++
		}
		out = append(out, ByteRange{Start: start, End: start + length - 1})
		start += length
	}
	return out, nil
}
