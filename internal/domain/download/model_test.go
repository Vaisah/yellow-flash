package download

import (
	"errors"
	"testing"
)

func TestTransitionTableIsExhaustive(t *testing.T) {
	legal := map[[2]Status]bool{}
	for from, tos := range transitions {
		for _, to := range tos {
			legal[[2]Status{from, to}] = true
		}
	}
	for _, from := range AllStatuses() {
		for _, to := range AllStatuses() {
			d := &Download{Status: from}
			err := d.Transition(to)
			if legal[[2]Status{from, to}] {
				if err != nil || d.Status != to {
					t.Errorf("%s -> %s should be legal: %v", from, to, err)
				}
				continue
			}
			if !errors.Is(err, ErrInvalidTransition) || d.Status != from {
				t.Errorf("%s -> %s should be rejected, got err=%v status=%s", from, to, err, d.Status)
			}
		}
	}
}

func TestLifecyclePaths(t *testing.T) {
	paths := [][]Status{
		{Queued, Initializing, Downloading, Verifying, Completed},
		{Queued, Initializing, Downloading, Pausing, Paused, Initializing, Downloading, Verifying, Completed},
		{Queued, Initializing, Failed, Initializing, Downloading, Cancelled},
		{Queued, Initializing, Pausing, Paused, Cancelled},
	}
	for _, path := range paths {
		d := &Download{Status: path[0]}
		for _, next := range path[1:] {
			if err := d.Transition(next); err != nil {
				t.Fatalf("path %v: %v", path, err)
			}
		}
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []Status{Completed, Cancelled} {
		if !s.IsTerminal() || len(transitions[s]) != 0 {
			t.Errorf("%s must be terminal", s)
		}
	}
	if CanTransition(Completed, Initializing) || CanTransition(Verifying, Initializing) || CanTransition(Downloading, Completed) {
		t.Fatal("unexpected shortcut transition")
	}
	if Status("BOGUS").Valid() {
		t.Fatal("unknown status reported valid")
	}
}

func TestMarkInterrupted(t *testing.T) {
	for _, s := range AllStatuses() {
		d := &Download{Status: s}
		changed := d.MarkInterrupted()
		if changed != s.IsActive() {
			t.Errorf("%s: changed=%v", s, changed)
		}
		if s.IsActive() && d.Status != Paused {
			t.Errorf("%s: got %s", s, d.Status)
		}
	}
}

func TestSegmentArithmeticAndHeaders(t *testing.T) {
	s := Segment{Start: 10, End: 19, Downloaded: 4}
	if s.Length() != 10 || s.Remaining() != 6 {
		t.Fatalf("length=%d remaining=%d", s.Length(), s.Remaining())
	}
	for _, h := range []string{"authorization", "Cookie", " proxy-authorization "} {
		if !IsSensitiveHeader(h) {
			t.Errorf("%q should be sensitive", h)
		}
	}
	if IsSensitiveHeader("Referer") {
		t.Error("Referer is not a credential")
	}
	d := &Download{Headers: map[string]string{"Referer": "x"}}
	c := d.Clone()
	c.Headers["Referer"] = "y"
	if d.Headers["Referer"] != "x" {
		t.Fatal("clone shares header map")
	}
}
