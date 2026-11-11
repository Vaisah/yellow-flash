// Ported from Mister Download Manager, crates/extract/src/tests.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package extract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

type entry struct {
	name string
	data []byte
}

func zipOf(t *testing.T, path string, entries []entry, flags uint16) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, e := range entries {
		w, err := z.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, Flags: flags})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(e.data)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func run(t *testing.T, archive, dest, password string, sub Subfolder) (Report, []string, error) {
	t.Helper()
	var seen []string
	r, err := Extract(archive, Policy{Dest: dest, Subfolder: sub, Password: password, Tool: FindSevenZip(),
		OnFile: func(p string) { seen = append(seen, p) }})
	return r, seen, err
}

// noStaging checks no staging folder is left beside the destination.
func noStaging(t *testing.T, dest string) {
	t.Helper()
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Fatalf("staging left behind: %s", e.Name())
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEntryPathsStayInside(t *testing.T) {
	ok := map[string]string{"a/b.txt": filepath.Join("a", "b.txt"), `a\b.txt`: filepath.Join("a", "b.txt"),
		"./a//b": filepath.Join("a", "b")}
	for in, want := range ok {
		if got, err := RelPath(in, 64); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"../evil", "a/../../b", `..\evil`, "/abs", `\abs`, `C:\x`, "C:x", "x:ads",
		"a.txt:Zone.Identifier", "", "./"} {
		if _, err := RelPath(bad, 64); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	// Device names become safe names, not refusals.
	if con, err := RelPath("CON.txt", 64); err != nil || con == "CON.txt" {
		t.Error(con, err)
	}
	if _, err := RelPath(strings.Repeat("a/", 65), 64); err == nil {
		t.Error("folders too deep")
	}
}

func TestVolumeNames(t *testing.T) {
	v, _ := VolumeKey("Movie.2024.part02.rar")
	if v.Base != "Movie.2024" || v.Kind != RarPart || v.Index != 2 {
		t.Error(v)
	}
	if v, _ := VolumeKey("a.PART1.RAR"); v.Index != 1 || !v.First() {
		t.Error(v)
	}
	if v, _ := VolumeKey("a.rar"); v.Kind != RarOld || v.Index != 1 {
		t.Error(v)
	}
	if v, _ := VolumeKey("a.r00"); v.Index != 2 {
		t.Error(v)
	}
	if v, _ := VolumeKey("a.r01"); v.Index != 3 {
		t.Error(v)
	}
	if v, _ := VolumeKey("pics.7z.003"); v.Base != "pics" || v.Kind != Split || v.Index != 3 {
		t.Error(v)
	}
	for _, no := range []string{"a.zip", "report.pdf"} {
		if _, ok := VolumeKey(no); ok {
			t.Error(no)
		}
	}
	if BaseName("photos.tar.gz") != "photos" || BaseName("movie.part1.rar") != "movie" {
		t.Error(BaseName("photos.tar.gz"), BaseName("movie.part1.rar"))
	}
	for _, n := range []string{"a.zip", "A.7Z", "b.part2.rar", "c.r00", "d.7z.003", "e.tar.gz", "f.tgz", "g.xz"} {
		if !ArchiveName(n) {
			t.Error(n)
		}
	}
	for _, n := range []string{"a.exe", "zip", ".zip", "notes.txt", "movie.mkv"} {
		if ArchiveName(n) {
			t.Error(n)
		}
	}
}

func TestZipSmartFolderAndMarks(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "two.zip")
	zipOf(t, a, []entry{{"one.txt", []byte("1")}, {"sub/two.txt", []byte("22")}}, 0)
	dest := filepath.Join(dir, "out")
	r, seen, err := run(t, a, dest, "", Smart)
	if err != nil {
		t.Fatal(err)
	}
	// Two things at the top: a folder named after the archive.
	if r.Folder != filepath.Join(dest, "two") || read(t, filepath.Join(dest, "two", "sub", "two.txt")) != "22" ||
		r.Files != 2 || len(seen) != 2 || r.Bytes != 3 {
		t.Fatalf("%+v %v", r, seen)
	}
	noStaging(t, dest)

	// One folder at the top: straight into the destination; a second run keeps the first.
	b := filepath.Join(dir, "one.zip")
	zipOf(t, b, []entry{{"app/readme.txt", []byte("hi")}, {"app/bin/x", []byte("x")}}, 0)
	if r, _, err := run(t, b, dest, "", Smart); err != nil || r.Folder != dest {
		t.Fatal(r, err)
	}
	if r, _, err := run(t, b, dest, "", Smart); err != nil || r.Renamed != 1 {
		t.Fatal(r, err)
	}
	if read(t, filepath.Join(dest, "app (1)", "readme.txt")) != "hi" {
		t.Fatal("numbered")
	}
	// Always: a folder even for one thing.
	if r, _, err := run(t, b, dest, "", Always); err != nil || r.Folder != filepath.Join(dest, "one") {
		t.Fatal(r, err)
	}
	noStaging(t, dest)
}

func TestAnEncryptedZipNeedsItsPasswordAnd7Zip(t *testing.T) {
	if FindSevenZip() != "" {
		t.Skip("7-Zip is installed: TestThroughSevenZip covers it")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "secret.zip")
	zipOf(t, a, []entry{{"s.txt", []byte("secret")}}, 0x1) // marked encrypted
	dest := filepath.Join(dir, "out")
	if _, _, err := run(t, a, dest, "", Never); !errors.Is(err, ErrNeedsPassword) {
		t.Fatal(err)
	}
	if _, _, err := run(t, a, dest, "pw", Never); !errors.Is(err, ErrNoTool) {
		t.Fatal(err)
	}
	noStaging(t, dest)
}

func TestHostileZipEntriesAreLeftOut(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "evil.zip")
	f, _ := os.Create(a)
	z := zip.NewWriter(f)
	for _, name := range []string{"../outside.txt", "ok.txt", "C:/drive.txt"} {
		w, _ := z.Create(name)
		_, _ = w.Write([]byte("x"))
	}
	link := &zip.FileHeader{Name: "link"}
	link.SetMode(os.ModeSymlink | 0o777)
	w, _ := z.CreateHeader(link)
	_, _ = w.Write([]byte("../../etc"))
	z.Close()
	f.Close()
	dest := filepath.Join(dir, "out")
	r, _, err := run(t, a, dest, "", Never)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "ok.txt")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "outside.txt"), filepath.Join(dest, "link")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s written", p)
		}
	}
	if len(r.Skipped) != 3 {
		t.Errorf("%+v", r.Skipped)
	}
}

func TestLimitsStopABomb(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "big.zip")
	zeros := make([]byte, 3<<20)
	zipOf(t, a, []entry{{"z1", zeros}, {"z2", zeros}}, 0)
	dest := filepath.Join(dir, "out")
	_, err := Extract(a, Policy{Dest: dest, Subfolder: Never, Limits: Limits{Total: 4 << 20, Entries: 100, Depth: 8}})
	var big *TooBigError
	if !errors.As(err, &big) {
		t.Fatal(err)
	}
	// Nothing half-written is left in place.
	if _, err := os.Stat(filepath.Join(dest, "z1")); err == nil {
		t.Fatal("z1 left")
	}
	noStaging(t, dest)
	_, err = Extract(a, Policy{Dest: dest, Limits: Limits{Total: 1 << 30, Entries: 1, Depth: 8}})
	if !errors.Is(err, ErrTooMany) {
		t.Fatal(err)
	}
}

// A zip split byte for byte (x.zip.001, .002…) reads as one; a missing
// middle volume is named.
func TestASplitZipAndAMissingVolume(t *testing.T) {
	dir := t.TempDir()
	whole := filepath.Join(dir, "whole.zip")
	noise := make([]byte, 300_000)
	x := uint64(0x9E3779B97F4A7C15)
	for i := range noise {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		noise[i] = byte(x)
	}
	zipOf(t, whole, []entry{{"pack/a.txt", []byte("alpha")}, {"pack/b.bin", noise}}, 0)
	b, _ := os.ReadFile(whole)
	for i, n := 0, 1; i < len(b); i, n = i+100_000, n+1 {
		end := min(i+100_000, len(b))
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("split.zip.%03d", n)), b[i:end], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(dir, "o")
	r, _, err := run(t, filepath.Join(dir, "split.zip.001"), dest, "", Smart)
	if err != nil || r.Folder != dest {
		t.Fatal(r, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "pack", "b.bin")); !bytes.Equal(got, noise) {
		t.Fatal("b.bin differs")
	}
	if err := os.Remove(filepath.Join(dir, "split.zip.002")); err != nil {
		t.Fatal(err)
	}
	_, _, err = run(t, filepath.Join(dir, "split.zip.001"), filepath.Join(dir, "o2"), "", Smart)
	var mv *MissingVolumeError
	if !errors.As(err, &mv) || mv.N != 2 {
		t.Fatal(err)
	}
	noStaging(t, filepath.Join(dir, "o2"))
}

func TestNo7ZipSaysSo(t *testing.T) {
	dir := t.TempDir()
	for name, head := range map[string][]byte{"x.7z": {'7', 'z', 0xBC, 0xAF, 0x27, 0x1C, 0, 4}, "y.rar": []byte("Rar!\x1A\x07\x01\x00")} {
		a := filepath.Join(dir, name)
		if err := os.WriteFile(a, head, 0o644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, "out")
		if _, err := Extract(a, Policy{Dest: dest}); !errors.Is(err, ErrNoTool) {
			t.Errorf("%s: %v", name, err)
		}
		noStaging(t, dest)
	}
}

func TestTarGzAndLinks(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "pack.tar.gz")
	f, _ := os.Create(a)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "pack/run.sh", Mode: 0o4755, Size: 5, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hello"))
	_ = tw.WriteHeader(&tar.Header{Name: "pack/evil", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	_ = tw.WriteHeader(&tar.Header{Name: "pack/hard", Linkname: "pack/run.sh", Typeflag: tar.TypeLink})
	tw.Close()
	gz.Close()
	f.Close()
	dest := filepath.Join(dir, "out")
	r, _, err := run(t, a, dest, "", Smart)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dest, "pack", "run.sh")) != "hello" {
		t.Fatal("run.sh")
	}
	if _, err := os.Lstat(filepath.Join(dest, "pack", "evil")); err == nil {
		t.Fatal("a link was made")
	}
	if len(r.Skipped) != 2 {
		t.Errorf("%+v", r.Skipped)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dest, "pack", "run.sh"))
		if fi.Mode().Perm() != 0o755 || fi.Mode()&os.ModeSetuid != 0 {
			t.Errorf("mode %v", fi.Mode())
		}
	}
}

func TestOneCompressedFile(t *testing.T) {
	dir := t.TempDir()
	gzPath := filepath.Join(dir, "report.csv.gz")
	f, _ := os.Create(gzPath)
	g := gzip.NewWriter(f)
	_, _ = g.Write([]byte("a,b\n1,2\n"))
	g.Close()
	f.Close()
	xzPath := filepath.Join(dir, "notes.txt.xz")
	f, _ = os.Create(xzPath)
	x, err := xz.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = x.Write([]byte("from xz"))
	x.Close()
	f.Close()
	dest := filepath.Join(dir, "out")
	if _, _, err := run(t, gzPath, dest, "", Smart); err != nil || read(t, filepath.Join(dest, "report.csv")) != "a,b\n1,2\n" {
		t.Fatal(err)
	}
	if _, _, err := run(t, xzPath, dest, "", Smart); err != nil || read(t, filepath.Join(dest, "notes.txt")) != "from xz" {
		t.Fatal(err)
	}
}

func TestNotAnArchive(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "notes.zip")
	_ = os.WriteFile(a, []byte("just text"), 0o644)
	if _, _, err := run(t, a, dir, "", Smart); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	// A damaged one: a zip's head, nothing after.
	_ = os.WriteFile(a, []byte("PK\x03\x04broken"), 0o644)
	var d *DamagedError
	if _, _, err := run(t, a, filepath.Join(dir, "o"), "", Smart); !errors.As(err, &d) {
		t.Fatal(err)
	}
}

// Through 7-Zip (skipped where it isn't installed): 7z plain, encrypted,
// split; its output goes through the same checks.
func TestThroughSevenZip(t *testing.T) {
	tool := FindSevenZip()
	if tool == "" {
		t.Skip("7-Zip not found")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	_ = os.MkdirAll(filepath.Join(src, "inner"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("alpha"), 0o644)
	noise := bytes.Repeat([]byte{1, 7, 3, 9, 2}, 60_000)
	_ = os.WriteFile(filepath.Join(src, "inner", "b.bin"), noise, 0o644)
	make7z := func(archive string, extra ...string) {
		args := append([]string{"a", "-bso0", "-bsp0", "-mx0"}, extra...)
		args = append(args, archive, filepath.Join(src, "*"))
		if out, err := exec.Command(tool, args...).CombinedOutput(); err != nil {
			t.Fatalf("7-Zip couldn't make %s: %v %s", archive, err, out)
		}
	}
	plain := filepath.Join(dir, "plain.7z")
	make7z(plain)
	r, _, err := run(t, plain, filepath.Join(dir, "o1"), "", Smart)
	if err != nil || read(t, filepath.Join(r.Folder, "a.txt")) != "alpha" {
		t.Fatal(r, err)
	}
	enc := filepath.Join(dir, "enc.7z")
	make7z(enc, "-ps3cret", "-mhe=on")
	if _, _, err := run(t, enc, filepath.Join(dir, "o2"), "", Smart); !errors.Is(err, ErrNeedsPassword) && !errors.Is(err, ErrWrongPassword) {
		t.Fatal(err)
	}
	if _, _, err := run(t, enc, filepath.Join(dir, "o2"), "bad", Smart); !errors.Is(err, ErrWrongPassword) {
		t.Fatal(err)
	}
	if _, _, err := run(t, enc, filepath.Join(dir, "o2"), "s3cret", Smart); err != nil {
		t.Fatal(err)
	}
	noStaging(t, filepath.Join(dir, "o2"))
	split := filepath.Join(dir, "split.7z")
	make7z(split, "-v100k")
	r, _, err = run(t, split+".001", filepath.Join(dir, "o3"), "", Smart)
	if err != nil || r.Folder != filepath.Join(dir, "o3", "split") {
		t.Fatal(r, err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.Folder, "inner", "b.bin")); !bytes.Equal(got, noise) {
		t.Fatal("b.bin differs")
	}
	_ = os.Remove(split + ".002")
	var mv *MissingVolumeError
	if _, _, err := run(t, split+".001", filepath.Join(dir, "o4"), "", Smart); !errors.As(err, &mv) || mv.N != 2 {
		t.Fatal(err)
	}
	// An encrypted zip goes to 7-Zip too.
	zenc := filepath.Join(dir, "zenc.zip")
	make7z(zenc, "-tzip", "-pzz")
	if _, _, err := run(t, zenc, filepath.Join(dir, "o5"), "zz", Smart); err != nil {
		t.Fatal(err)
	}
}
