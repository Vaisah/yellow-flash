// Package repositories contains the SQLite implementations of the domain
// repository interfaces.
package repositories

import (
	"database/sql"
	"fmt"
	"time"
)

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullableStamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return stamp(t)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func parseStamp(column string, v sql.NullString) (time.Time, error) {
	if !v.Valid || v.String == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("corrupt %s timestamp %q: %w", column, v.String, err)
	}
	return t, nil
}
