// Ported from Mister Download Manager, crates/tools/src/ffmpeg.rs and gallery.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPicksStableLGPLSharedBuildsOnly(t *testing.T) {
	for name, want := range map[string]string{
		"ffmpeg-n9.0-latest-win64-lgpl-shared-9.0.zip": "9.0",
		// A dated build's names (what GitHub calls "latest" while BtbN
		// remakes its own).
		"ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-shared-9.0.zip": "9.0",
		"ffmpeg-n8.1.3-34-ga35157abb9-win64-lgpl-shared-8.1.zip": "8.1",
	} {
		if got, ok := stableLine(name); !ok || got != want {
			t.Errorf("%s: %q %v", name, got, ok)
		}
	}
	for _, other := range []string{
		"ffmpeg-n9.0-latest-win64-lgpl-9.0.zip",
		"ffmpeg-master-latest-win64-lgpl-shared.zip",
		"ffmpeg-n9.0-latest-win64-gpl-shared-9.0.zip",
		"ffmpeg-n8.1-latest-win64-lgpl-shared-9.0.zip",
		"ffmpeg-N-128406-g1fe6854fd9-win64-lgpl-shared.zip",
		"ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-9.0.zip",
		"ffmpeg-n9.0.2-47-gb204e94e90-win64-gpl-shared-9.0.zip",
		"ffmpeg-n9.0.2-47-gb204e94e90-linux64-lgpl-shared-9.0.tar.xz",
		"ffmpeg-n8.1.3-34-ga35157abb9-win64-lgpl-shared-9.0.zip",
		"ffmpeg-n9.0.2-x-gzz-win64-lgpl-shared-9.0.zip",
	} {
		if got, ok := stableLine(other); ok {
			t.Errorf("%s: %q", other, got)
		}
	}
}

func asset(name string) ffmpegAsset { return ffmpegAsset{Name: name, URL: "https://x/" + name} }

// The newest release is a dated build; and a release still uploading (no
// checksums yet) is passed over for the one before.
func TestTheNewestReleaseWithABuildAndItsChecksums(t *testing.T) {
	dated := ffmpegRelease{PublishedAt: "2026-11-04T12:53:43Z", Assets: []ffmpegAsset{
		asset("ffmpeg-n8.1.3-34-ga35157abb9-win64-lgpl-shared-8.1.zip"),
		asset("ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-shared-9.0.zip"),
		asset("checksums.sha256"),
	}}
	if a, ok := bestBuild(&dated); !ok || a.Name != "ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-shared-9.0.zip" {
		t.Errorf("best build %+v", a)
	}
	uploading := ffmpegRelease{PublishedAt: "2026-11-05T12:50:00Z", Assets: []ffmpegAsset{
		asset("ffmpeg-n9.0-latest-win64-lgpl-shared-9.0.zip"),
	}}
	_, a, ok := pickFfmpeg([]ffmpegRelease{dated, uploading})
	if !ok || a.Name != "ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-shared-9.0.zip" {
		t.Errorf("picked %+v", a)
	}
}

// buildZip is a zip holding files.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(data))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestUnpacksOnlyBinFiles(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "b.zip")
	if err := os.WriteFile(zipPath, buildZip(t, map[string]string{
		"ffmpeg-n9.0/bin/ffmpeg.exe":     "exe",
		"ffmpeg-n9.0/bin/avcodec-62.dll": "dll",
		"ffmpeg-n9.0/doc/readme.txt":     "doc",
		"ffmpeg-n9.0/LICENSE.txt":        "lic",
		// Nothing climbs out of the version's folder.
		"../../bin/evil.exe":          "evil",
		"ffmpeg-n9.0/bin/../../x.dll": "x",
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := unpackFfmpeg(zipPath, dir, "9.0.20261028", "https://x/")
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Dir(exe)
	if b, _ := os.ReadFile(exe); string(b) != "exe" || !isFile(filepath.Join(folder, "avcodec-62.dll")) {
		t.Error("bin's files")
	}
	for _, name := range []string{"readme.txt", "LICENSE.txt", "x.dll"} {
		if isFile(filepath.Join(folder, name)) {
			t.Errorf("%s unpacked", name)
		}
	}
	// evil.exe sits in a folder named bin, so it lands, by its name, in the
	// version's folder and nowhere else.
	if isFile(filepath.Join(dir, "..", "..", "bin", "evil.exe")) || isFile(filepath.Join(filepath.Dir(dir), "evil.exe")) {
		t.Error("a file climbed out")
	}
	if _, err := unpackFfmpeg(zipPath, dir, "../x", "https://x/"); err == nil {
		t.Error("a version naming another folder")
	}
	noExe := filepath.Join(dir, "none.zip")
	_ = os.WriteFile(noExe, buildZip(t, map[string]string{"f/bin/ffprobe.exe": "p"}), 0o600)
	if _, err := unpackFfmpeg(noExe, dir, "9.1", "https://x/"); !errors.Is(err, ErrMissing) {
		t.Errorf("no ffmpeg.exe: %v", err)
	}
}

// fakeBtbN serves BtbN's release list with one build and its checksums.
func fakeBtbN(t *testing.T, published string, build []byte, digest string) (*httptest.Server, *int) {
	hits := new(int)
	name := "ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-shared-9.0.zip"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"published_at": published, "assets": []map[string]string{
				{"name": name, "browser_download_url": base + "/build", "digest": digest},
				{"name": "checksums.sha256", "browser_download_url": base + "/sums"},
			}}})
		case "/sums":
			fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(build), name)
		case "/build":
			*hits++
			_, _ = w.Write(build)
		}
	}))
	t.Cleanup(srv.Close)
	saved := ffmpegReleases
	ffmpegReleases = srv.URL + "/releases"
	t.Cleanup(func() { ffmpegReleases = saved })
	return srv, hits
}

func TestFfmpegIsInstalledFromItsCheckedBuild(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	build := buildZip(t, map[string]string{"ffmpeg-n9.0/bin/ffmpeg.exe": "ffmpeg", "ffmpeg-n9.0/bin/ffprobe.exe": "ffprobe"})
	sum := sha256.Sum256(build)

	// GitHub's digest disagrees with the checksum list: refused.
	srv, hits := fakeBtbN(t, "2026-11-04T12:53:43Z", build, "sha256:"+fmt.Sprintf("%x", sha256.Sum256([]byte("other"))))
	if _, err := installFfmpeg(ctx, dir, srv.Client(), nil); !errors.Is(err, ErrBadChecksum) || *hits != 0 {
		t.Fatalf("a disagreeing digest: %v, fetched %d", err, *hits)
	}

	srv, _ = fakeBtbN(t, "2026-11-04T12:53:43Z", build, fmt.Sprintf("sha256:%x", sum))
	got, err := installFfmpeg(ctx, dir, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got.Exe); got.Version != "9.0.20261104" || string(b) != "ffmpeg" {
		t.Errorf("installed %+v %q", got, b)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "ffmpeg", "*.zip")); len(leftovers) != 0 {
		t.Errorf("left %v", leftovers)
	}
	if mine, ok := own(dir, "ffmpeg"); !ok || mine != got {
		t.Error("not current")
	}
	// The same build again, or an older one: nothing fetched.
	srv, hits = fakeBtbN(t, "2026-11-01T00:00:00Z", build, "")
	if again, err := installFfmpeg(ctx, dir, srv.Client(), nil); err != nil || again != got || *hits != 0 {
		t.Errorf("older: %+v %v, fetched %d", again, err, *hits)
	}
}

func TestEachGalleryDlReleaseGetsOnlyTheOptionsItAccepts(t *testing.T) {
	for v, want := range map[string]GalleryAccepts{
		"1.20.0": {}, "1.23.5": {},
		"1.24.0": {UserAgent: true}, "1.26.9": {UserAgent: true}, "1.27.0": {UserAgent: true},
		"1.27.1": {true, true}, "1.32.19": {true, true}, "1.31.0-dev": {true, true}, "2.0": {true, true},
		// One that can't be read: as recent as our own.
		"unknown": {true, true},
	} {
		if got := GalleryAcceptsOf(v); got != want {
			t.Errorf("%s: %+v", v, got)
		}
	}
	for _, v := range []string{"1.17.0", "1.19.3", "0.9"} {
		if !galleryTooOld(v) {
			t.Errorf("%s not too old", v)
		}
	}
	for _, v := range []string{"1.20.0", "1.26.9", "unknown"} {
		if galleryTooOld(v) {
			t.Errorf("%s too old", v)
		}
	}
}

func TestAChosenGalleryDlTooOldGivesWayToOurOwn(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "old-gallery-dl")
	_ = os.WriteFile(exe, []byte("x"), 0o755)
	old := Installed{Version: "1.17.0", Exe: exe}
	if err := writePrograms(dir, map[Helper]Installed{HelperGalleryDl: old}); err != nil {
		t.Fatal(err)
	}
	if _, ok := GalleryDl(dir); ok {
		t.Error("the old one is used")
	}
	if got, ok := GalleryDlChosenTooOld(dir); !ok || got != old {
		t.Error("the old one isn't named")
	}
	ours, err := put(dir, "gallery-dl", "1.30.0", "gallery-dl.bin", []byte("ours"), "https://x/")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := GalleryDl(dir); !ok || got != ours {
		t.Errorf("used %+v", got)
	}
	if _, ok := YtDlp(dir); ok {
		t.Error("taken for yt-dlp")
	}
}

func TestGalleryDlIsInstalledFromASignedRelease(t *testing.T) {
	asset, ok := GalleryDlAsset()
	if !ok {
		t.Skip("no gallery-dl build for this system")
	}
	program := []byte("gallery-dl")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.30.2", "assets": []map[string]string{
				{"name": "SHA256SUMS", "browser_download_url": base + "/sums"},
				{"name": "SHA256SUMS.sig", "browser_download_url": base + "/sig"},
				{"name": asset, "browser_download_url": base + "/program"},
			}})
		case "/sums":
			fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(program), asset)
		case "/sig":
			_, _ = w.Write([]byte("good"))
		case "/program":
			_, _ = w.Write(program)
		}
	}))
	defer srv.Close()
	saved := galleryDlSource
	galleryDlSource = source{releases: srv.URL + "/latest", signed: func(_, sig []byte) bool { return string(sig) == "good" }}
	defer func() { galleryDlSource = saved }()
	dir := t.TempDir()
	got, err := InstallGalleryDl(context.Background(), dir, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.30.2" || filepath.Base(filepath.Dir(got.Exe)) != "1.30.2" {
		t.Errorf("installed %+v", got)
	}
	if mine, ok := GalleryDl(dir); !ok || mine != got {
		t.Error("not used")
	}
	// gallery-dl's own key signs for it alone.
	if !SignedByGalleryDl(read(t, "testdata/gallery-dl-SHA256SUMS"), read(t, "testdata/gallery-dl-SHA256SUMS.sig")) {
		t.Error("its real list")
	}
	if SignedByGalleryDl(read(t, "testdata/SHA2-256SUMS"), read(t, "testdata/SHA2-256SUMS.sig")) {
		t.Error("yt-dlp's list")
	}
}
