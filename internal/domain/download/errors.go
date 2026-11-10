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
	// ErrNoSpace means the destination volume cannot hold the file.
	ErrNoSpace = errors.New("not enough free space")
	// ErrTooBigForFileSystem means the destination's file system cannot
	// store a file this large (FAT32: 4 GiB − 1).
	ErrTooBigForFileSystem = errors.New("file too large for the destination's file system")
)

// TransitionError describes a rejected state change.
type TransitionError struct{ From, To Status }

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s: %s -> %s", ErrInvalidTransition, e.From, e.To)
}

// Unwrap lets errors.Is match ErrInvalidTransition.
func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }
