package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// migration is one schema version. Each runs in its own transaction
// together with the PRAGMA user_version bump, so a failure leaves the
// database at the previous version.
type migration struct {
	version    int
	statements []string
}

// migrations must only ever be appended to.
var migrations = []migration{
	{
		// v1 is exactly the schema shipped before versioned migrations, so
		// existing databases (already at user_version 1) are recognised.
		version: 1,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL)`,
			`INSERT INTO schema_version(version) SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM schema_version)`,
			`CREATE TABLE IF NOT EXISTS downloads(
				id TEXT PRIMARY KEY, url TEXT NOT NULL, final_url TEXT, filename TEXT NOT NULL,
				destination TEXT NOT NULL, temp_path TEXT NOT NULL, mime_type TEXT, etag TEXT,
				last_modified TEXT, size INTEGER NOT NULL, downloaded INTEGER NOT NULL,
				range_supported INTEGER NOT NULL, connections INTEGER NOT NULL, retries INTEGER NOT NULL,
				status TEXT NOT NULL, error TEXT, created_at TEXT NOT NULL, started_at TEXT,
				completed_at TEXT, updated_at TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS downloads_status_idx ON downloads(status)`,
			`CREATE TABLE IF NOT EXISTS download_segments(
				id TEXT PRIMARY KEY,
				download_id TEXT NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
				segment_index INTEGER NOT NULL, start_byte INTEGER NOT NULL, end_byte INTEGER NOT NULL,
				downloaded INTEGER NOT NULL, status TEXT NOT NULL, retries INTEGER NOT NULL,
				UNIQUE(download_id, segment_index))`,
			`CREATE TABLE IF NOT EXISTS download_headers(
				download_id TEXT NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
				name TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY(download_id, name))`,
			`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS queues(id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at TEXT NOT NULL)`,
		},
	},
	{
		version: 2,
		statements: []string{
			`ALTER TABLE downloads ADD COLUMN filename_explicit INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE downloads ADD COLUMN queue_id TEXT REFERENCES queues(id) ON DELETE SET NULL`,
			`ALTER TABLE queues ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 0`,
			`CREATE INDEX IF NOT EXISTS downloads_created_idx ON downloads(created_at)`,
			`CREATE INDEX IF NOT EXISTS downloads_queue_idx ON downloads(queue_id)`,
			// user_version is the single source of truth from now on.
			`DROP TABLE IF EXISTS schema_version`,
		},
	},
}

// SchemaVersion is the newest schema this build understands.
var SchemaVersion = migrations[len(migrations)-1].version

func currentVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

func migrate(ctx context.Context, db *sql.DB) error {
	version, err := currentVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > SchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, SchemaVersion)
	}
	for _, mig := range migrations {
		if mig.version <= version {
			continue
		}
		if err := apply(ctx, db, mig); err != nil {
			return fmt.Errorf("migrate to v%d: %w", mig.version, err)
		}
	}
	return nil
}

func apply(ctx context.Context, db *sql.DB, mig migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range mig.statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, mig.version)); err != nil {
		return err
	}
	return tx.Commit()
}
