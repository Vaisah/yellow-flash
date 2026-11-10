// Ported from Mister Download Manager, crates/tools/src/ffmpeg.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// ffmpeg for merging video and audio: BtbN's LGPL "shared" Windows build of
// the newest stable line (ffmpeg-nX.Y-latest-win64-lgpl-shared-X.Y.zip),
// downloaded on first need, checked against the release's checksums.sha256
// and GitHub's own digest of the asset (BtbN signs nothing), its bin/
// unpacked into tools/ffmpeg/<version>/, switched to atomically, the
// previous version kept. Linux and macOS use the system's ffmpeg.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ffmpegReleases are BtbN's newest releases. Not releases/latest: BtbN
// publishes a dated build each day (autobuild-…) and then remakes its
// latest release with simpler names, and meanwhile GitHub's "latest" is the
// dated one, or a release still uploading its files.
var ffmpegReleases = "https://api.github.com/repos/BtbN/FFmpeg-Builds/releases?per_page=5"

type ffmpegRelease struct {
	PublishedAt string        `json:"published_at"`
	Assets      []ffmpegAsset `json:"assets"`
}

type ffmpegAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	// Digest is "sha256:<hex>", GitHub's digest of the upload.
	Digest string `json:"digest"`
}

// stableLine is the stable line of an LGPL shared win64 asset, named as
// BtbN's latest release names it (ffmpeg-n9.0-latest-win64-lgpl-shared-9.0.zip)
// or as a dated build does (ffmpeg-n9.0.2-47-gb204e94e90-win64-lgpl-shared-9.0.zip):
// "9.0". Never the development (master, ffmpeg-N-…) builds.
func stableLine(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "ffmpeg-n")
	if !ok {
		return "", false
	}
	if rest, ok = strings.CutSuffix(rest, ".zip"); !ok {
		return "", false
	}
	i := strings.LastIndex(rest, "-win64-lgpl-shared-")
	if i < 0 {
		return "", false
	}
	head, line := rest[:i], rest[i+len("-win64-lgpl-shared-"):]
	numeric := func(v string) bool {
		if v == "" {
			return false
		}
		for _, p := range strings.Split(v, ".") {
			if _, err := strconv.ParseUint(p, 10, 32); err != nil {
				return false
			}
		}
		return true
	}
	isHex := func(s string) bool {
		for _, c := range s {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
		return true
	}
	version, build, ok := strings.Cut(head, "-")
	if !ok {
		return "", false
	}
	ofLine := version == line || strings.HasPrefix(version, line+".")
	buildOK := build == "latest"
	if n, hash, found := strings.Cut(build, "-g"); found {
		_, err := strconv.ParseUint(n, 10, 32)
		buildOK = err == nil && isHex(hash)
	}
	if numeric(line) && numeric(version) && ofLine && buildOK {
		return line, true
	}
	return "", false
}

// bestBuild is the release's LGPL shared win64 build of the newest stable
// line.
func bestBuild(r *ffmpegRelease) (*ffmpegAsset, bool) {
	parts := func(v string) []int {
		var out []int
		for _, p := range strings.Split(v, ".") {
			n, _ := strconv.Atoi(p)
			out = append(out, n)
		}
		return out
	}
	var best *ffmpegAsset
	var bestLine []int
	for i := range r.Assets {
		line, ok := stableLine(r.Assets[i].Name)
		if !ok {
			continue
		}
		if p := parts(line); best == nil || slices.Compare(p, bestLine) > 0 {
			best, bestLine = &r.Assets[i], p
		}
	}
	return best, best != nil
}

// pickFfmpeg is, among releases, the newest with a build and its checksums.
func pickFfmpeg(releases []ffmpegRelease) (*ffmpegRelease, *ffmpegAsset, bool) {
	// Newest first (RFC 3339 times sort as text).
	sort.SliceStable(releases, func(i, j int) bool { return releases[i].PublishedAt > releases[j].PublishedAt })
	for i := range releases {
		r := &releases[i]
		hasSums := slices.ContainsFunc(r.Assets, func(a ffmpegAsset) bool { return a.Name == "checksums.sha256" })
		if a, ok := bestBuild(r); ok && hasSums {
			return r, a, true
		}
	}
	return nil, nil, false
}

// Ffmpeg is the ffmpeg to use: the person's own (chosen), else ours in dir
// (dir/ffmpeg/current.json); on Linux and macOS, else the system's own.
func Ffmpeg(dir string) (Installed, bool) {
	if i, ok := Chosen(dir, HelperFfmpeg); ok {
		return i, true
	}
	if i, ok := own(dir, "ffmpeg"); ok {
		return i, true
	}
	if runtime.GOOS != "windows" {
		return systemFfmpeg()
	}
	return Installed{}, false
}

// systemFfmpeg is the ffmpeg the package manager or Homebrew installed.
// Homebrew's folders are looked in too: an app started from the Dock
// doesn't get the shell's PATH.
func systemFfmpeg() (Installed, bool) {
	dirs := append(filepath.SplitList(os.Getenv("PATH")), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin")
	for _, d := range dirs {
		if p := filepath.Join(d, "ffmpeg"); d != "" && isFile(p) {
			return Installed{Version: "system", Exe: p}, true
		}
	}
	return Installed{}, false
}

// InstallFfmpeg installs the newest stable LGPL build into dir unless the
// installed one is as new (Windows; elsewhere the system's ffmpeg is used).
func InstallFfmpeg(ctx context.Context, dir string, client *http.Client, progress Progress) (Installed, error) {
	if mine, ok := Chosen(dir, HelperFfmpeg); ok {
		return mine, nil
	}
	if runtime.GOOS != "windows" {
		return Installed{}, fmt.Errorf("%w: an ffmpeg build for this system (install ffmpeg)", ErrMissing)
	}
	return installFfmpeg(ctx, dir, client, progress)
}

func installFfmpeg(ctx context.Context, dir string, client *http.Client, progress Progress) (Installed, error) {
	body, err := getBytes(ctx, client, ffmpegReleases)
	if err != nil {
		return Installed{}, err
	}
	var releases []ffmpegRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return Installed{}, fmt.Errorf("%w: %v", ErrBadRelease, err)
	}
	rel, asset, ok := pickFfmpeg(releases)
	if !ok {
		return Installed{}, fmt.Errorf("%w: LGPL Windows build", ErrMissing)
	}
	line, _ := stableLine(asset.Name)
	// 9.0.20261028: the line, then the build date (the release is rebuilt
	// daily).
	var date strings.Builder
	for _, c := range rel.PublishedAt[:min(10, len(rel.PublishedAt))] {
		if c >= '0' && c <= '9' {
			date.WriteRune(c)
		}
	}
	version := line + "." + date.String()
	if current, ok := own(dir, "ffmpeg"); ok && !newer(version, current.Version) {
		return current, nil
	}
	var sumsURL string
	for _, a := range rel.Assets {
		if a.Name == "checksums.sha256" {
			sumsURL = a.URL
		}
	}
	sums, err := getBytes(ctx, client, sumsURL)
	if err != nil {
		return Installed{}, err
	}
	expected, ok := SumFor(string(sums), asset.Name)
	if !ok {
		return Installed{}, fmt.Errorf("%w: checksum for %s", ErrMissing, asset.Name)
	}
	if d, ok := strings.CutPrefix(asset.Digest, "sha256:"); ok {
		if b, err := hex.DecodeString(d); err != nil || !bytes.Equal(b, expected[:]) {
			return Installed{}, ErrBadChecksum
		}
	}
	// Downloaded to a file beside the install (about 80 MB), checked against
	// the checksum; named by it, so one release's cut-off download resumes.
	root := filepath.Join(dir, "ffmpeg")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Installed{}, err
	}
	zipPath := filepath.Join(root, "ffmpeg-"+hex.EncodeToString(expected[:8])+".zip")
	if err := fetchVerified(ctx, client, asset.URL, zipPath, expected, progress); err != nil {
		return Installed{}, err
	}
	exe, err := unpackFfmpeg(zipPath, root, version, asset.URL)
	_ = os.Remove(zipPath)
	if err != nil {
		return Installed{}, err
	}
	return makeCurrent(root, Installed{Version: version, Exe: exe})
}

// Bounds on what a build may unpack to, against a malicious archive.
const (
	maxUnpackedFile  = 512 << 20
	maxUnpackedTotal = 2 << 30
)

// unpackFfmpeg unpacks the zip's bin/ (ffmpeg.exe, ffprobe.exe and their
// DLLs) into root/<version>/: only plain files directly in a bin folder, by
// their own name, so nothing can climb out.
func unpackFfmpeg(zipPath, root, version, from string) (string, error) {
	if !validVersion(version) {
		return "", fmt.Errorf("%w: version %q", ErrBadRelease, version)
	}
	folder := filepath.Join(root, version)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRelease, err)
	}
	defer zr.Close()
	var total int64
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if !f.Mode().IsRegular() || path.Base(path.Dir(name)) != "bin" {
			continue
		}
		base := path.Base(name)
		if base == "." || base == ".." || strings.ContainsAny(base, `:/\`) {
			continue
		}
		if f.UncompressedSize64 > maxUnpackedFile {
			return "", fmt.Errorf("%w: %s is too large", ErrBadRelease, base)
		}
		n, err := unpackFile(f, filepath.Join(folder, base))
		if err != nil {
			return "", err
		}
		if total += n; total > maxUnpackedTotal {
			return "", fmt.Errorf("%w: the build is too large", ErrBadRelease)
		}
		if strings.HasSuffix(strings.ToLower(base), ".exe") {
			markDownloaded(filepath.Join(folder, base), from)
		}
	}
	exe := filepath.Join(folder, "ffmpeg.exe")
	if !isFile(exe) {
		return "", fmt.Errorf("%w: ffmpeg.exe in the build", ErrMissing)
	}
	return exe, nil
}

func unpackFile(f *zip.File, dest string) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBadRelease, err)
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	// The size the archive claims is checked above; this holds whatever it
	// really holds to it.
	n, err := io.Copy(out, io.LimitReader(rc, maxUnpackedFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxUnpackedFile {
		err = fmt.Errorf("%w: %s is too large", ErrBadRelease, filepath.Base(dest))
	}
	return n, err
}
