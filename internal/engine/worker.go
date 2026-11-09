package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/platform"
)

// readBufferSize bounds per-worker memory regardless of file size.
const readBufferSize = 64 << 10

// errYielded means a worker gave its part back instead of retrying.
var errYielded = errors.New("worker yielded its part")

// opened is a response already in flight (a download's first request),
// with the cancel function of the context it was made with.
type opened struct {
	res    *http.Response
	cancel context.CancelCauseFunc
}

func (o *opened) close() {
	if o != nil {
		_ = o.res.Body.Close()
		o.cancel(nil)
	}
}

// adopt binds the response to the consuming worker's ctx: it was made under
// the download's context, so without this a worker whose group is cancelled
// would keep reading it until the idle timeout. The returned func releases
// the binding and closes the response.
func (o *opened) adopt(ctx context.Context) func() {
	stop := context.AfterFunc(ctx, func() { o.cancel(context.Cause(ctx)) })
	return func() {
		stop()
		o.close()
	}
}

// isThrottle reports a server signalling too many connections.
func isThrottle(c Classification) bool {
	return c.Kind == KindHTTP && (c.StatusCode == http.StatusTooManyRequests || c.StatusCode == http.StatusServiceUnavailable)
}

// transferSegmented downloads the file over several connections driven by
// the planner: connection 0 continues the first request (when given) and
// every other connection takes half of the largest remaining part, so no
// connection idles while work remains. Progress is checkpointed
// periodically and whenever a part completes: the file is synced first,
// then the committed counts are persisted, so recorded progress never
// exceeds durable data.
func (m *Manager) transferSegmented(ctx context.Context, e *entry, d *domain.Download, meta *domain.RemoteMetadata, headers http.Header, first *opened) error {
	f, err := filesystem.OpenTemp(d.TempPath)
	if err != nil {
		first.close()
		return fmt.Errorf("open temporary file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		first.close()
		return err
	}
	stored, err := m.store.ListSegments(ctx, d.ID)
	if err != nil {
		first.close()
		return err
	}
	// A fresh download starts as a single part; connections split it.
	segs, reused, err := planSegments(d.ID, meta.Size, 1, m.cfg.MinSegmentSize, stored, info.Size())
	if err != nil {
		first.close()
		return err
	}
	if !reused && len(stored) > 0 {
		if err = m.store.DeleteSegments(ctx, d.ID); err != nil {
			first.close()
			return err
		}
	}
	pl := newPlanner(segs, m.cfg.MinSegmentSize)
	if err = checkSpace(d.Destination, meta.Size, meta.Size-pl.written()); err != nil {
		first.close()
		return err
	}
	// A fresh large file is made sparse before it is extended, so writing
	// far into it does not make NTFS zero-fill everything before.
	if info.Size() < meta.Size && meta.Size >= platform.SparseMinSize && pl.written() == 0 {
		platform.SetSparse(f, true)
	}
	if err = filesystem.Preallocate(f, meta.Size); err != nil {
		first.close()
		return fmt.Errorf("preallocate: %w", err)
	}
	if err = m.store.SaveSegments(ctx, d.ID, pl.snapshot(d.ID)); err != nil {
		first.close()
		return err
	}
	tr := newTracker(meta.Size, pl.written(), time.Now())
	m.setTracker(e, tr)
	defer m.setTracker(e, nil)

	persistCtx := context.WithoutCancel(ctx)
	checkpoint := func() error {
		snaps := pl.snapshot(d.ID)
		var sum int64
		for _, s := range snaps {
			sum += s.Downloaded
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

	workers := m.connectionsFor(meta.URL, min(d.Connections, m.cfg.MaxConnections))
	var alive atomic.Int32
	alive.Store(int32(workers))
	firstPart := pl.next(0)
	if firstPart == nil || firstPart.cursor != 0 {
		first.close() // only usable for a part that starts at byte 0
		first = nil
	}
	for id := 0; id < workers; id++ {
		var pt *part
		var op *opened
		if id == 0 {
			pt, op = firstPart, first
		}
		g.Go(func() error {
			yielded := false
			defer func() {
				if !yielded {
					alive.Add(-1)
				}
			}()
			// A throttled connection steps aside while others still run, so
			// concurrency settles at what the server allows; the last one
			// never yields, for it throttling is a real error.
			yield := func(c Classification) bool {
				if !isThrottle(c) {
					return false
				}
				for {
					n := alive.Load()
					if n <= 1 {
						return false
					}
					if alive.CompareAndSwap(n, n-1) {
						yielded = true
						return true
					}
				}
			}
			buf := make([]byte, readBufferSize)
			for {
				if pt == nil {
					if pt = pl.next(id); pt == nil {
						return nil
					}
				}
				err := m.fetchPart(gctx, e, f, pl, pt, meta, headers, tr, buf, yield, op)
				op = nil
				if errors.Is(err, errYielded) {
					pl.release(pt)
					m.hostRefused(meta.URL, int(tr.receiving.Load()))
					m.cfg.Logger.Info("server is limiting connections; continuing with fewer", "id", d.ID, "connections", alive.Load())
					return nil
				}
				if err != nil {
					return fmt.Errorf("part at byte %d: %w", pt.start, err)
				}
				mon.notify()
				pt = nil
			}
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
	if !pl.finished() {
		return errors.New("download stopped with parts unfinished")
	}
	return nil
}

func (m *Manager) fetchPart(ctx context.Context, e *entry, f *os.File, pl *planner, pt *part, meta *domain.RemoteMetadata, headers http.Header, tr *tracker, buf []byte, yield func(Classification) bool, op *opened) error {
	return m.withRetryYield(ctx, e, func() { pl.retried(pt) }, yield, func() (bool, error) {
		before, _ := pl.span(pt)
		err := m.fetchPartOnce(ctx, e, f, pl, pt, meta, headers, tr, buf, op)
		op = nil
		after, _ := pl.span(pt)
		return after > before, err
	})
}

// fetchPartOnce streams the rest of pt into the file. It stops as soon as
// pt is complete, including when another connection split pt's tail off.
func (m *Manager) fetchPartOnce(ctx context.Context, e *entry, f *os.File, pl *planner, pt *part, meta *domain.RemoteMetadata, headers http.Header, tr *tracker, buf []byte, op *opened) error {
	from, end := pl.span(pt)
	if from >= end {
		op.close()
		return nil
	}
	tr.active.Add(1)
	defer tr.active.Add(-1)
	var res *http.Response
	var onIdle func()
	if op != nil {
		defer op.adopt(ctx)()
		res, onIdle = op.res, func() { op.cancel(errIdleTimeout) }
	} else {
		reqCtx, cancelReq := context.WithCancelCause(ctx)
		defer cancelReq(nil)
		onIdle = func() { cancelReq(errIdleTimeout) }
		req, err := network.NewRequest(reqCtx, http.MethodGet, meta.URL, headers, network.RangeHeader(from, end-1))
		if err != nil {
			return err
		}
		if v := ifRangeValidator(meta.ETag, meta.LastModified); v != "" {
			req.Header.Set("If-Range", v)
		}
		if res, err = m.cfg.Client.Do(req); err != nil {
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
			if cr.Start != from || cr.End != end-1 {
				return fmt.Errorf("%w: got bytes %d-%d, requested %d-%d", errContentRangeMismatch, cr.Start, cr.End, from, end-1)
			}
		case http.StatusOK:
			if changedValidators(res.Header, meta) {
				return fmt.Errorf("%w: full response with new validators", domain.ErrRemoteChanged)
			}
			return errRangeIgnored
		default:
			return network.NewStatusError(res)
		}
	}
	body := newIdleReader(res.Body, m.cfg.ReadIdleTimeout, onIdle)
	defer body.stop()
	pos := from
	receiving := false
	defer func() {
		if receiving {
			tr.receiving.Add(-1)
		}
	}()
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if !receiving {
				// This connection is receiving data: the server allows at
				// least this many at once.
				receiving = true
				m.hostWorking(meta.URL, int(tr.receiving.Add(1)))
			}
			if err := m.throttle(ctx, e, n); err != nil {
				return err
			}
			allowed := pl.claim(pt, int64(n))
			if allowed > 0 {
				if _, werr := f.WriteAt(buf[:allowed], pos); werr != nil {
					pl.abandon(pt)
					return fmt.Errorf("write: %w", werr)
				}
				pl.commit(pt, allowed)
				pos += allowed
				tr.add(allowed)
			}
			if allowed < int64(n) {
				return nil // part complete or split: this response is no longer needed
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				if !pl.done(pt) {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			return body.err(rerr)
		}
		if pl.done(pt) {
			// Read the end of the body so the connection can be reused.
			_, _ = body.Read(buf[:1])
			return nil
		}
	}
}

func changedValidators(h http.Header, meta *domain.RemoteMetadata) bool {
	if etag := h.Get("ETag"); etag != "" && meta.ETag != "" && etag != meta.ETag {
		return true
	}
	lm := h.Get("Last-Modified")
	return lm != "" && meta.LastModified != "" && lm != meta.LastModified
}

// connectionsFor caps want at the limit learned for the URL's server.
func (m *Manager) connectionsFor(rawURL string, want int) int {
	if limit, ok := m.hosts.limit(hostOf(rawURL)); ok && limit < want {
		return max(1, limit)
	}
	return max(1, want)
}

// hostRefused records that the URL's server refused an extra connection
// while working others were receiving data, and persists what was learned.
func (m *Manager) hostRefused(rawURL string, working int) {
	if host := hostOf(rawURL); host != "" {
		l, _ := m.hosts.refused(host, working)
		m.saveHostLimit(l)
	}
}

// hostWorking records that n connections to the URL's server receive data.
func (m *Manager) hostWorking(rawURL string, n int) {
	if host := hostOf(rawURL); host != "" {
		if l, changed := m.hosts.working(host, n); changed {
			m.saveHostLimit(l)
		}
	}
}

func (m *Manager) saveHostLimit(l domain.HostLimit) {
	if m.cfg.HostLimits == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.cfg.HostLimits.SaveHostLimit(ctx, l); err != nil {
		m.cfg.Logger.Warn("save learned connection limit failed", "host", l.Host, "err", err)
	}
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// throttle waits for the download's own limiter, then the global one.
func (m *Manager) throttle(ctx context.Context, e *entry, n int) error {
	m.mu.Lock()
	own := e.limiter
	m.mu.Unlock()
	if err := own.Wait(ctx, n); err != nil {
		return err
	}
	return m.limiter.Wait(ctx, n)
}

// transferStream downloads with one connection. It resumes with an open
// range only when the server proved range support; otherwise every retry
// restarts from zero. Restarts and checkpoints are serialised so a crash
// can never leave a persisted offset pointing at unsynced data.
func (m *Manager) transferStream(ctx context.Context, e *entry, d *domain.Download, meta *domain.RemoteMetadata, headers http.Header, first *opened) error {
	f, err := filesystem.OpenTemp(d.TempPath)
	if err != nil {
		first.close()
		return fmt.Errorf("open temporary file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		first.close()
		return err
	}
	var offset int64
	if meta.RangeSupported {
		offset = durableOffset(d.Downloaded, info.Size(), meta.Size)
	}
	if offset != 0 {
		first.close() // the first request reads from byte 0
		first = nil
	}
	if err = checkSpace(d.Destination, meta.Size, meta.Size-offset); err != nil {
		first.close()
		return err
	}
	if err = f.Truncate(offset); err != nil {
		first.close()
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
		err := m.fetchStream(wctx, e, f, meta, headers, tr, buf, restart, first)
		first = nil
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

// fetchStream performs one single-connection transfer from the current
// offset, continuing op (the first request) when given. meta is updated
// when the server reveals a new length.
func (m *Manager) fetchStream(ctx context.Context, e *entry, f *os.File, meta *domain.RemoteMetadata, headers http.Header, tr *tracker, buf []byte, restart func(func(*domain.Download)) error, op *opened) error {
	offset := tr.bytes()
	if offset > 0 && !meta.RangeSupported {
		if err := restart(nil); err != nil {
			op.close()
			return err
		}
		offset = 0
	}
	if meta.Size >= 0 && offset >= meta.Size {
		op.close()
		return nil
	}
	tr.active.Add(1)
	defer tr.active.Add(-1)
	var res *http.Response
	var onIdle func()
	if op != nil && offset == 0 {
		defer op.adopt(ctx)()
		res, onIdle = op.res, func() { op.cancel(errIdleTimeout) }
	} else {
		op.close()
		reqCtx, cancelReq := context.WithCancelCause(ctx)
		defer cancelReq(nil)
		onIdle = func() { cancelReq(errIdleTimeout) }
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
		if res, err = m.cfg.Client.Do(req); err != nil {
			return err
		}
		defer res.Body.Close()
	}
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
			meta.Checksum = network.ChecksumFromHeaders(res.Header, true)
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
	body := newIdleReader(res.Body, m.cfg.ReadIdleTimeout, onIdle)
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
			if err := m.throttle(ctx, e, n); err != nil {
				return err
			}
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
			return body.err(rerr)
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

// idleReader aborts a response when no data arrives for timeout.
type idleReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
	fired   atomic.Bool
}

func newIdleReader(r io.Reader, timeout time.Duration, onIdle func()) *idleReader {
	ir := &idleReader{r: r, timeout: timeout}
	ir.timer = time.AfterFunc(timeout, func() {
		ir.fired.Store(true)
		onIdle()
	})
	return ir
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.timeout)
	}
	return n, err
}

func (i *idleReader) stop() { i.timer.Stop() }

// err reports an idle timeout instead of the cancellation it caused.
func (i *idleReader) err(err error) error {
	if i.fired.Load() {
		return errIdleTimeout
	}
	return err
}
