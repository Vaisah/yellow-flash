// Package sqlite is the SQLite persistence adapter (modernc.org/sqlite, no
// cgo). The engine depends only on domain interfaces, never on this package.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// pragmas are applied by the driver to every new connection, so they
// survive connection recycling. WAL with synchronous=NORMAL is crash-safe
// for the application; a power loss can at worst roll back the latest
// progress checkpoint, which is always safe because data is synced before
// progress is recorded.
const pragmas = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?"+pragmas)
	if err != nil {
		return nil, err
	}
	// One connection serialises writers (SQLite allows one at a time) and
	// avoids SQLITE_BUSY between our own goroutines.
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err = migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
