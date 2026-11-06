package download

import "time"

// Status is the lifecycle state of a download.
type Status string

// Download lifecycle states.
const (
	Queued       Status = "QUEUED"
	Initializing Status = "INITIALIZING"
	Downloading  Status = "DOWNLOADING"
	Pausing      Status = "PAUSING"
	Paused       Status = "PAUSED"
	Verifying    Status = "VERIFYING"
	Completed    Status = "COMPLETED"
	Failed       Status = "FAILED"
	Cancelled    Status = "CANCELLED"
)

// transitions is the complete set of legal state changes. Anything absent is
// rejected, including self-transitions, so every change is deliberate.
var transitions = map[Status][]Status{
	Queued:       {Initializing, Cancelled},
	Initializing: {Downloading, Verifying, Pausing, Failed, Cancelled},
	Downloading:  {Pausing, Verifying, Failed, Cancelled},
	Pausing:      {Paused, Failed, Cancelled},
	Paused:       {Initializing, Cancelled},
	Verifying:    {Completed, Failed, Paused},
	Failed:       {Initializing, Cancelled},
	Completed:    {},
	Cancelled:    {},
}

// AllStatuses lists every known state in lifecycle order.
func AllStatuses() []Status {
	return []Status{Queued, Initializing, Downloading, Pausing, Paused, Verifying, Completed, Failed, Cancelled}
}

// Valid reports whether s is a known state.
func (s Status) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// IsTerminal reports whether no further transitions are possible.
func (s Status) IsTerminal() bool { return s == Completed || s == Cancelled }

// IsActive reports whether a worker owns the download in this state.
func (s Status) IsActive() bool {
	return s == Initializing || s == Downloading || s == Pausing || s == Verifying
}

// CanTransition reports whether moving from one state to another is legal.
func CanTransition(from, to Status) bool {
	for _, allowed := range transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Transition moves the download to a new state, rejecting illegal changes.
// Callers must hold whatever lock guards the Download.
func (d *Download) Transition(to Status) error {
	if !CanTransition(d.Status, to) {
		return &TransitionError{From: d.Status, To: to}
	}
	d.Status = to
	d.UpdatedAt = time.Now().UTC()
	return nil
}

// MarkInterrupted converts a state left behind by a crashed or killed process
// into PAUSED so it can be resumed safely, and reports whether it changed
// anything. It is the only way out of an active state without the owning
// worker, which is exactly the crash-recovery situation.
func (d *Download) MarkInterrupted() bool {
	if !d.Status.IsActive() {
		return false
	}
	d.Status = Paused
	d.UpdatedAt = time.Now().UTC()
	return true
}
