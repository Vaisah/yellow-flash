// Ported from Mister Download Manager, crates/app/src/queue.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package queue

import (
	"fmt"
	"testing"
	"time"
)

func q(n int, items ...string) Queue {
	return Queue{ID: MainID, Name: "Main download queue", FilesAtOnce: n, Items: items, Schedule: DefaultSchedule()}
}

func TestStartsInOrderUpToTheLimit(t *testing.T) {
	now := time.Now()
	st := func(id string) Member {
		if id == "1" {
			return Member{State: Running}
		}
		return Member{State: Waiting}
	}
	for n, want := range map[int]string{2: "[2]", 3: "[2 3]", 1: "[]"} {
		if got := fmt.Sprint(ToStart(q(n, "1", "2", "3"), st, now)); got != want {
			t.Errorf("limit %d: %s, want %s", n, got, want)
		}
	}
}

func TestFailedFilesWaitAndRespectTheRetryLimit(t *testing.T) {
	t0 := time.Now()
	failed := func(tries int) func(string) Member {
		return func(string) Member { return Member{State: Failed, At: t0, Tries: tries} }
	}
	if len(ToStart(q(1, "7"), failed(1), t0)) != 0 {
		t.Fatal("30 s delay")
	}
	if got := ToStart(q(1, "7"), failed(1), t0.Add(RetryDelay)); len(got) != 1 {
		t.Fatalf("after the delay: %v", got)
	}
	limited := q(1, "7")
	two := 2
	limited.Schedule.Retries = &two
	if len(ToStart(limited, failed(2), t0.Add(RetryDelay))) != 0 || !Finished(limited, failed(2)) {
		t.Fatal("retry limit")
	}
	if Finished(q(1, "7"), failed(9)) {
		t.Fatal("no limit: keeps retrying")
	}
}

func TestOnceAtADateOrDailyOnCheckedDays(t *testing.T) {
	s := DefaultSchedule()
	s.Days[0] = false // not on Sundays
	if s.StartsOn(Date{2026, 11, 1}, time.Sunday) || !s.StartsOn(Date{2026, 11, 2}, time.Monday) {
		t.Fatal("daily")
	}
	s.Once = &Date{2026, 11, 3}
	if s.StartsOn(Date{2026, 11, 2}, time.Monday) || !s.StartsOn(Date{2026, 11, 3}, time.Tuesday) {
		t.Fatal("only on its date")
	}
}

func TestFailedFileMovesToTheEnd(t *testing.T) {
	qq := q(1, "1", "2", "3")
	RequeueFailed(&qq, "1")
	if fmt.Sprint(qq.Items) != "[2 3 1]" {
		t.Fatal(qq.Items)
	}
}

func TestScheduleTimeCrossing(t *testing.T) {
	elevenPM := TimeOfDay{23, 0, 0}
	if !Crossed(elevenPM, 82_799, 82_800) || Crossed(elevenPM, 82_800, 82_801) {
		t.Fatal("at 11 PM")
	}
	if !Crossed(TimeOfDay{0, 0, 5}, 86_398, 6) {
		t.Fatal("across midnight")
	}
}
