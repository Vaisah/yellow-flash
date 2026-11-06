package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "downloads.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sample(id string) *domain.Download {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &domain.Download{ID: id, URL: "https://example.test/file", Filename: "file", Destination: "/tmp",
		TempPath: "/tmp/file.part", Size: 100, Downloaded: 20, Connections: 4, Status: domain.Paused,
		ETag: `"v1"`, FilenameExplicit: true, CreatedAt: now, UpdatedAt: now, StartedAt: now}
}

func TestPragmasAndSchemaVersion(t *testing.T) {
	s := open(t)
	var fk, version int
	var mode string
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
}

func TestDownloadRoundTripHeadersAndNotFound(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	d := sample("a")
	d.Headers = map[string]string{"Referer": "https://example.test/", "Authorization": "Bearer secret", "Cookie": "s=1"}
	if err := s.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.Paused || got.Downloaded != 20 || got.Size != 100 || !got.CreatedAt.Equal(d.CreatedAt) ||
		!got.StartedAt.Equal(d.StartedAt) || !got.CompletedAt.IsZero() || !got.FilenameExplicit || got.ETag != `"v1"` {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Headers) != 1 || got.Headers["Referer"] == "" {
		t.Fatalf("credentials must never be persisted: %v", got.Headers)
	}
	var leaked int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM download_headers WHERE name IN ('Authorization','Cookie')`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("credential header stored")
	}
	if _, err = s.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	d.Status, d.Downloaded = domain.Completed, 100
	if err = s.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Status != domain.Completed {
		t.Fatalf("list after upsert: %+v %v", list, err)
	}
}

func TestSegmentsTransactionalUpsertAndCascade(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if err := s.Save(ctx, sample("d")); err != nil {
		t.Fatal(err)
	}
	segs := []domain.Segment{
		{ID: "d-0", DownloadID: "d", Index: 0, Start: 0, End: 49, Downloaded: 10, Status: domain.SegmentPending},
		{ID: "d-1", DownloadID: "d", Index: 1, Start: 50, End: 99, Downloaded: 50, Status: domain.SegmentCompleted},
	}
	if err := s.SaveSegments(ctx, "d", segs); err != nil {
		t.Fatal(err)
	}
	segs[0].Downloaded, segs[0].Retries = 30, 2
	if err := s.SaveSegments(ctx, "d", segs[:1]); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListSegments(ctx, "d")
	if err != nil || len(got) != 2 || got[0].Downloaded != 30 || got[0].Retries != 2 || got[1].Status != domain.SegmentCompleted {
		t.Fatalf("segments: %+v %v", got, err)
	}
	// A batch containing an invalid row must not be partially applied.
	bad := []domain.Segment{{ID: "d-0", DownloadID: "d", Index: 0, End: 49, Downloaded: 49}, {ID: "x-0", DownloadID: "x"}}
	if err = s.SaveSegments(ctx, "d", bad); err == nil {
		t.Fatal("mismatched download id accepted")
	}
	if got, _ = s.ListSegments(ctx, "d"); got[0].Downloaded != 30 {
		t.Fatal("failed batch partially applied")
	}
	if err = s.SaveSegments(ctx, "orphan", []domain.Segment{{ID: "o-0", DownloadID: "orphan"}}); err == nil {
		t.Fatal("foreign key not enforced")
	}
	if err = s.Delete(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.ListSegments(ctx, "d"); len(got) != 0 {
		t.Fatal("segments did not cascade")
	}
}

func TestSettingsAndQueues(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, ok, err := s.Settings().Get(ctx, "connections"); ok || err != nil {
		t.Fatalf("missing setting: ok=%v err=%v", ok, err)
	}
	_ = s.Settings().Set(ctx, "connections", "8")
	_ = s.Settings().Set(ctx, "connections", "4")
	if v, ok, err := s.Settings().Get(ctx, "connections"); v != "4" || !ok || err != nil {
		t.Fatalf("setting = %q %v %v", v, ok, err)
	}
	if all, err := s.Settings().All(ctx); err != nil || len(all) != 1 {
		t.Fatalf("all settings: %v %v", all, err)
	}
	q := &queue.Queue{ID: "q1", Name: "Main", MaxConcurrent: 3, CreatedAt: time.Now().UTC()}
	if err := s.Queues().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	if qs, err := s.Queues().List(ctx); err != nil || len(qs) != 1 || qs[0].MaxConcurrent != 3 {
		t.Fatalf("queues: %+v %v", qs, err)
	}
	d := sample("in-queue")
	d.QueueID = "q1"
	if err := s.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.Queues().Delete(ctx, "q1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "in-queue")
	if err != nil || got.QueueID != "" {
		t.Fatalf("queue deletion should clear membership: %+v %v", got, err)
	}
}

// TestMigratesVersion1Database opens a database created by the previous
// release (schema v1) and checks existing rows survive the upgrade.
func TestMigratesVersion1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = apply(ctx, db, migrations[0]); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO downloads VALUES('old','https://example.test/a','','a.bin','/tmp','/tmp/a.bin.part','','','',10,5,1,4,0,'PAUSED','','2025-01-01T00:00:00Z',NULL,NULL,'2025-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(ctx, "old")
	if err != nil || got.Downloaded != 5 || got.Status != domain.Paused || got.FilenameExplicit {
		t.Fatalf("migrated row: %+v %v", got, err)
	}
	var version int
	_ = s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != SchemaVersion {
		t.Fatalf("version %d", version)
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, _ := sql.Open("sqlite", path)
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("newer schema must be rejected")
	}
}
