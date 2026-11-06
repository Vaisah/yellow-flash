package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/platform"
)

var (
	// errRangeIgnored means the server answered a range request with the
	// full body; the download falls back to a single stream.
	errRangeIgnored = errors.New("server ignored the Range request")
	// errContentRangeMismatch means a 206 response covers different bytes
	// than requested.
	errContentRangeMismatch = errors.New("Content-Range does not match the requested range")
	// ErrWebPage means the link returned an HTML page rather than a file,
	// e.g. a video site's watch page. Such pages need a media extractor or
	// the browser integration to find the real media URL.
	ErrWebPage = errors.New("the link points to a web page, not a downloadable file")
)

func isWebPage(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

// run owns the download until it reaches a resting state.
func (m *Manager) run(ctx context.Context, e *entry) {
	defer m.wg.Done()
	m.finish(e, m.execute(ctx, e))
}

// finish records the outcome. It is the only place a run leaves an active
// state, and it persists that state before releasing waiters.
func (m *Manager) finish(e *entry, err error) {
	m.mu.Lock()
	d := e.d
	cleanup := false
	switch {
	case d.Status == domain.Cancelled:
		cleanup = true
		d.Downloaded = 0
	case err == nil:
		if terr := d.Transition(domain.Completed); terr != nil {
			m.cfg.Logger.Error("completion transition rejected", "id", d.ID, "err", terr)
		}
		d.CompletedAt, d.Error = time.Now().UTC(), ""
	case d.Status == domain.Pausing || (d.Status == domain.Verifying && Classify(err).Kind == KindCanceled):
		_ = d.Transition(domain.Paused)
	default:
		if terr := d.Transition(domain.Failed); terr != nil {
			m.cfg.Logger.Error("failure transition rejected", "id", d.ID, "err", terr)
		}
		d.Error = err.Error()
	}
	done := e.done
	e.tracker, e.cancel, e.done = nil, nil, nil
	snap := d.Clone()
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if cleanup {
		m.discard(ctx, snap)
	}
	_ = m.save(ctx, snap)
	close(done)
}

// execute probes, transfers, verifies and publishes, restarting from zero
// when the remote representation changes underneath us.
func (m *Manager) execute(ctx context.Context, e *entry) error {
	m.mu.Lock()
	d := e.d.Clone()
	m.mu.Unlock()
	secret, err := m.cfg.Secrets.Get(ctx, d.ID)
	if err != nil && !errors.Is(err, platform.ErrSecretNotFound) {
		return fmt.Errorf("load credentials: %w", err)
	}
	headers := buildHeaders(d.Headers, secret)
	if err = os.MkdirAll(d.Destination, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	forceSingle := false
	for restart := 0; ; restart++ {
		err = m.attempt(ctx, e, headers, forceSingle)
		if err == nil || ctx.Err() != nil || restart >= maxRestarts {
			return err
		}
		switch {
		case errors.Is(err, errRangeIgnored):
			forceSingle = true
		case errors.Is(err, domain.ErrRemoteChanged):
		default:
			return err
		}
		m.cfg.Logger.Info("restarting download from zero", "id", d.ID, "reason", err.Error())
		if err = m.resetProgress(ctx, e); err != nil {
			return err
		}
	}
}

func (m *Manager) attempt(ctx context.Context, e *entry, headers http.Header, forceSingle bool) error {
	meta, err := m.probe(ctx, e, headers)
	if err != nil {
		return err
	}
	if forceSingle {
		meta.RangeSupported = false
	}
	d, err := m.applyMetadata(ctx, e, meta)
	if err != nil {
		return err
	}
	if meta.RangeSupported && meta.Size > 0 {
		err = m.transferSegmented(ctx, e, d, &meta, headers)
	} else {
		err = m.transferStream(ctx, e, d, &meta, headers)
	}
	if err != nil {
		return err
	}
	return m.complete(ctx, e, meta)
}

func (m *Manager) probe(ctx context.Context, e *entry, headers http.Header) (domain.RemoteMetadata, error) {
	m.mu.Lock()
	rawURL := e.d.URL
	m.mu.Unlock()
	var meta domain.RemoteMetadata
	err := m.withRetry(ctx, e, nil, func() (bool, error) {
		var err error
		meta, err = m.cfg.Prober.Probe(ctx, rawURL, headers)
		return false, err
	})
	if err != nil {
		return meta, fmt.Errorf("metadata: %w", err)
	}
	return meta, nil
}

// applyMetadata revalidates the representation, resets progress if it
// changed, adopts a Content-Disposition filename for fresh downloads, and
// moves INITIALIZING to DOWNLOADING.
func (m *Manager) applyMetadata(ctx context.Context, e *entry, meta domain.RemoteMetadata) (*domain.Download, error) {
	m.mu.Lock()
	changed := representationChanged(e.d, meta)
	status, id := e.d.Status, e.d.ID
	m.mu.Unlock()
	if status != domain.Initializing && status != domain.Downloading {
		return nil, context.Canceled
	}
	if changed {
		m.cfg.Logger.Info("remote resource changed; discarding partial data", "id", id)
		if err := m.resetProgress(ctx, e); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	d := e.d
	if d.Status != domain.Initializing && d.Status != domain.Downloading {
		m.mu.Unlock()
		return nil, context.Canceled
	}
	if isWebPage(meta.MIMEType) && !filesystem.IsHTMLName(d.Filename) && !filesystem.IsHTMLName(meta.Filename) {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w (server sent %s)", ErrWebPage, meta.MIMEType)
	}
	if !d.FilenameExplicit && d.Downloaded == 0 && !exists(d.TempPath) {
		name := d.Filename
		if meta.Filename != "" {
			if sanitized, err := filesystem.SanitizeFilename(meta.Filename); err == nil {
				name = sanitized
			}
		}
		name = filesystem.WithExtension(name, filesystem.ExtensionForMIME(meta.MIMEType))
		if name != d.Filename {
			d.Filename = m.reserveNameLocked(d.Destination, name, d.ID)
			d.TempPath = filesystem.TempPath(d.Destination, d.Filename)
		}
	}
	d.FinalURL, d.Size, d.MIMEType = meta.URL, meta.Size, meta.MIMEType
	d.ETag, d.LastModified, d.RangeSupported = meta.ETag, meta.LastModified, meta.RangeSupported
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now().UTC()
	}
	if d.Status == domain.Initializing {
		_ = d.Transition(domain.Downloading)
	}
	d.UpdatedAt = time.Now().UTC()
	snap := d.Clone()
	m.mu.Unlock()
	if err := m.save(ctx, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// complete verifies the finished temporary file and publishes it. A
// download is never marked complete merely because its workers stopped.
func (m *Manager) complete(ctx context.Context, e *entry, meta domain.RemoteMetadata) error {
	snap, err := m.transition(e, domain.Verifying)
	if err != nil {
		// A pause or cancel won the race; the bytes stay for a later resume.
		return context.Canceled
	}
	if err = m.save(ctx, snap); err != nil {
		return err
	}
	size, err := verifyFile(ctx, snap.TempPath, meta)
	if err != nil {
		if errors.Is(err, ErrIntegrity) {
			// Corrupt data must not be resumed; a retry starts clean.
			if rerr := m.resetProgress(context.WithoutCancel(ctx), e); rerr != nil {
				m.cfg.Logger.Warn("discard corrupt data failed", "id", snap.ID, "err", rerr)
			}
		}
		return err
	}
	name, err := m.publisher.Publish(snap.TempPath, snap.Destination, snap.Filename)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	m.update(e, func(d *domain.Download) {
		d.Filename, d.Size, d.Downloaded = name, size, size
	})
	if err = m.store.DeleteSegments(context.WithoutCancel(ctx), snap.ID); err != nil {
		m.cfg.Logger.Warn("delete completed segments failed", "id", snap.ID, "err", err)
	}
	return nil
}

func (m *Manager) setTracker(e *entry, tr *tracker) {
	m.mu.Lock()
	e.tracker = tr
	m.mu.Unlock()
}
