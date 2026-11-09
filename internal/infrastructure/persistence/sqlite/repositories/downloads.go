package repositories

import (
	"context"
	"database/sql"
	"fmt"

	domain "github.com/vaisah/internal/domain/download"
)

// Downloads persists download records and their non-secret headers.
type Downloads struct{ db *sql.DB }

// NewDownloads returns a repository bound to db.
func NewDownloads(db *sql.DB) *Downloads { return &Downloads{db: db} }

const downloadColumns = `id,url,final_url,filename,filename_explicit,destination,temp_path,mime_type,etag,
	last_modified,size,downloaded,range_supported,connections,retries,status,error,queue_id,
	speed_limit,expected_checksum,created_at,started_at,completed_at,updated_at`

// Save upserts the download and replaces its headers in one transaction.
// Credential headers are never written, even if a caller passes them.
func (r *Downloads) Save(ctx context.Context, d *domain.Download) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO downloads(`+downloadColumns+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET url=excluded.url, final_url=excluded.final_url,
		filename=excluded.filename, filename_explicit=excluded.filename_explicit,
		destination=excluded.destination, temp_path=excluded.temp_path, mime_type=excluded.mime_type,
		etag=excluded.etag, last_modified=excluded.last_modified, size=excluded.size,
		downloaded=excluded.downloaded, range_supported=excluded.range_supported,
		connections=excluded.connections, retries=excluded.retries, status=excluded.status,
		error=excluded.error, queue_id=excluded.queue_id, speed_limit=excluded.speed_limit,
		expected_checksum=excluded.expected_checksum, started_at=excluded.started_at,
		completed_at=excluded.completed_at, updated_at=excluded.updated_at`,
		d.ID, d.URL, d.FinalURL, d.Filename, d.FilenameExplicit, d.Destination, d.TempPath, d.MIMEType,
		d.ETag, d.LastModified, d.Size, d.Downloaded, d.RangeSupported, d.Connections, d.Retries,
		string(d.Status), d.Error, nullableString(d.QueueID), d.SpeedLimit, nullableString(d.ExpectedChecksum),
		stamp(d.CreatedAt), nullableStamp(d.StartedAt),
		nullableStamp(d.CompletedAt), stamp(d.UpdatedAt))
	if err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM download_headers WHERE download_id=?`, d.ID); err != nil {
		return err
	}
	if len(d.Headers) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO download_headers(download_id,name,value) VALUES(?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for name, value := range d.Headers {
			if domain.IsSensitiveHeader(name) {
				continue
			}
			if _, err = stmt.ExecContext(ctx, d.ID, name, value); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Get returns one download or an error wrapping domain.ErrNotFound.
func (r *Downloads) Get(ctx context.Context, id string) (*domain.Download, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+downloadColumns+` FROM downloads WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanDownloads(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, id)
	}
	if err = r.attachHeaders(ctx, list, id); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// List returns all downloads, newest first.
func (r *Downloads) List(ctx context.Context) ([]domain.Download, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+downloadColumns+` FROM downloads ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	list, err := scanDownloads(rows)
	if err != nil {
		return nil, err
	}
	return list, r.attachHeaders(ctx, list, "")
}

// Delete removes a download; segments and headers cascade.
func (r *Downloads) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM downloads WHERE id=?`, id)
	return err
}

func scanDownloads(rows *sql.Rows) ([]domain.Download, error) {
	defer rows.Close()
	out := []domain.Download{}
	for rows.Next() {
		var d domain.Download
		var finalURL, mime, etag, lastModified, errText, queueID, status, checksum sql.NullString
		var created, started, completed, updated sql.NullString
		if err := rows.Scan(&d.ID, &d.URL, &finalURL, &d.Filename, &d.FilenameExplicit, &d.Destination,
			&d.TempPath, &mime, &etag, &lastModified, &d.Size, &d.Downloaded, &d.RangeSupported,
			&d.Connections, &d.Retries, &status, &errText, &queueID, &d.SpeedLimit, &checksum,
			&created, &started, &completed, &updated); err != nil {
			return nil, err
		}
		d.ExpectedChecksum = checksum.String
		d.FinalURL, d.MIMEType, d.ETag = finalURL.String, mime.String, etag.String
		d.LastModified, d.Error, d.QueueID = lastModified.String, errText.String, queueID.String
		d.Status = domain.Status(status.String)
		if !d.Status.Valid() {
			return nil, fmt.Errorf("download %s has unknown status %q", d.ID, status.String)
		}
		var err error
		if d.CreatedAt, err = parseStamp("created_at", created); err != nil {
			return nil, err
		}
		if d.StartedAt, err = parseStamp("started_at", started); err != nil {
			return nil, err
		}
		if d.CompletedAt, err = parseStamp("completed_at", completed); err != nil {
			return nil, err
		}
		if d.UpdatedAt, err = parseStamp("updated_at", updated); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// attachHeaders loads headers for the listed downloads in one query.
func (r *Downloads) attachHeaders(ctx context.Context, list []domain.Download, onlyID string) error {
	if len(list) == 0 {
		return nil
	}
	query, args := `SELECT download_id,name,value FROM download_headers`, []any{}
	if onlyID != "" {
		query, args = query+` WHERE download_id=?`, append(args, onlyID)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := make(map[string]*domain.Download, len(list))
	for i := range list {
		index[list[i].ID] = &list[i]
	}
	for rows.Next() {
		var id, name, value string
		if err = rows.Scan(&id, &name, &value); err != nil {
			return err
		}
		if d := index[id]; d != nil {
			if d.Headers == nil {
				d.Headers = map[string]string{}
			}
			d.Headers[name] = value
		}
	}
	return rows.Err()
}
