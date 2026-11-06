package engine

import (
	"errors"
	"io"
)

// errRangeOverflow means a writer was asked to write past its range end.
var errRangeOverflow = errors.New("write exceeds allocated byte range")

// rangeWriter writes sequentially into [off, end] of an io.WriterAt and
// refuses any byte beyond end, so a worker can never touch another
// segment's bytes even if the server misbehaves. WriteAt is used instead of
// append so concurrent workers never share a file offset.
type rangeWriter struct {
	w        io.WriterAt
	off, end int64
}

func (w *rangeWriter) Write(p []byte) (int, error) {
	room := w.end - w.off + 1
	if room <= 0 {
		return 0, errRangeOverflow
	}
	overflow := false
	if int64(len(p)) > room {
		p, overflow = p[:room], true
	}
	n, err := w.w.WriteAt(p, w.off)
	w.off += int64(n)
	if err == nil && overflow {
		err = errRangeOverflow
	}
	return n, err
}
