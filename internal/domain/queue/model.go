// Package queue models named groups of downloads for scheduling.
package queue

import "time"

// Queue is a named group of downloads. A MaxConcurrent of 0 means unlimited.
// Scheduling is implemented in Phase 3; Phase 1 only persists queues.
type Queue struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	MaxConcurrent int       `json:"maxConcurrent"`
	CreatedAt     time.Time `json:"createdAt"`
}
