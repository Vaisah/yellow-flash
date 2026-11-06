package repositories

import (
	"context"
	"database/sql"
	"fmt"

	domain "github.com/vaisah/internal/domain/download"
)

// Segments persists byte-range progress.
type Segments struct{ db *sql.DB }

// NewSegments returns a repository bound to db.
func NewSegments(db *sql.DB) *Segments { return &Segments{db: db} }

// SaveSegments upserts all segments in one transaction with a prepared
// statement, so a checkpoint is either fully recorded or not at all.
func (r *Segments) SaveSegments(ctx context.Context, downloadID string, segments []domain.Segment) error {
	if len(segments) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO download_segments(id,download_id,segment_index,start_byte,end_byte,downloaded,status,retries)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET start_byte=excluded.start_byte, end_byte=excluded.end_byte,
		downloaded=excluded.downloaded, status=excluded.status, retries=excluded.retries`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range segments {
		if s.DownloadID != downloadID {
			return fmt.Errorf("segment %s belongs to %s, not %s", s.ID, s.DownloadID, downloadID)
		}
		if _, err = stmt.ExecContext(ctx, s.ID, downloadID, s.Index, s.Start, s.End, s.Downloaded, string(s.Status), s.Retries); err != nil {
			return fmt.Errorf("save segment %d: %w", s.Index, err)
		}
	}
	return tx.Commit()
}

// ListSegments returns segments ordered by index.
func (r *Segments) ListSegments(ctx context.Context, downloadID string) ([]domain.Segment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,download_id,segment_index,start_byte,end_byte,downloaded,status,retries
		FROM download_segments WHERE download_id=? ORDER BY segment_index`, downloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Segment{}
	for rows.Next() {
		var s domain.Segment
		var status string
		if err = rows.Scan(&s.ID, &s.DownloadID, &s.Index, &s.Start, &s.End, &s.Downloaded, &status, &s.Retries); err != nil {
			return nil, err
		}
		s.Status = domain.SegmentStatus(status)
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSegments removes all segments of a download.
func (r *Segments) DeleteSegments(ctx context.Context, downloadID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM download_segments WHERE download_id=?`, downloadID)
	return err
}
