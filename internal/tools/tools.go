// Ported from Mister Download Manager, crates/tools/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package tools installs and runs the helper programs Yellow Flash drives
// for what a plain download can't do: yt-dlp (video pages), ffmpeg (merging
// video and audio) and gallery-dl (image galleries).
//
// Installs are verified and pinned. yt-dlp is never updated by itself
// (yt-dlp -U): a release is fetched from its GitHub page, its SHA2-256SUMS is
// checked against yt-dlp's OpenPGP signing key (pinned in keys/yt-dlp.asc),
// the program against that list, and it is installed next to earlier
// versions (tools/yt-dlp/<version>/), switched to atomically, never
// downgraded, the previous version kept for rollback. A program the person
// chose instead (chosen.go) is used first and never updated.
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/vaisah/internal/ipc"
)

//go:embed keys/yt-dlp.asc
var ytdlpKey []byte

var (
	// ErrBadSignature: the release's checksum list is not signed by its
	// publisher's key.
	ErrBadSignature = errors.New("the release's checksum list is not signed by its publisher's key")
	// ErrBadChecksum: the downloaded program doesn't match its signed checksum.
	ErrBadChecksum = errors.New("the downloaded program doesn't match its signed checksum")
	// ErrMissing: the release lacks a file it needs.
	ErrMissing = errors.New("the release has no such file")
	// ErrBadRelease: the release's data can't be used.
	ErrBadRelease = errors.New("unexpected release data")
	// ErrNotTheProgram: a program chosen as a helper didn't answer as one.
	ErrNotTheProgram = errors.New("this program isn't the helper it was chosen as")
	// ErrTooOld: a chosen program is a release Yellow Flash can't drive.
	ErrTooOld = errors.New("this program is too old")
)

// Installed is an installed helper.
type Installed struct {
	Version string `json:"version"`
	Exe     string `json:"exe"`
}

// Dir is <data dir>/tools, where the helpers are installed.
func Dir(dataDir string) string { return filepath.Join(dataDir, "tools") }

// UserAgent is Yellow Flash's own name, sent when it fetches its helpers.
var UserAgent = "YellowFlash/" + ipc.AppVersion

// YtDlpAsset is yt-dlp's file name for this platform in its releases.
func YtDlpAsset() string {
	switch runtime.GOOS {
	case "windows":
		return "yt-dlp.exe"
	case "darwin":
		return "yt-dlp_macos"
	}
	return "yt-dlp_linux"
}

// SignedByYtDlp reports whether sums carries a valid signature sig (OpenPGP)
// by yt-dlp's key or one of its subkeys.
func SignedByYtDlp(sums, sig []byte) bool { return verifyPGP(ytdlpKey, sums, sig) }

// maxSmall bounds what is read into memory: release data, checksum lists,
// signatures.
const maxSmall = 8 << 20

// getBytes is url's body.
func getBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxSmall+1))
	if err == nil && len(b) > maxSmall {
		err = fmt.Errorf("%s: too large", url)
	}
	return b, err
}

// signedList fetches a release's checksum list ("hash  name" lines) with its
// detached signature, and accepts it only when signed (the publisher's key)
// says so.
func signedList(ctx context.Context, client *http.Client, sumsURL, sigURL string, signed func(sums, sig []byte) bool) (string, error) {
	sums, err := getBytes(ctx, client, sumsURL)
	if err != nil {
		return "", err
	}
	sig, err := getBytes(ctx, client, sigURL)
	if err != nil {
		return "", err
	}
	if !signed(sums, sig) {
		return "", ErrBadSignature
	}
	return string(sums), nil
}

// Progress is told how much of a helper has been fetched (total -1:
// unknown).
type Progress func(got, total int64)

// maxProgram bounds a helper's download.
const maxProgram = 1 << 30

// fetchVerified downloads url into path and accepts it only when its SHA-256
// is expected. An earlier attempt cut off is resumed from where it stopped;
// a file that doesn't match is removed.
func fetchVerified(ctx context.Context, client *http.Client, url, path string, expected [32]byte, progress Progress) error {
	var have int64
	if fi, err := os.Stat(path); err == nil {
		have = fi.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	total := int64(-1)
	switch {
	case res.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
		if res.ContentLength >= 0 {
			total = have + res.ContentLength
		}
	case res.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		// Whole already: checked below.
		flags |= os.O_APPEND
	case res.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
		total = res.ContentLength
	default:
		return fmt.Errorf("%s: %s", url, res.Status)
	}
	if total > maxProgram {
		return fmt.Errorf("%s: too large", url)
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		got := have
		buf := make([]byte, 64<<10)
		for {
			n, rerr := res.Body.Read(buf)
			if n > 0 {
				if got += int64(n); got > maxProgram {
					rerr = fmt.Errorf("%s: too large", url)
				} else if _, werr := f.Write(buf[:n]); werr != nil {
					rerr = werr
				}
				if progress != nil {
					progress(got, total)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				f.Close()
				return rerr // kept: the next attempt resumes
			}
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	sum, err := sha256Of(path)
	if err != nil {
		return err
	}
	if sum != expected {
		_ = os.Remove(path)
		return ErrBadChecksum
	}
	return nil
}

// sha256Of is the SHA-256 of the file at path.
func sha256Of(path string) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// SumFor is the SHA-256 a "hash  name" list gives for name.
func SumFor(sums, name string) ([32]byte, bool) {
	var sum [32]byte
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		b, err := hex.DecodeString(fields[0])
		if err != nil || len(b) != 32 {
			return sum, false
		}
		copy(sum[:], b)
		return sum, true
	}
	return sum, false
}

// YtDlp is the yt-dlp to use: the person's own (chosen), else ours in dir
// (dir/yt-dlp/current.json), if any and still there.
func YtDlp(dir string) (Installed, bool) {
	if i, ok := Chosen(dir, HelperYtDlp); ok {
		return i, true
	}
	return own(dir, "yt-dlp")
}

// own is our install of tool in dir, if still there.
func own(dir, tool string) (Installed, bool) {
	var i Installed
	raw, err := os.ReadFile(filepath.Join(dir, tool, "current.json"))
	if err != nil || json.Unmarshal(raw, &i) != nil || !isFile(i.Exe) {
		return Installed{}, false
	}
	return i, true
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// newer reports whether version a is newer than b. Versions are release
// dates (2026.10.26, maybe .1) or semantic versions: numeric parts compare
// in order.
func newer(a, b string) bool {
	parts := func(v string) []int {
		var out []int
		for _, p := range strings.Split(v, ".") {
			if n, err := strconv.Atoi(p); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	return slices.Compare(parts(a), parts(b)) > 0
}

// release is a GitHub release as its API describes it.
type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *release) url(name string) (string, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrMissing, name)
}

// source is where a helper's releases come from and whose key signs them;
// tests point it elsewhere.
type source struct {
	releases string
	signed   func(sums, sig []byte) bool
}

var ytdlpSource = source{
	releases: "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest",
	signed:   SignedByYtDlp,
}

// InstallYtDlp installs the latest yt-dlp release into dir unless the
// installed one is as new, fetched with client, counted into progress. It
// returns what is installed afterwards. A chosen copy is the person's to
// update: it is returned as it is.
func InstallYtDlp(ctx context.Context, dir string, client *http.Client, progress Progress) (Installed, error) {
	if mine, ok := Chosen(dir, HelperYtDlp); ok {
		return mine, nil
	}
	return installSigned(ctx, dir, client, signedRelease{
		tool: "yt-dlp", src: ytdlpSource, sums: "SHA2-256SUMS", asset: YtDlpAsset(),
	}, progress)
}

// signedRelease is a helper published as one program with a signed
// checksum list.
type signedRelease struct {
	tool  string // its folder under the tools dir
	src   source
	sums  string // the checksum list's name; its signature is sums+".sig"
	asset string // the program's name in the release
}

// installSigned installs r's latest release unless the installed one is as
// new: the checksum list checked against the publisher's key, the program
// against the list, then put in place beside the previous version.
func installSigned(ctx context.Context, dir string, client *http.Client, r signedRelease, progress Progress) (Installed, error) {
	body, err := getBytes(ctx, client, r.src.releases)
	if err != nil {
		return Installed{}, err
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return Installed{}, fmt.Errorf("%w: %v", ErrBadRelease, err)
	}
	version := strings.TrimPrefix(rel.TagName, "v")
	if current, ok := own(dir, r.tool); ok && !newer(version, current.Version) {
		return current, nil // never the same again, never older
	}
	sumsURL, err := rel.url(r.sums)
	if err != nil {
		return Installed{}, err
	}
	sigURL, err := rel.url(r.sums + ".sig")
	if err != nil {
		return Installed{}, err
	}
	sums, err := signedList(ctx, client, sumsURL, sigURL, r.src.signed)
	if err != nil {
		return Installed{}, err
	}
	expected, ok := SumFor(sums, r.asset)
	if !ok {
		return Installed{}, fmt.Errorf("%w: checksum for %s", ErrMissing, r.asset)
	}
	exeURL, err := rel.url(r.asset)
	if err != nil {
		return Installed{}, err
	}
	root := filepath.Join(dir, r.tool)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Installed{}, err
	}
	// Named by its checksum: one release's cut-off download resumes, never
	// another's.
	download := filepath.Join(root, r.tool+"-"+hex.EncodeToString(expected[:8])+".download")
	if err := fetchVerified(ctx, client, exeURL, download, expected, progress); err != nil {
		return Installed{}, err
	}
	program, err := os.ReadFile(download)
	_ = os.Remove(download)
	if err != nil {
		return Installed{}, err
	}
	if sha256.Sum256(program) != expected {
		return Installed{}, ErrBadChecksum
	}
	return put(dir, r.tool, version, r.asset, program, exeURL)
}

// validVersion refuses a version that could name another folder.
func validVersion(v string) bool {
	return v != "" && !strings.ContainsAny(v, `/\:`) && !strings.HasPrefix(v, ".")
}

// put installs verified program as version of tool
// (dir/<tool>/<version>/<asset>) and makes it current (atomically), keeping
// the previous version for rollback and removing older ones.
func put(dir, tool, version, asset string, program []byte, from string) (Installed, error) {
	if !validVersion(version) {
		return Installed{}, fmt.Errorf("%w: version %q", ErrBadRelease, version)
	}
	root := filepath.Join(dir, tool)
	folder := filepath.Join(root, version)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return Installed{}, err
	}
	exe := filepath.Join(folder, asset)
	if err := writeAtomic(exe, program, 0o755); err != nil {
		return Installed{}, err
	}
	markDownloaded(exe, from)
	return makeCurrent(root, Installed{Version: version, Exe: exe})
}

// makeCurrent makes now (installed in root/<version>/) the current version
// of a tool: current.json switched atomically, the previous version kept for
// rollback, older ones removed.
func makeCurrent(root string, now Installed) (Installed, error) {
	var previous Installed
	if raw, err := os.ReadFile(filepath.Join(root, "current.json")); err == nil {
		_ = json.Unmarshal(raw, &previous)
	}
	raw, err := json.MarshalIndent(now, "", "  ")
	if err != nil {
		return Installed{}, err
	}
	if err := writeAtomic(filepath.Join(root, "current.json"), raw, 0o644); err != nil {
		return Installed{}, err
	}
	// Keep the new and the previous version; older ones go.
	keep := []string{now.Version, previous.Version}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() && !slices.Contains(keep, e.Name()) {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
	return now, nil
}

// writeAtomic writes data to path through a temporary file renamed into
// place, so a reader sees the old file or the new one, never a part.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = io.Copy(tmp, bytes.NewReader(data))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, perm)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// markDownloaded gives the program the Mark of the Web on Windows: it came
// from the internet, and Windows checks it as it does a browser download.
func markDownloaded(path, from string) {
	if runtime.GOOS == "windows" {
		_ = os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\nHostUrl="+from+"\r\n"), 0o644)
	}
}
