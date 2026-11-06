package download

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidTransition is returned (wrapped) when a state change is illegal.
	ErrInvalidTransition = errors.New("invalid download state transition")
	// ErrNotFound is returned when a download does not exist.
	ErrNotFound = errors.New("download not found")
	// ErrRemoteChanged indicates the remote representation no longer matches
	// the bytes already downloaded.
	ErrRemoteChanged = errors.New("remote resource changed")
	// ErrInvalidRequest is returned for malformed create requests.
	ErrInvalidRequest = errors.New("invalid download request")
	// ErrShuttingDown is returned when work is submitted during shutdown.
	ErrShuttingDown = errors.New("download manager is shutting down")
)

// TransitionError describes a rejected state change.
type TransitionError struct{ From, To Status }

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s: %s -> %s", ErrInvalidTransition, e.From, e.To)
}

// Unwrap lets errors.Is match ErrInvalidTransition.
func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }
