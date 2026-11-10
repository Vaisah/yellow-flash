// Ported from Mister Download Manager, crates/engine/src/{hosts,limiter,disk,verify}.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/platform"
)

/* ---------- hosts.rs ---------- */

func TestRefusalsTeachTheMostThatWorked(t *testing.T) {
	h := newHostLimits(hostLimitDecay)
	if _, applies := h.refused("x.org", 1); applies {
		t.Fatal("one refusal can be a hiccup")
	}
	if _, ok := h.limit("x.org"); ok {
		t.Fatal("no cap after one refusal")
	}
	// A refusal while only one other was receiving doesn't hide that two worked before.
	if l, applies := h.refused("x.org", 2); !applies || l.MaxConnections != 2 {
		t.Fatalf("second refusal: %+v %v", l, applies)
	}
	if l, _ := h.refused("x.org", 1); l.MaxConnections != 2 {
		t.Fatalf("fewer never lowers it: %+v", l)
	}
	// More seen working later raises it; fewer never lowers it.
	h.working("x.org", 3)
	h.working("x.org", 1)
	if n, _ := h.limit("x.org"); n != 3 {
		t.Fatalf("cap %d, want 3", n)
	}
	h.working("y.org", 5)
	if _, ok := h.limit("y.org"); ok {
		t.Fatal("nothing refused there: no cap")
	}
	if got := h.learned(); len(got) != 1 || got[0].Host != "x.org" || got[0].MaxConnections != 3 {
		t.Fatalf("learned %+v", got)
	}
}

func TestLearnedLimitsDecayAndReload(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	h := newHostLimits(hostLimitDecay)
	h.now = func() time.Time { return now }
	h.refused("x.org", 2)
	h.refused("x.org", 2)
	saved := h.learned()

	again := newHostLimits(hostLimitDecay)
	again.now = func() time.Time { return now.Add(time.Minute) }
	again.load(saved)
	if n, ok := again.limit("x.org"); !ok || n != 2 {
		t.Fatalf("reloaded: %d %v", n, ok)
	}
	again.now = func() time.Time { return now.Add(hostLimitDecay) }
	if _, ok := again.limit("x.org"); ok {
		t.Fatal("forgotten after the decay")
	}
	stale := newHostLimits(hostLimitDecay)
	stale.now = func() time.Time { return now.Add(time.Hour) }
	stale.load(saved)
	if len(stale.learned()) != 0 {
		t.Fatal("a stale record is not loaded")
	}
}

/* ---------- limiter.rs ---------- */

func TestLimiterLimitsAverageRate(t *testing.T) {
	l := NewLimiter(400 << 10)
	start := time.Now()
	for range 20 {
		if err := l.Wait(context.Background(), 10<<10); err != nil { // 200 KB at 400 KB/s ≈ 0.5 s
			t.Fatal(err)
		}
	}
	if secs := time.Since(start).Seconds(); secs < 0.4 || secs > 0.75 {
		t.Fatalf("took %.2fs", secs)
	}
}

func TestLimiterUnlimitedAndChangeable(t *testing.T) {
	l := NewLimiter(0)
	start := time.Now()
	_ = l.Wait(context.Background(), 100<<20)
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("unlimited waited")
	}
	l.Set(1024)
	if l.Rate() != 1024 {
		t.Fatal("rate")
	}
	l.Set(0)
	if l.Rate() != 0 {
		t.Fatal("unset")
	}
	var nilLimiter *Limiter
	if nilLimiter.Wait(context.Background(), 1) != nil {
		t.Fatal("a nil limiter limits nothing")
	}
}

func TestLimiterCountsWhatWentThrough(t *testing.T) {
	l := NewLimiter(0)
	_ = l.Wait(context.Background(), 1000)
	l.Set(1 << 30)
	_ = l.Wait(context.Background(), 500)
	l.Refund(200)
	if l.Taken() != 1300 {
		t.Fatalf("taken %d: limited or not, less what was given back", l.Taken())
	}
}

func TestLimiterWaitEndsWithTheContext(t *testing.T) {
	l := NewLimiter(1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx, 1<<20); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

/* ---------- disk.rs ---------- */

func TestDiskNeedsTheFilePlusAMargin(t *testing.T) {
	ntfs := func(free uint64) platform.Volume { return platform.Volume{Free: free, FileSystem: "NTFS"} }
	if err := checkVolume(ntfs(10<<30), 1<<30, 1<<30); err != nil {
		t.Fatal(err)
	}
	if err := checkVolume(ntfs(1<<30+diskMargin-1), 1<<30, 1<<30); !errors.Is(err, domain.ErrNoSpace) {
		t.Fatalf("got %v", err)
	}
	// Only what is still to be written counts.
	if err := checkVolume(ntfs(diskMargin+10), 1<<30, 10); err != nil {
		t.Fatal(err)
	}
}

func TestFAT32RefusesFourGiBFiles(t *testing.T) {
	for _, fs := range []string{"FAT32", "vfat", "msdos"} {
		v := platform.Volume{Free: 64 << 30, FileSystem: fs}
		if err := checkVolume(v, fat32Max, fat32Max); err != nil {
			t.Fatalf("%s: %v", fs, err)
		}
		if err := checkVolume(v, 4<<30, 4<<30); !errors.Is(err, domain.ErrTooBigForFileSystem) {
			t.Fatalf("%s: %v", fs, err)
		}
	}
	if err := checkVolume(platform.Volume{Free: 64 << 30, FileSystem: "exFAT"}, 8<<30, 8<<30); err != nil {
		t.Fatal(err)
	}
	// Unknown: the space is still checked.
	if err := checkVolume(platform.Volume{Free: 1 << 30}, 8<<30, 8<<30); err == nil {
		t.Fatal("space not checked")
	}
}

func TestSizesReadLikeIDMs(t *testing.T) {
	for n, want := range map[int64]string{512: "512 bytes", 1536: "1.50 KB", 4 << 30: "4.00 GB", 150 << 20: "150 MB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestVolumeOfARealAndAMissingFolder(t *testing.T) {
	dir := t.TempDir()
	v, ok := platform.VolumeOf(dir)
	if !ok {
		t.Skip("no volume probe on this system")
	}
	if v.Free == 0 {
		t.Fatalf("%+v", v)
	}
	missing, ok := platform.VolumeOf(filepath.Join(dir, "not", "yet"))
	if !ok || missing.FileSystem != v.FileSystem {
		t.Fatalf("missing folder: %+v %v, parent %+v", missing, ok, v)
	}
	if err := checkSpace(dir, 1, 1); err != nil {
		t.Fatal(err)
	}
}

/* ---------- verify.rs ---------- */

func TestDigestsOfAFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fox.txt")
	if err := os.WriteFile(p, []byte("The quick brown fox jumps over the lazy dog"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"d7a8fbb307d7809469ca9abcb0082e4f8d5651e46d3cdb762d02d0bf37c9e592",
		"9e107d9d372bb6826bd81d3542a419d6",
		"crc32:414fa339",
		"sha1:2fd4e1c67a2d28fced849ee1bb76e7391b93eb12",
		"07e547d9586f6a73f73fbac0435ed76951218fb7d0c8d788a309d785436bbb642e93a252a954f23912547d1e8a3b5ed6e1bfd7097821233fa0538f3db854fee6",
	} {
		c, err := domain.ParseChecksum(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if err = verifyChecksum(context.Background(), p, c); err != nil {
			t.Fatalf("%s: %v", c.Algorithm, err)
		}
	}
	wrong, _ := domain.ParseChecksum("00000000000000000000000000000000")
	if err := verifyChecksum(context.Background(), p, wrong); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("wrong hash: %v", err)
	}
}
