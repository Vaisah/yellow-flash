package repositories

import (
	"context"
	"database/sql"

	domain "github.com/vaisah/internal/domain/download"
)

// HostLimits persists learned per-host connection limits.
type HostLimits struct{ db *sql.DB }

// NewHostLimits returns a repository bound to db.
func NewHostLimits(db *sql.DB) *HostLimits { return &HostLimits{db: db} }

// LoadHostLimits returns every saved limit.
func (r *HostLimits) LoadHostLimits(ctx context.Context) ([]domain.HostLimit, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT host,max_connections,seen,updated_at FROM host_limits ORDER BY host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.HostLimit
	for rows.Next() {
		var l domain.HostLimit
		var updated sql.NullString
		if err = rows.Scan(&l.Host, &l.MaxConnections, &l.Seen, &updated); err != nil {
			return nil, err
		}
		if l.UpdatedAt, err = parseStamp("updated_at", updated); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveHostLimit upserts one limit.
func (r *HostLimits) SaveHostLimit(ctx context.Context, l domain.HostLimit) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO host_limits(host,max_connections,seen,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(host) DO UPDATE SET max_connections=excluded.max_connections, seen=excluded.seen,
		updated_at=excluded.updated_at`, l.Host, l.MaxConnections, l.Seen, stamp(l.UpdatedAt))
	return err
}
