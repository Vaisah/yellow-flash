// Ported from Mister Download Manager, crates/app/src/store.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package jsonstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/domain/queue"
)

var ctx = context.Background()

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func download(id string, created time.Time) *domain.Download {
	return &domain.Download{ID: id, URL: "https://x/" + id, Filename: id + ".bin", Destination: "/dl",
		TempPath: "/dl/" + id + ".bin.part", Size: 100, Status: domain.Queued, CreatedAt: created, UpdatedAt: created}
}

func TestDownloadRoundTripHeadersAndNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	s := open(t, path)
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	d := download("a", now)
	d.Headers = map[string]string{"Referer": "https://page/", "Authorization": "Bearer secret", "Cookie": "s=1"}
	d.SpeedLimit, d.ExpectedChecksum, d.FilenameExplicit = 4096, "sha256:00", true
	if err := s.Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, download("b", now.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "s=1") {
		t.Fatal("credentials were written to the list")
	}

	s = open(t, path)
	defer s.Close()
	got, err := s.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Headers["Referer"] != "https://page/" || len(got.Headers) != 1 {
		t.Fatalf("headers %v", got.Headers)
	}
	if got.SpeedLimit != 4096 || got.ExpectedChecksum != "sha256:00" || !got.FilenameExplicit || !got.CreatedAt.Equal(now) {
		t.Fatalf("round trip %+v", got)
	}
	list, _ := s.List(ctx)
	if len(list) != 2 || list[0].ID != "b" {
		t.Fatalf("newest first: %+v", list)
	}
	if _, err = s.Get(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err = s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted download still there")
	}
}

func TestSegmentsAreDurableAtOnceAndGoWithTheirDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	s := open(t, path)
	if err := s.SaveSegments(ctx, "ghost", []domain.Segment{{DownloadID: "ghost"}}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segments of an unknown download: %v", err)
	}
	_ = s.Save(ctx, download("a", time.Now()))
	seg := func(i int, done int64) domain.Segment {
		return domain.Segment{ID: fmt.Sprintf("a-%d", i), DownloadID: "a", Index: i, Start: int64(i) * 50,
			End: int64(i)*50 + 49, Downloaded: done, Status: domain.SegmentPending}
	}
	if err := s.SaveSegments(ctx, "a", []domain.Segment{seg(0, 10), seg(1, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSegments(ctx, "a", []domain.Segment{seg(1, 25)}); err != nil {
		t.Fatal(err)
	}
	// On disk before any list save: a fresh reader of the resume file sees it.
	fresh := &Store{resumeDir: filepath.Join(filepath.Dir(path), "resume"), segments: map[string]map[int]domain.Segment{}}
	m, err := fresh.segmentsLocked("a")
	if err != nil || len(m) != 2 || m[0].Downloaded != 10 || m[1].Downloaded != 25 {
		t.Fatalf("resume file: %+v %v", m, err)
	}
	if err = s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(s.resumePath("a")); !os.IsNotExist(err) {
		t.Fatal("resume file outlived its download")
	}
	_ = s.Close()
}

func TestSettingsQueuesAndHostLimitsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	s := open(t, path)
	_ = s.Settings().Set(ctx, "ui.connections", "8")
	_ = s.Queues().Create(ctx, &queue.Queue{ID: "q1", Name: "Main", MaxConcurrent: 3, CreatedAt: time.Now()})
	d := download("a", time.Now())
	d.QueueID = "q1"
	_ = s.Save(ctx, d)
	_ = s.SaveHostLimit(ctx, domain.HostLimit{Host: "x.org", MaxConnections: 2, Seen: 2, UpdatedAt: time.Now()})
	_ = s.Close()

	s = open(t, path)
	defer s.Close()
	if v, ok, _ := s.Settings().Get(ctx, "ui.connections"); !ok || v != "8" {
		t.Fatalf("setting %q %v", v, ok)
	}
	if qs, _ := s.Queues().List(ctx); len(qs) != 1 || qs[0].MaxConcurrent != 3 {
		t.Fatalf("queues %+v", qs)
	}
	if ls, _ := s.LoadHostLimits(ctx); len(ls) != 1 || ls[0].MaxConnections != 2 {
		t.Fatalf("host limits %+v", ls)
	}
	_ = s.Queues().Delete(ctx, "q1")
	if got, _ := s.Get(ctx, "a"); got.QueueID != "" {
		t.Fatal("a deleted queue's downloads keep no queue")
	}
}

func TestTheSaverWritesTheNewestListOffTheCallingGoroutine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	s := open(t, path)
	for i := range 50 {
		_ = s.Settings().Set(ctx, "n", fmt.Sprint(i))
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"n": "49"`) {
		t.Fatalf("not the newest list: %s", raw)
	}
	_ = s.Close()
	if err := s.Save(ctx, download("late", time.Now())); !errors.Is(err, ErrClosed) {
		t.Fatalf("a closed store takes nothing: %v", err)
	}
}

func setN(t *testing.T, path string, n int) {
	t.Helper()
	s := open(t, path)
	_ = s.Settings().Set(ctx, "n", fmt.Sprint(n))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func readN(t *testing.T, path string) string {
	t.Helper()
	s := open(t, path)
	defer s.Close()
	v, _, _ := s.Settings().Get(ctx, "n")
	return v
}

func generationN(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := parse(raw)
	if !ok {
		t.Fatalf("%s does not parse", path)
	}
	return f.Settings["n"]
}

func TestADamagedListFallsBackToThePreviousGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "downloads.json")
	setN(t, path, 7)
	setN(t, path, 9)
	if got := readN(t, path); got != "9" {
		t.Fatalf("got %s", got)
	}
	_ = os.WriteFile(path, []byte("{ not json"), 0o600)
	if got := readN(t, path); got != "9" && got != "7" {
		t.Fatalf("the backup holds a generation before the damage: %s", got)
	}
	entries, _ := os.ReadDir(dir)
	kept := false
	for _, e := range entries {
		kept = kept || strings.Contains(e.Name(), ".corrupt-")
	}
	if !kept {
		t.Fatal("damaged file not kept aside")
	}
}

func TestOlderGenerationsRotateAtMostEveryTenMinutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "downloads.json")
	s := &Store{path: path, now: time.Now}
	setN(t, path, 1)
	setN(t, path, 2) // .1 = list 1
	setN(t, path, 3) // too soon: .1 stays list 1
	if got := generationN(t, s.generationPath(1)); got != "1" {
		t.Fatalf(".1 = %s", got)
	}
	old := time.Now().Add(-11 * time.Minute)
	_ = os.Chtimes(s.generationPath(1), old, old)
	setN(t, path, 4) // .1 = list 3, .2 = list 1
	if a, b, c := generationN(t, s.generationPath(1)), generationN(t, s.generationPath(2)), generationN(t, s.backupPath()); a != "3" || b != "1" || c != "3" {
		t.Fatalf(".1=%s .2=%s .bak=%s", a, b, c)
	}
}

func TestLoadingFallsBackThroughTheGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	setN(t, path, 1)
	setN(t, path, 2)
	_ = os.WriteFile(path, []byte("broken"), 0o600)
	_ = os.WriteFile(path+".bak", []byte("broken too"), 0o600)
	if got := readN(t, path); got != "1" {
		t.Fatalf("from .1.bak: %s", got)
	}
}

func TestADamagedFileNeverReplacesAGoodBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	setN(t, path, 3)
	setN(t, path, 4)
	s := open(t, path)
	_ = os.WriteFile(path, []byte("garbage"), 0o600) // damaged while running
	_ = s.Settings().Set(ctx, "n", "5")
	_ = s.Close()
	if got := generationN(t, path+".bak"); got != "3" {
		t.Fatalf(".bak = %s", got)
	}
}

func TestAnUnreadableListIsNeverSavedOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	if err := os.Mkdir(path, 0o700); err != nil { // there, but reading it fails
		t.Fatal(err)
	}
	s := open(t, path)
	_ = s.Settings().Set(ctx, "n", "1")
	if err := s.Close(); !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatal("the unreadable list was replaced")
	}
}

func TestAByteOrderMarkIsTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	_ = os.WriteFile(path, []byte("\xef\xbb\xbf{\"version\":1,\"downloads\":[],\"settings\":{\"n\":\"bom\"}}"), 0o600)
	if got := readN(t, path); got != "bom" {
		t.Fatalf("got %q", got)
	}
}

// MDM #69: thousands of downloads save and load quickly.
func TestAListOfThousandsSavesAndLoadsQuickly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "downloads.json")
	s := open(t, path)
	base := time.Now()
	for i := range 6000 {
		_ = s.Save(ctx, download(fmt.Sprintf("v%06d", i), base.Add(time.Duration(i))))
	}
	start := time.Now()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	saved := time.Since(start)
	start = time.Now()
	s = open(t, path)
	defer s.Close()
	loaded := time.Since(start)
	if list, _ := s.List(ctx); len(list) != 6000 {
		t.Fatalf("%d downloads", len(list))
	}
	t.Logf("6000 downloads: saved in %v, loaded in %v", saved, loaded)
	if saved > 3*time.Second || loaded > 3*time.Second {
		t.Fatalf("too slow: %v / %v", saved, loaded)
	}
}
