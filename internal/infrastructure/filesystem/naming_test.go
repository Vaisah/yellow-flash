// Ported from Mister Download Manager, crates/engine/src/naming.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package filesystem

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mustSanitize(t *testing.T, in string) string {
	t.Helper()
	out, err := SanitizeFilename(in)
	if err != nil {
		t.Fatalf("SanitizeFilename(%q): %v", in, err)
	}
	return out
}

func TestAllReservedDeviceNames(t *testing.T) {
	for _, n := range []string{"CONIN$.txt", "conout$", "COM¹.log", "LPT³", "com9.txt", "nul", "COM0"} {
		if got := mustSanitize(t, n); !strings.HasPrefix(got, "_") {
			t.Errorf("%s → %s", n, got)
		}
	}
	for _, n := range []string{"COMPUTER.txt", "compact.txt", "console"} {
		if got := mustSanitize(t, n); got != n {
			t.Errorf("%s → %s", n, got)
		}
	}
}

func TestFormatCharactersAreRemoved(t *testing.T) {
	if got := mustSanitize(t, "invoice\u202Efdp.exe"); got != "invoicefdp.exe" {
		t.Fatalf("got %q", got)
	}
	for _, c := range []rune{0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D, 0x2066, 0x2067, 0x2068,
		0x2069, 0x200B, 0x200C, 0x200D, 0xFEFF, 0x061C, 0x00AD, 0xE0041} {
		if got := mustSanitize(t, "a"+string(c)+"b.txt"); got != "ab.txt" {
			t.Errorf("U+%04X: %q", c, got)
		}
	}
	if _, err := SanitizeFilename("\u202E\u200B"); err == nil {
		t.Fatal("only invisible characters: nothing is left")
	}
	if got := mustSanitize(t, "تقرير-été-日本.zip"); got != "تقرير-été-日本.zip" {
		t.Fatalf("visible text in any script is kept: %q", got)
	}
}

func TestSanitizes(t *testing.T) {
	for in, want := range map[string]string{
		"../../evil.exe": "evil.exe", "a<b>c:d?.txt": "a_b_c_d_.txt", "con.txt": "_con.txt",
		"COM1": "_COM1", "name. . ": "name",
	} {
		if got := mustSanitize(t, in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestNamesShrinkToFitADeepFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the 259-unit limit is Windows'")
	}
	deep := `C:\` + strings.Repeat("d", 200)
	name := FitName(deep, strings.Repeat("n", 100)+".zip")
	if !strings.HasSuffix(name, ".zip") || !PathFits(deep, name) {
		t.Fatalf("%q does not fit", name)
	}
	if got := FitName(`C:\dl`, "a.zip"); got != "a.zip" {
		t.Fatalf("short names stay: %q", got)
	}
	// A folder too deep for any name keeps it whole.
	deeper := `C:\` + strings.Repeat("d", 240)
	if got := FitName(deeper, "a.zip"); got != "a.zip" || PathFits(deeper, "a-longer-file-name.zip") {
		t.Fatalf("too deep: %q", got)
	}
}

func TestATokenNamedDownloadFitsItsFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the 259-unit limit is Windows'")
	}
	dir := `C:\Users\someone\Downloads`
	token := "ag1." + strings.Repeat("eyJhcyI6MTMzMTY5LCJjIjoiQU41WUMybVpz", 12)
	name := FitName(dir, mustSanitize(t, token))
	if !PathFits(dir, name) || filepath.Dir(filepath.Join(dir, name)) != dir {
		t.Fatalf("%q", name)
	}
	named := FitName(dir, mustSanitize(t, fmt.Sprintf("%s.zip", strings.Repeat("x", 300))))
	if !PathFits(dir, named) || filepath.Ext(named) != ".zip" {
		t.Fatalf("%q", named)
	}
}
