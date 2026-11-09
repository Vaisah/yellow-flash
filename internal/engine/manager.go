// Package engine implements the HTTP download engine independently of any
// UI, database or operating system. Persistence, networking and secret
// storage are injected through small interfaces.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vaisah/internal/application/events"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/infrastructure/network/sites"
	"github.com/vaisah/internal/platform"
)

// Prober discovers remote metadata. *network.Prober satisfies it.
type Prober interface {
	Probe(ctx context.Context, url string, headers http.Header) (domain.RemoteMetadata, error)
	// Open sends a download's first request; the response body is the
	// file from byte 0 (nil for an empty file).
	Open(ctx context.Context, url string, headers http.Header) (domain.RemoteMetadata, *http.Response, error)
}

// Config tunes the engine. Zero values select defaults.
type Config struct {
	// Connections is the default per-download connection count (default 8).
	Connections int
	// MaxConnections caps any download's connection count (default 16).
	MaxConnections int
	// MinSegmentSize avoids splitting small files into many tiny ranges
	// (default 256 KiB). More connections are not always faster.
	MinSegmentSize int64
	// Retry is the retry policy; nil selects DefaultRetryPolicy.
	Retry *RetryPolicy
	// Client performs transfers; default network.NewClient with
	// AllowPrivateNetworks.
	Client network.Doer
	// Prober discovers metadata; default network.NewProber(Client).
	Prober Prober
	// AllowPrivateNetworks permits loopback/private destinations for the
	// default client. Keep false for untrusted URLs.
	AllowPrivateNetworks bool
	Publisher            events.Publisher
	Secrets              platform.SecretStore
	Logger               *slog.Logger
	// ProgressInterval throttles progress events (default 250ms).
	ProgressInterval time.Duration
	// PersistInterval is how often progress is synced and saved (default 2s).
	PersistInterval time.Duration
	// ReadIdleTimeout aborts a response body that stalls (default 60s).
	ReadIdleTimeout time.Duration
	// DefaultDirectory is used when a request has none (default ~/Downloads).
	DefaultDirectory string
	// HostLimits persists learned per-server connection limits; nil keeps
	// them in memory only.
	HostLimits domain.HostLimitStore
	// HostLimitDecay is how long a learned limit lasts without being seen
	// again (default 10 minutes).
	HostLimitDecay time.Duration
	// DNS is the resolver the default client uses; nil is the system's.
	DNS *network.DNSChoice
}

const (
	hardMaxConnections = 32
	maxRestarts        = 2
)

func (c Config) withDefaults() Config {
	if c.MaxConnections < 1 {
		c.MaxConnections = 16
	}
	c.MaxConnections = min(c.MaxConnections, hardMaxConnections)
	if c.Connections < 1 {
		c.Connections = 8
	}
	c.Connections = min(c.Connections, c.MaxConnections)
	if c.MinSegmentSize < 1 {
		c.MinSegmentSize = 256 << 10
	}
	if c.ProgressInterval <= 0 {
		c.ProgressInterval = 250 * time.Millisecond
	}
	if c.PersistInterval <= 0 {
		c.PersistInterval = 2 * time.Second
	}
	if c.ReadIdleTimeout <= 0 {
		c.ReadIdleTimeout = 60 * time.Second
	}
	if c.HostLimitDecay <= 0 {
		c.HostLimitDecay = hostLimitDecay
	}
	if c.Client == nil {
		c.Client = network.NewClient(network.Options{AllowPrivate: c.AllowPrivateNetworks, MaxConnsPerHost: c.MaxConnections * 2, DNS: c.DNS})
	}
	if c.Prober == nil {
		c.Prober = network.NewProber(c.Client)
	}
	if c.Publisher == nil {
		c.Publisher = events.Nop{}
	}
	if c.Secrets == nil {
		c.Secrets = platform.NewMemorySecretStore()
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return c
}

// entry is the in-memory owner of one download. All fields are guarded by
// Manager.mu. done is non-nil exactly while a run goroutine owns the entry.
type entry struct {
	d       *domain.Download
	cancel  context.CancelFunc
	done    chan struct{}
	tracker *tracker
	// limiter enforces the download's own speed limit while it runs.
	limiter *Limiter
}

// Manager owns download lifecycles. It is the single authority for status
// changes: every change goes through domain.Download.Transition under mu.
type Manager struct {
	cfg       Config
	retry     RetryPolicy
	store     domain.Store
	publisher *filesystem.Publisher

	baseCtx    context.Context
	baseCancel context.CancelFunc
	wg         sync.WaitGroup

	// limiter caps the combined speed of all downloads (unlimited by default).
	limiter *Limiter
	// hosts holds learned per-server connection limits.
	hosts *hostLimits

	mu       sync.Mutex
	items    map[string]*entry
	stopping bool
}

// SetSpeedLimit caps the combined download speed in bytes per second;
// 0 or less removes the limit. It applies at once to running downloads.
func (m *Manager) SetSpeedLimit(bytesPerSecond int64) { m.limiter.Set(bytesPerSecond) }

// SpeedLimit returns the current limit in bytes per second (0 = none).
func (m *Manager) SpeedLimit() int64 { return m.limiter.Rate() }

// SetDownloadSpeedLimit caps one download in bytes per second (0 or less:
// unlimited). It is persisted, and applies at once if the download runs.
func (m *Manager) SetDownloadSpeedLimit(ctx context.Context, id string, bytesPerSecond int64) error {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return err
	}
	bytesPerSecond = max(0, bytesPerSecond)
	snap := m.update(e, func(d *domain.Download) { d.SpeedLimit = bytesPerSecond })
	m.mu.Lock()
	if e.limiter != nil {
		e.limiter.Set(bytesPerSecond)
	}
	m.mu.Unlock()
	return m.save(ctx, snap)
}

// HostLimits lists the per-server connection limits learned so far.
func (m *Manager) HostLimits() []domain.HostLimit { return m.hosts.learned() }

// New creates a Manager. store is required.
func New(store domain.Store, cfg Config) (*Manager, error) {
	if store == nil {
		return nil, errors.New("engine: store is required")
	}
	cfg = cfg.withDefaults()
	policy := DefaultRetryPolicy()
	if cfg.Retry != nil {
		policy = cfg.Retry.normalized()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, retry: policy, store: store, publisher: filesystem.NewPublisher(), baseCtx: ctx, baseCancel: cancel,
		items: map[string]*entry{}, limiter: NewLimiter(0), hosts: newHostLimits(cfg.HostLimitDecay)}
	if cfg.HostLimits != nil {
		saved, err := cfg.HostLimits.LoadHostLimits(ctx)
		if err != nil {
			cfg.Logger.Warn("load learned connection limits failed", "err", err)
		}
		m.hosts.load(saved)
	}
	return m, nil
}

// managedHeaders are controlled by the engine and may not be overridden.
var managedHeaders = map[string]bool{
	"Range": true, "If-Range": true, "Accept-Encoding": true, "Host": true, "Content-Length": true,
	"Transfer-Encoding": true, "Connection": true, "Te": true, "Upgrade": true, "Trailer": true,
	"Keep-Alive": true, "Proxy-Connection": true,
}

// splitHeaders validates caller headers and separates credentials.
func splitHeaders(in map[string]string) (public, secret map[string]string, err error) {
	for name, value := range in {
		canonical := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if canonical == "" || strings.ContainsAny(canonical, " \t\r\n:") || strings.ContainsAny(value, "\r\n\x00") {
			return nil, nil, fmt.Errorf("%w: malformed header %q", domain.ErrInvalidRequest, name)
		}
		if managedHeaders[canonical] {
			return nil, nil, fmt.Errorf("%w: header %s is managed by the engine", domain.ErrInvalidRequest, canonical)
		}
		target := &public
		if domain.IsSensitiveHeader(canonical) {
			target = &secret
		}
		if *target == nil {
			*target = map[string]string{}
		}
		(*target)[canonical] = value
	}
	return public, secret, nil
}

// Create validates an untrusted request and records a QUEUED download.
func (m *Manager) Create(ctx context.Context, req domain.CreateRequest) (*domain.Download, error) {
	m.mu.Lock()
	stopping := m.stopping
	m.mu.Unlock()
	if stopping {
		return nil, domain.ErrShuttingDown
	}
	u, err := network.ValidateURL(network.UnwrapLink(req.URL))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrInvalidRequest, err)
	}
	if r := sites.NotAFile(u); r != nil { // a Google Drive folder
		return nil, fmt.Errorf("%w: %w", domain.ErrInvalidRequest, r)
	}
	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		dir = m.cfg.DefaultDirectory
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve default download directory: %w", err)
		}
		dir = filepath.Join(home, "Downloads")
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return nil, fmt.Errorf("%w: directory: %w", domain.ErrInvalidRequest, err)
	}
	explicit := strings.TrimSpace(req.Filename) != ""
	raw := req.Filename
	if !explicit {
		raw = filesystem.FilenameFromURL(u)
	}
	name, err := filesystem.SanitizeFilename(raw)
	if err != nil {
		if explicit {
			return nil, fmt.Errorf("%w: %w", domain.ErrInvalidRequest, err)
		}
		name = "download"
	}
	conns := req.Connections
	if conns == 0 {
		conns = m.cfg.Connections
	}
	if conns < 1 || conns > m.cfg.MaxConnections {
		return nil, fmt.Errorf("%w: connections must be between 1 and %d", domain.ErrInvalidRequest, m.cfg.MaxConnections)
	}
	public, secret, err := splitHeaders(req.Headers)
	if err != nil {
		return nil, err
	}
	checksum, err := domain.ParseChecksum(req.Checksum)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrInvalidRequest, err)
	}
	if req.SpeedLimit < 0 {
		return nil, fmt.Errorf("%w: speed limit must not be negative", domain.ErrInvalidRequest)
	}
	name = filesystem.FitName(dir, name)
	now := time.Now().UTC()
	d := &domain.Download{ID: uuid.NewString(), URL: u.String(), FilenameExplicit: explicit, Destination: dir,
		Size: domain.UnknownSize, Connections: conns, Status: domain.Queued, QueueID: req.QueueID, Headers: public,
		SpeedLimit: req.SpeedLimit, ExpectedChecksum: checksum.String(), CreatedAt: now, UpdatedAt: now}
	m.mu.Lock()
	d.Filename = m.reserveNameLocked(dir, name, d.ID)
	d.TempPath = filesystem.TempPath(dir, d.Filename)
	m.items[d.ID] = &entry{d: d}
	snap := d.Clone()
	m.mu.Unlock()
	if len(secret) > 0 {
		if err = m.cfg.Secrets.Put(ctx, d.ID, secret); err != nil {
			m.forget(d.ID)
			return nil, fmt.Errorf("store credentials: %w", err)
		}
	}
	if err = m.store.Save(ctx, snap); err != nil {
		m.forget(d.ID)
		_ = m.cfg.Secrets.Delete(ctx, d.ID)
		return nil, err
	}
	m.emitChanged(d.ID)
	return snap, nil
}

// Inspect probes a link without creating a download, returning the name the
// download would get, its size and type, and whether it is a web page.
func (m *Manager) Inspect(ctx context.Context, rawURL string, headers map[string]string) (*domain.LinkInfo, error) {
	u, err := network.ValidateURL(network.UnwrapLink(rawURL))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrInvalidRequest, err)
	}
	public, secret, err := splitHeaders(headers)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := m.cfg.Prober.Probe(ctx, u.String(), buildHeaders(public, secret))
	if err != nil {
		return nil, err
	}
	raw := meta.Filename
	if raw == "" {
		raw = filesystem.FilenameFromURL(u)
	}
	name, err := filesystem.SanitizeFilename(raw)
	if err != nil {
		name = "download"
	}
	// Decide "web page" on the name as linked, before an extension is
	// inferred from the content type (which would turn "watch" into
	// "watch.html" and hide the problem).
	webPage := isWebPage(meta.MIMEType) && !filesystem.IsHTMLName(name)
	name = filesystem.WithExtension(name, filesystem.ExtensionForMIME(meta.MIMEType))
	return &domain.LinkInfo{URL: u.String(), FinalURL: meta.URL, Filename: name, Size: meta.Size, MIMEType: meta.MIMEType,
		RangeSupported: meta.RangeSupported, WebPage: webPage}, nil
}

func (m *Manager) forget(id string) {
	m.mu.Lock()
	delete(m.items, id)
	m.mu.Unlock()
}

// reserveNameLocked returns name, or "name (n).ext", such that neither the
// final file nor its temporary file is in use on disk or by another live
// download. Callers hold m.mu.
func (m *Manager) reserveNameLocked(dir, name, selfID string) string {
	for n := 0; ; n++ {
		candidate := filesystem.Candidate(name, n)
		temp := filesystem.TempPath(dir, candidate)
		taken := false
		for id, e := range m.items {
			if id != selfID && !e.d.Status.IsTerminal() && e.d.TempPath == temp {
				taken = true
				break
			}
		}
		if !taken && !exists(temp) && !exists(filepath.Join(dir, candidate)) {
			return candidate
		}
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Start begins a QUEUED, PAUSED or FAILED download.
func (m *Manager) Start(ctx context.Context, id string) error {
	return m.begin(ctx, id, domain.Queued, domain.Paused, domain.Failed)
}

// Resume continues a PAUSED download. Remote metadata is revalidated first;
// if the resource changed, the download restarts from zero.
func (m *Manager) Resume(ctx context.Context, id string) error {
	return m.begin(ctx, id, domain.Paused)
}

// Retry restarts a FAILED download, keeping valid partial data.
func (m *Manager) Retry(ctx context.Context, id string) error {
	return m.begin(ctx, id, domain.Failed)
}

func (m *Manager) begin(ctx context.Context, id string, allowed ...domain.Status) error {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return domain.ErrShuttingDown
	}
	ok := false
	for _, s := range allowed {
		ok = ok || e.d.Status == s
	}
	if !ok || e.done != nil {
		from := e.d.Status
		m.mu.Unlock()
		return &domain.TransitionError{From: from, To: domain.Initializing}
	}
	if err = e.d.Transition(domain.Initializing); err != nil {
		m.mu.Unlock()
		return err
	}
	e.d.Error = ""
	if e.limiter == nil {
		e.limiter = NewLimiter(e.d.SpeedLimit)
	} else {
		e.limiter.Set(e.d.SpeedLimit)
	}
	runCtx, cancel := context.WithCancel(m.baseCtx)
	e.cancel, e.done = cancel, make(chan struct{})
	snap := e.d.Clone()
	m.wg.Add(1)
	m.mu.Unlock()

	if err = m.store.Save(ctx, snap); err != nil {
		cancel()
		m.mu.Lock()
		_ = e.d.Transition(domain.Failed)
		e.d.Error = "could not persist state: " + err.Error()
		done := e.done
		e.cancel, e.done = nil, nil
		m.mu.Unlock()
		close(done)
		m.wg.Done()
		return err
	}
	m.emitChanged(id)
	go m.run(runCtx, e)
	return nil
}

// Pause stops an active download after persisting durable progress. It
// blocks until workers have stopped or ctx ends.
func (m *Manager) Pause(ctx context.Context, id string) error {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	switch e.d.Status {
	case domain.Paused:
		m.mu.Unlock()
		return nil
	case domain.Initializing, domain.Downloading:
		if err = e.d.Transition(domain.Pausing); err != nil {
			m.mu.Unlock()
			return err
		}
	case domain.Pausing:
	default:
		from := e.d.Status
		m.mu.Unlock()
		return &domain.TransitionError{From: from, To: domain.Pausing}
	}
	cancel, done := e.cancel, e.done
	m.mu.Unlock()
	m.emitChanged(id)
	if cancel != nil {
		cancel()
	}
	return waitDone(ctx, done)
}

// Cancel stops the download, deletes its temporary data and marks it
// CANCELLED. Unlike Pause, progress is discarded.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if err = e.d.Transition(domain.Cancelled); err != nil {
		m.mu.Unlock()
		return err
	}
	cancel, done := e.cancel, e.done
	snap := e.d.Clone()
	m.mu.Unlock()
	if done != nil {
		// The run goroutine performs cleanup once its workers have stopped.
		cancel()
		return waitDone(ctx, done)
	}
	m.discard(ctx, snap)
	if err = m.store.Save(ctx, snap); err != nil {
		return err
	}
	m.emitChanged(id)
	return nil
}

// Delete removes an inactive download record and its temporary file.
// Completed files are never deleted.
func (m *Manager) Delete(ctx context.Context, id string) error {
	e, err := m.entryFor(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if e.done != nil || e.d.Status.IsActive() {
		from := e.d.Status
		m.mu.Unlock()
		return fmt.Errorf("%w: cannot delete a %s download", domain.ErrInvalidTransition, from)
	}
	snap := e.d.Clone()
	m.mu.Unlock()
	if snap.Status != domain.Completed {
		if err = os.Remove(snap.TempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err = m.store.Delete(ctx, id); err != nil {
		return err
	}
	_ = m.cfg.Secrets.Delete(ctx, id)
	m.forget(id)
	return nil
}

// Get returns a snapshot of one download.
func (m *Manager) Get(ctx context.Context, id string) (*domain.Download, error) {
	m.mu.Lock()
	e := m.items[id]
	if e != nil {
		snap := m.snapshotLocked(e)
		m.mu.Unlock()
		return snap, nil
	}
	m.mu.Unlock()
	return m.store.Get(ctx, id)
}

// List returns all downloads, newest first, with live progress.
func (m *Manager) List(ctx context.Context) ([]domain.Download, error) {
	stored, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	seen := make(map[string]bool, len(stored))
	out := make([]domain.Download, 0, len(stored))
	for _, d := range stored {
		seen[d.ID] = true
		if e := m.items[d.ID]; e != nil {
			out = append(out, *m.snapshotLocked(e))
		} else {
			out = append(out, d)
		}
	}
	for id, e := range m.items {
		if !seen[id] {
			out = append(out, *m.snapshotLocked(e))
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Recover must be called once at startup. Downloads left active by a crash
// are marked PAUSED and their byte counts reconciled with the temporary
// file and segment records. Remote metadata is revalidated on resume.
func (m *Manager) Recover(ctx context.Context) error {
	stored, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	for i := range stored {
		d := &stored[i]
		m.mu.Lock()
		live := m.items[d.ID] != nil && m.items[d.ID].done != nil
		m.mu.Unlock()
		if live {
			continue
		}
		interrupted := d.MarkInterrupted()
		changed, err := m.reconcile(ctx, d)
		if err != nil {
			return fmt.Errorf("recover %s: %w", d.ID, err)
		}
		if interrupted || changed {
			if err = m.store.Save(ctx, d); err != nil {
				return err
			}
		}
		m.mu.Lock()
		m.items[d.ID] = &entry{d: d.Clone()}
		m.mu.Unlock()
	}
	return nil
}

// Shutdown pauses all active downloads and waits for their state to be
// persisted. New work is rejected afterwards.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.stopping = true
	for _, e := range m.items {
		if e.cancel == nil {
			continue
		}
		if e.d.Status == domain.Initializing || e.d.Status == domain.Downloading {
			_ = e.d.Transition(domain.Pausing)
		}
		e.cancel()
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	if err := waitDone(ctx, done); err != nil {
		return err
	}
	m.baseCancel()
	return nil
}

func waitDone(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) entryFor(ctx context.Context, id string) (*entry, error) {
	m.mu.Lock()
	e := m.items[id]
	m.mu.Unlock()
	if e != nil {
		return e, nil
	}
	d, err := m.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e = m.items[id]; e == nil {
		e = &entry{d: d}
		m.items[id] = e
	}
	return e, nil
}

// snapshotLocked returns a copy including live byte counts.
func (m *Manager) snapshotLocked(e *entry) *domain.Download {
	c := e.d.Clone()
	if e.tracker != nil {
		c.Downloaded = e.tracker.bytes()
	}
	return c
}

// update mutates the download under the lock and returns a snapshot.
func (m *Manager) update(e *entry, fn func(d *domain.Download)) *domain.Download {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(e.d)
	e.d.UpdatedAt = time.Now().UTC()
	return e.d.Clone()
}

// transition changes status through the state machine.
func (m *Manager) transition(e *entry, to domain.Status) (*domain.Download, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := e.d.Transition(to); err != nil {
		return nil, err
	}
	return e.d.Clone(), nil
}

func (m *Manager) save(ctx context.Context, snap *domain.Download) error {
	if err := m.store.Save(ctx, snap); err != nil {
		m.cfg.Logger.Warn("persist download failed", "id", snap.ID, "err", err)
		return err
	}
	m.emitChanged(snap.ID)
	return nil
}

func (m *Manager) recordRetry(e *entry) {
	m.mu.Lock()
	e.d.Retries++
	id := e.d.ID
	m.mu.Unlock()
	m.emitChanged(id)
}

// withRetry runs op until it succeeds, fails permanently, exhausts the
// policy, or ctx ends. The attempt counter resets whenever op made
// progress, so long transfers survive occasional transient failures.
func (m *Manager) withRetry(ctx context.Context, e *entry, onRetry func(), op func() (progressed bool, err error)) error {
	return m.withRetryYield(ctx, e, onRetry, nil, op)
}

// withRetryYield is withRetry with an optional yield hook: when it returns
// true for a failure, retrying stops and errYielded is returned so the
// caller can hand the work to someone else.
func (m *Manager) withRetryYield(ctx context.Context, e *entry, onRetry func(), yield func(Classification) bool, op func() (progressed bool, err error)) error {
	attempt, total := 0, 0
	for {
		progressed, err := op()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if progressed {
			attempt = 0
		}
		c := Classify(err)
		if yield != nil && yield(c) {
			return fmt.Errorf("%w: %w", errYielded, err)
		}
		if !m.retry.ShouldRetry(attempt, c) {
			if total > 0 {
				return fmt.Errorf("%w (gave up after %d retries)", err, total)
			}
			return err
		}
		m.cfg.Logger.Debug("retrying", "kind", c.Kind, "status", c.StatusCode, "attempt", attempt+1, "err", err.Error())
		m.recordRetry(e)
		if onRetry != nil {
			onRetry()
		}
		if err = sleepContext(ctx, m.retry.Delay(attempt, c, rand.Float64())); err != nil {
			return err
		}
		attempt++
		total++
	}
}

func (m *Manager) emitChanged(id string) {
	m.mu.Lock()
	e := m.items[id]
	if e == nil {
		m.mu.Unlock()
		return
	}
	snap := m.snapshotLocked(e)
	m.mu.Unlock()
	snap.Headers = nil
	m.cfg.Publisher.Publish(events.Event{Type: events.DownloadChanged, Download: *snap})
}

func (m *Manager) emitProgress(e *entry, tr *tracker) {
	m.mu.Lock()
	snap := m.snapshotLocked(e)
	m.mu.Unlock()
	snap.Headers = nil
	p := tr.snapshot(snap.ID, time.Now())
	m.cfg.Publisher.Publish(events.Event{Type: events.ProgressUpdated, Download: *snap, Progress: p})
}

// discard removes temporary data and segment records.
func (m *Manager) discard(ctx context.Context, d *domain.Download) {
	if err := os.Remove(d.TempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.cfg.Logger.Warn("remove temporary file failed", "id", d.ID, "err", err)
	}
	if err := m.store.DeleteSegments(ctx, d.ID); err != nil {
		m.cfg.Logger.Warn("delete segments failed", "id", d.ID, "err", err)
	}
	_ = m.cfg.Secrets.Delete(ctx, d.ID)
}
