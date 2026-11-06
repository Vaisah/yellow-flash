package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/infrastructure/network"
)

// readBufferSize bounds per-worker memory regardless of file size.
const readBufferSize = 64 << 10

// transferSegmented downloads all incomplete segments with a bounded pool
// of workers writing at their own offsets. The first permanent failure
// cancels the remaining workers. Progress is checkpointed periodically and
// on every segment completion: the file is synced first, then the counts
// that were observed before the sync are persisted, so recorded progress
// never exceeds durable data.
func (m *Manager) transferSegmented(ctx context.Context, e *entry, d *domain.Download, meta *domain.RemoteMetadata, headers http.Header) error {
	f, err := filesystem.OpenTemp(d.TempPath)
	if err != nil {
		return fmt.Errorf("open temporary file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stored, err := m.store.ListSegments(ctx, d.ID)
	if err != nil {
		return err
	}
	segs, reused, err := planSegments(d.ID, meta.Size, d.Connections, m.cfg.MinSegmentSize, stored, info.Size())
	if err != nil {
		return err
	}
	if !reused && len(stored) > 0 {
		if err = m.store.DeleteSegments(ctx, d.ID); err != nil {
			return err
		}
	}
	if err = filesystem.Preallocate(f, meta.Size); err != nil {
		return fmt.Errorf("preallocate: %w", err)
	}
	if err = m.store.SaveSegments(ctx, d.ID, segs); err != nil {
		return err
	}
	states := newSegmentStates(segs)
	var base int64
	var pending []*segmentState
	for _, st := range states {
		base += st.done.Load()
		if !st.complete() {
			pending = append(pending, st)
		}
	}
	tr := newTracker(meta.Size, base, time.Now())
	m.setTracker(e, tr)
	defer m.setTracker(e, nil)

	persistCtx := context.WithoutCancel(ctx)
	checkpoint := func() error {
		snaps := make([]domain.Segment, len(states))
		var sum int64
		for i, st := range states {
			snaps[i] = st.snapshot()
			sum += snaps[i].Downloaded
		}
		if err := f.Sync(); err != nil {
			return fmt.Errorf("sync: %w", err)
		}
		if err := m.store.SaveSegments(persistCtx, d.ID, snaps); err != nil {
			return err
		}
		return m.save(persistCtx, m.update(e, func(d *domain.Download) { d.Downloaded = sum }))
	}

	wctx, cancelW := context.WithCancelCause(ctx)
	defer cancelW(nil)
	mon := m.startMonitor(e, tr, checkpoint, func(err error) { cancelW(err) })
	g, gctx := errgroup.WithContext(wctx)
	jobs := make(chan *segmentState)
	g.Go(func() error {
		defer close(jobs)
		for _, st := range pending {
			select {
			case jobs <- st:
			case <-gctx.Done():
				return nil
			}
		}
		return nil
	})
	workers := min(d.Connections, m.cfg.MaxConnections, len(pending))
	for i := 0; i < workers; i++ {
		g.Go(func() error {
			buf := make([]byte, readBufferSize)
			for st := range jobs {
				if err := m.fetchSegment(gctx, e, f, st, meta, headers, tr, buf); err != nil {
					return fmt.Errorf("segment %d: %w", st.seg.Index, err)
				}
				mon.notify()
			}
			return nil
		})
	}
	err = g.Wait()
	mon.close()
	if cause := context.Cause(wctx); err != nil && cause != nil && !errors.Is(cause, context.Canceled) {
		err = cause
	}
	ckErr := checkpoint()
	switch {
	case err != nil:
		return err
	case ctx.Err() != nil:
		return ctx.Err()
	case ckErr != nil:
		return ckErr
	}
	for _, st := range states {
		if !st.complete() {
			return fmt.Errorf("segment %d incomplete", st.seg.Index)
		}
	}
	return nil
}

func (m *Manager) fetchSegment(ctx context.Context, e *entry, f *os.File, st *segmentState, meta *domain.RemoteMetadata, headers http.Header, tr *tracker, buf []byte) error {
	return m.withRetry(ctx, e, func() { st.retries.Add(1) }, func() (bool, error) {
		before := st.done.Load()
		err := m.fetchRange(ctx, f, st, meta, headers, tr, buf)
		return st.done.Load() > before, err
	})
}

// fetchRange requests the rest of one segment and writes it at its offset.
func (m *Manager) fetchRange(ctx context.Context, f *os.File, st *segmentState, meta *domain.RemoteMetadata, headers http.Header, tr *tracker, buf []byte) error {
	start, end := st.next(), st.seg.End
	if start > end {
		return nil
	}
	reqCtx, cancelReq := context.WithCancelCause(ctx)
	defer cancelReq(nil)
	req, err := network.NewRequest(reqCtx, http.MethodGet, meta.URL, headers, network.RangeHeader(start, end))
	if err != nil {
		return err
	}
	validator := ifRangeValidator(meta.ETag, meta.LastModified)
	if validator != "" {
		req.Header.Set("If-Range", validator)
	}
	tr.active.Add(1)
	defer tr.active.Add(-1)
	res, err := m.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusPartialContent:
		cr, err := network.ParseContentRange(res.Header.Get("Content-Range"))
		if err != nil {
			return err
		}
		if cr.Total >= 0 && cr.Total != meta.Size {
			return fmt.Errorf("%w: size is now %d", domain.ErrRemoteChanged, cr.Total)
		}
		if cr.Start != start || cr.End != end {
			return fmt.Errorf("%w: got bytes %d-%d, requested %d-%d", errContentRangeMismatch, cr.Start, cr.End, start, end)
		}
	case http.StatusOK:
		if changedValidators(res.Header, meta) {
			return fmt.Errorf("%w: full response with new validators", domain.ErrRemoteChanged)
		}
		return errRangeIgnored
	default:
		return network.NewStatusError(res)
	}
	body := newIdleReader(res.Body, m.cfg.ReadIdleTimeout, func() { cancelReq(errIdleTimeout) })
	defer body.stop()
	w := &rangeWriter{w: f, off: start, end: end}
	for remaining := end - start + 1; remaining > 0; {
		n, rerr := body.Read(buf[:min(int64(len(buf)), remaining)])
		if n > 0 {
			wn, werr := w.Write(buf[:n])
			st.done.Add(int64(wn))
			tr.add(int64(wn))
			remaining -= int64(wn)
			if werr != nil {
				return fmt.Errorf("write: %w", werr)
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				if remaining > 0 {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			return bodyError(reqCtx, rerr)
		}
	}
	return nil
}

func changedValidators(h http.Header, meta *domain.RemoteMetadata) bool {
	if etag := h.Get("ETag"); etag != "" && meta.ETag != "" && etag != meta.ETag {
		return true
	}
	lm := h.Get("Last-Modified")
	return lm != "" && meta.LastModified != "" && lm != meta.LastModified
}

// transferStream downloads with one connection. It resumes with an open
// range only when the server proved range support; otherwise every retry
// restarts from zero. Restarts and checkpoints are serialised so a crash
// can never leave a persisted offset pointing at unsynced data.
func (m *Manager) transferStream(ctx context.Context, e *entry, d *domain.Download, meta *domain.RemoteMetadata, headers http.Header) error {
	f, err := filesystem.OpenTemp(d.TempPath)
	if err != nil {
		return fmt.Errorf("open temporary file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var offset int64
	if meta.RangeSupported {
		offset = durableOffset(d.Downloaded, info.Size(), meta.Size)
	}
	if err = f.Truncate(offset); err != nil {
		return err
	}
	tr := newTracker(meta.Size, offset, time.Now())
	m.setTracker(e, tr)
	defer m.setTracker(e, nil)

	persistCtx := context.WithoutCancel(ctx)
	var fileMu sync.Mutex
	checkpoint := func() error {
		fileMu.Lock()
		defer fileMu.Unlock()
		off := tr.bytes()
		if err := f.Sync(); err != nil {
			return fmt.Errorf("sync: %w", err)
		}
		return m.save(persistCtx, m.update(e, func(d *domain.Download) { d.Downloaded = off }))
	}
	restart := func(adopt func(*domain.Download)) error {
		fileMu.Lock()
		defer fileMu.Unlock()
		tr.reset(0, time.Now())
		tr.total.Store(meta.Size)
		if err := f.Truncate(0); err != nil {
			return err
		}
		return m.save(persistCtx, m.update(e, func(d *domain.Download) {
			d.Downloaded = 0
			if adopt != nil {
				adopt(d)
			}
		}))
	}

	wctx, cancelW := context.WithCancelCause(ctx)
	defer cancelW(nil)
	mon := m.startMonitor(e, tr, checkpoint, func(err error) { cancelW(err) })
	buf := make([]byte, readBufferSize)
	err = m.withRetry(wctx, e, nil, func() (bool, error) {
		before := tr.bytes()
		err := m.fetchStream(wctx, f, meta, headers, tr, buf, restart)
		return tr.bytes() > before, err
	})
	mon.close()
	if cause := context.Cause(wctx); err != nil && cause != nil && !errors.Is(cause, context.Canceled) {
		err = cause
	}
	ckErr := checkpoint()
	switch {
	case err != nil:
		return err
	case ctx.Err() != nil:
		return ctx.Err()
	}
	return ckErr
}

// fetchStream performs one single-connection request from the current
// offset. meta is updated when the server reveals a new length.
func (m *Manager) fetchStream(ctx context.Context, f *os.File, meta *domain.RemoteMetadata, headers http.Header, tr *tracker, buf []byte, restart func(func(*domain.Download)) error) error {
	offset := tr.bytes()
	if offset > 0 && !meta.RangeSupported {
		if err := restart(nil); err != nil {
			return err
		}
		offset = 0
	}
	if meta.Size >= 0 && offset >= meta.Size {
		return nil
	}
	reqCtx, cancelReq := context.WithCancelCause(ctx)
	defer cancelReq(nil)
	rangeHeader := ""
	if offset > 0 {
		rangeHeader = network.RangeHeader(offset, -1)
	}
	req, err := network.NewRequest(reqCtx, http.MethodGet, meta.URL, headers, rangeHeader)
	if err != nil {
		return err
	}
	if offset > 0 {
		if v := ifRangeValidator(meta.ETag, meta.LastModified); v != "" {
			req.Header.Set("If-Range", v)
		}
	}
	tr.active.Add(1)
	defer tr.active.Add(-1)
	res, err := m.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		switch {
		case offset > 0:
			// If-Range failed or Range was ignored: this body replaces
			// everything, so discard the old bytes and adopt its metadata.
			meta.Size = res.ContentLength
			if v := res.Header.Get("ETag"); v != "" {
				meta.ETag = v
			}
			if v := res.Header.Get("Last-Modified"); v != "" {
				meta.LastModified = v
			}
			meta.ChecksumAlgorithm, meta.Checksum = network.ChecksumFromHeaders(res.Header, true)
			size, etag, lm := meta.Size, meta.ETag, meta.LastModified
			if err := restart(func(d *domain.Download) { d.Size, d.ETag, d.LastModified = size, etag, lm }); err != nil {
				return err
			}
			offset = 0
		case meta.Size >= 0 && res.ContentLength >= 0 && res.ContentLength != meta.Size:
			return fmt.Errorf("%w: length is now %d", domain.ErrRemoteChanged, res.ContentLength)
		case meta.Size < 0 && res.ContentLength >= 0:
			meta.Size = res.ContentLength
			tr.total.Store(meta.Size)
		}
	case http.StatusPartialContent:
		cr, err := network.ParseContentRange(res.Header.Get("Content-Range"))
		if err != nil {
			return err
		}
		if cr.Start != offset {
			return fmt.Errorf("%w: got offset %d, requested %d", errContentRangeMismatch, cr.Start, offset)
		}
		if cr.Total >= 0 && meta.Size >= 0 && cr.Total != meta.Size {
			return fmt.Errorf("%w: size is now %d", domain.ErrRemoteChanged, cr.Total)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// Resuming exactly at the end of an unknown-length resource.
		if total, ok := network.ParseUnsatisfiedRange(res.Header.Get("Content-Range")); ok && offset > 0 && total == offset {
			meta.Size = offset
			return nil
		}
		return network.NewStatusError(res)
	default:
		return network.NewStatusError(res)
	}
	body := newIdleReader(res.Body, m.cfg.ReadIdleTimeout, func() { cancelReq(errIdleTimeout) })
	defer body.stop()
	limit := int64(-1)
	if meta.Size >= 0 {
		limit = meta.Size - offset
	}
	for off := offset; limit != 0; {
		want := int64(len(buf))
		if limit > 0 {
			want = min(want, limit)
		}
		n, rerr := body.Read(buf[:want])
		if n > 0 {
			if _, werr := f.WriteAt(buf[:n], off); werr != nil {
				return fmt.Errorf("write: %w", werr)
			}
			off += int64(n)
			tr.add(int64(n))
			if limit > 0 {
				limit -= int64(n)
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				if limit > 0 {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			return bodyError(reqCtx, rerr)
		}
	}
	return nil
}

// monitor emits throttled progress and triggers periodic checkpoints.
type monitor struct {
	stop, done chan struct{}
	signal     chan struct{}
}

func (m *Manager) startMonitor(e *entry, tr *tracker, checkpoint func() error, fail func(error)) *monitor {
	mon := &monitor{stop: make(chan struct{}), done: make(chan struct{}), signal: make(chan struct{}, 1)}
	go func() {
		defer close(mon.done)
		progress := time.NewTicker(m.cfg.ProgressInterval)
		defer progress.Stop()
		persist := time.NewTicker(m.cfg.PersistInterval)
		defer persist.Stop()
		for {
			var err error
			select {
			case <-mon.stop:
				return
			case <-progress.C:
				m.emitProgress(e, tr)
			case <-persist.C:
				err = checkpoint()
			case <-mon.signal:
				err = checkpoint()
			}
			if err != nil {
				fail(err)
				return
			}
		}
	}()
	return mon
}

// notify requests a checkpoint without blocking.
func (mon *monitor) notify() {
	select {
	case mon.signal <- struct{}{}:
	default:
	}
}

func (mon *monitor) close() {
	close(mon.stop)
	<-mon.done
}

// idleReader cancels the request when no data arrives for timeout.
type idleReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func newIdleReader(r io.Reader, timeout time.Duration, onIdle func()) *idleReader {
	return &idleReader{r: r, timeout: timeout, timer: time.AfterFunc(timeout, onIdle)}
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.timeout)
	}
	return n, err
}

func (i *idleReader) stop() { i.timer.Stop() }

// bodyError surfaces idle timeouts that were reported as cancellations.
func bodyError(reqCtx context.Context, err error) error {
	if errors.Is(context.Cause(reqCtx), errIdleTimeout) {
		return errIdleTimeout
	}
	return err
}
