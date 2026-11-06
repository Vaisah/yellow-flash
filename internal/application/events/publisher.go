// Package events defines the notifications the engine emits. It is a leaf
// package so the engine and any delivery mechanism (Wails, CLI, tests) can
// share it without depending on each other.
package events

import "github.com/vaisah/internal/domain/download"

// Type identifies an event.
type Type string

const (
	// DownloadChanged is emitted on every state or metadata change.
	DownloadChanged Type = "download.changed"
	// ProgressUpdated is emitted periodically (throttled) while transferring.
	ProgressUpdated Type = "download.progress"
)

// Event carries a snapshot of the download and, for ProgressUpdated, its
// transfer statistics. Snapshots never contain credentials.
type Event struct {
	Type     Type              `json:"type"`
	Download download.Download `json:"download"`
	Progress download.Progress `json:"progress"`
}

// Publisher receives events. Implementations must return quickly and must
// not call back into the engine synchronously.
type Publisher interface {
	Publish(Event)
}

// PublisherFunc adapts a function to Publisher.
type PublisherFunc func(Event)

// Publish calls f.
func (f PublisherFunc) Publish(e Event) { f(e) }

// Nop discards events.
type Nop struct{}

// Publish does nothing.
func (Nop) Publish(Event) {}
