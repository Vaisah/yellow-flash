package repositories

import (
	"context"
	"database/sql"

	"github.com/vaisah/internal/domain/queue"
)

// Queues persists download queues. Scheduling arrives in Phase 3.
type Queues struct{ db *sql.DB }

// NewQueues returns a repository bound to db.
func NewQueues(db *sql.DB) *Queues { return &Queues{db: db} }

// Create inserts a queue.
func (r *Queues) Create(ctx context.Context, q *queue.Queue) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO queues(id,name,max_concurrent,created_at) VALUES(?,?,?,?)`,
		q.ID, q.Name, q.MaxConcurrent, stamp(q.CreatedAt))
	return err
}

// List returns queues in creation order.
func (r *Queues) List(ctx context.Context) ([]queue.Queue, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,max_concurrent,created_at FROM queues ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []queue.Queue{}
	for rows.Next() {
		var q queue.Queue
		var created sql.NullString
		if err = rows.Scan(&q.ID, &q.Name, &q.MaxConcurrent, &created); err != nil {
			return nil, err
		}
		if q.CreatedAt, err = parseStamp("created_at", created); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Delete removes a queue; member downloads keep existing with no queue.
func (r *Queues) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM queues WHERE id=?`, id)
	return err
}
