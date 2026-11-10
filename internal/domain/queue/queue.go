// Ported from Mister Download Manager, crates/app/src/queue.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package queue models download queues and their schedules: a queue
// downloads N files at a time in list order; a file that fails moves to
// the end and is retried 30 s later (or up to a retry limit); finished files
// leave download queues (a synchronization queue keeps them); a queue with
// nothing left to start or running stops.
package queue

import "time"

// RetryDelay is how long a failed file waits before its queue tries it
// again ("IDM will retry … with 30 seconds interval").
const RetryDelay = 30 * time.Second

// DefaultFilesAtOnce is "Download 4 files at the same time".
const DefaultFilesAtOnce = 4

// The built-in queues, which cannot be deleted.
const (
	MainID = "main"
	SyncID = "sync"
)

// TimeOfDay is a clock time.
type TimeOfDay struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
	Second int `json:"second"`
}

// Seconds is the time as seconds since midnight.
func (t TimeOfDay) Seconds() int { return t.Hour*3600 + t.Minute*60 + t.Second }

// Date is a calendar day.
type Date struct {
	Year  int        `json:"year"`
	Month time.Month `json:"month"`
	Day   int        `json:"day"`
}

// DateOf returns t's day.
func DateOf(t time.Time) Date { y, m, d := t.Date(); return Date{y, m, d} }

// TurnOff is what the computer does when a queue is done.
type TurnOff string

// Turn-off modes.
const (
	ShutDown  TurnOff = "shutdown"
	Sleep     TurnOff = "sleep"
	Hibernate TurnOff = "hibernate"
)

// Schedule starts and stops a queue by the clock and says what happens when
// it is done.
type Schedule struct {
	StartOnStartup bool       `json:"startOnStartup,omitempty"`
	StartAt        *TimeOfDay `json:"startAt,omitempty"`
	// Days are the weekdays StartAt applies to, Sunday first ("Daily").
	Days   [7]bool    `json:"days"`
	StopAt *TimeOfDay `json:"stopAt,omitempty"`
	// Once is "Once at" this date instead of Daily.
	Once *Date `json:"once,omitempty"`
	// Retries is "Number of retries for each file if downloading failed";
	// nil retries forever.
	Retries *int `json:"retries,omitempty"`
	// When the queue is done.
	OpenWhenDone string  `json:"openWhenDone,omitempty"`
	ExitWhenDone bool    `json:"exitWhenDone,omitempty"`
	TurnOff      TurnOff `json:"turnOff,omitempty"`
	ForceClose   bool    `json:"forceClose,omitempty"`
	// Wake is "Wake the computer to run this queue" (kept; waking is not
	// implemented yet).
	Wake bool `json:"wake,omitempty"`
}

// DefaultSchedule is IDM's: nothing checked, all days.
func DefaultSchedule() Schedule {
	return Schedule{Days: [7]bool{true, true, true, true, true, true, true}}
}

// StartsOn reports whether StartAt applies on day: "Once at" its date, else
// Daily on the checked weekdays.
func (s Schedule) StartsOn(day Date, weekday time.Weekday) bool {
	if s.Once != nil {
		return *s.Once == day
	}
	return s.Days[int(weekday)%7]
}

// Queue is a named, ordered group of downloads.
type Queue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// FilesAtOnce is "Download N files at the same time" (0: the default).
	FilesAtOnce int `json:"filesAtOnce"`
	// Items are download IDs in queue order.
	Items []string `json:"items"`
	// Sync queues keep finished files.
	Sync      bool      `json:"sync,omitempty"`
	Schedule  Schedule  `json:"schedule"`
	CreatedAt time.Time `json:"createdAt"`
	// Running is the queue's state while the app runs; never saved.
	Running bool `json:"-"`
}

// Builtin reports one of the two queues that cannot be deleted.
func (q Queue) Builtin() bool { return q.ID == MainID || q.ID == SyncID }

// Limit is FilesAtOnce, at least 1.
func (q Queue) Limit() int {
	if q.FilesAtOnce < 1 {
		return DefaultFilesAtOnce
	}
	return q.FilesAtOnce
}

// Clone returns a copy that shares nothing.
func (q Queue) Clone() Queue {
	q.Items = append([]string(nil), q.Items...)
	if q.Schedule.StartAt != nil {
		t := *q.Schedule.StartAt
		q.Schedule.StartAt = &t
	}
	if q.Schedule.StopAt != nil {
		t := *q.Schedule.StopAt
		q.Schedule.StopAt = &t
	}
	if q.Schedule.Once != nil {
		d := *q.Schedule.Once
		q.Schedule.Once = &d
	}
	if q.Schedule.Retries != nil {
		n := *q.Schedule.Retries
		q.Schedule.Retries = &n
	}
	return q
}

// MemberState is what a queue needs to know about one of its downloads.
type MemberState int

// Member states.
const (
	// Waiting: not started, or stopped; can start now.
	Waiting MemberState = iota
	Running
	Complete
	// Failed: failed Tries times, last At.
	Failed
)

// Member is one download's state for its queue.
type Member struct {
	State MemberState
	At    time.Time // Failed: when it last failed
	Tries int       // Failed: how often
}

// ToStart returns the IDs to start now: in queue order, up to the queue's
// limit running at once. Failed files wait RetryDelay and are skipped after
// the schedule's retries.
func ToStart(q Queue, member func(string) Member, now time.Time) []string {
	running := 0
	for _, id := range q.Items {
		if member(id).State == Running {
			running++
		}
	}
	free := q.Limit() - running
	var out []string
	for _, id := range q.Items {
		if free <= 0 {
			break
		}
		m := member(id)
		ready := m.State == Waiting ||
			m.State == Failed && (q.Schedule.Retries == nil || m.Tries < *q.Schedule.Retries) && now.Sub(m.At) >= RetryDelay
		if ready {
			out = append(out, id)
			free--
		}
	}
	return out
}

// Finished reports a queue with nothing running and nothing it may still
// start (now or after a retry delay).
func Finished(q Queue, member func(string) Member) bool {
	for _, id := range q.Items {
		m := member(id)
		switch {
		case m.State == Complete:
		case m.State == Failed && q.Schedule.Retries != nil && m.Tries >= *q.Schedule.Retries:
		default:
			return false
		}
	}
	return true
}

// RequeueFailed moves a failed file to the end of the queue.
func RequeueFailed(q *Queue, id string) {
	for i, x := range q.Items {
		if x == id {
			q.Items = append(append(q.Items[:i:i], q.Items[i+1:]...), id)
			return
		}
	}
}

// Crossed reports whether the clock passed t between prev and now (seconds
// since midnight), across midnight too.
func Crossed(t TimeOfDay, prev, now int) bool {
	s := t.Seconds()
	if prev <= now {
		return prev < s && s <= now
	}
	return s > prev || s <= now
}
