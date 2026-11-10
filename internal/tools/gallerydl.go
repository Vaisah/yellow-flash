// Ported from Mister Download Manager, crates/tools/src/gallery.rs (installing)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package tools

// gallery-dl: image galleries (Wikimedia Commons, Flickr, DeviantArt,
// Pixiv…) as yt-dlp is for video pages. An optional helper, installed the
// same way: a release from its own page (Codeberg), its SHA256SUMS checked
// against its author's OpenPGP key (pinned in keys/gallery-dl.asc), the
// program against that list, kept in tools/gallery-dl/<version>/ with the
// previous version, never older; or the person's own copy (chosen.go).

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"runtime"
)

// galleryDlKey is gallery-dl's author's signing key (Mike Fährmann,
// fingerprint 3E09F5908333DD83DBDCE7375680CA389D365A88). It signs each
// release's SHA256SUMS.
//
//go:embed keys/gallery-dl.asc
var galleryDlKey []byte

// galleryDlSource is Codeberg's API for its releases (as GitHub's:
// tag_name, assets).
var galleryDlSource = source{
	releases: "https://codeberg.org/api/v1/repos/mikf/gallery-dl/releases/latest",
	signed:   SignedByGalleryDl,
}

// SignedByGalleryDl reports whether sums carries a valid signature sig by
// gallery-dl's author's key.
func SignedByGalleryDl(sums, sig []byte) bool { return verifyPGP(galleryDlKey, sums, sig) }

// GalleryDlAsset is gallery-dl's program for this system in its releases;
// false on macOS (no build: Homebrew's, chosen, works there) and on Linux
// other than x86-64 (the same, from the system's packages or pip).
func GalleryDlAsset() (string, bool) {
	switch {
	case runtime.GOOS == "windows":
		return "gallery-dl.exe", true
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "gallery-dl.bin", true
	}
	return "", false
}

// GalleryDl is the gallery-dl to use: the person's own (chosen), else ours
// in dir. A chosen one too old to drive is passed over for our own copy;
// GalleryDlChosenTooOld names it.
func GalleryDl(dir string) (Installed, bool) {
	if mine, ok := Chosen(dir, HelperGalleryDl); ok && !galleryTooOld(mine.Version) {
		return mine, true
	}
	return own(dir, "gallery-dl")
}

// GalleryDlChosenTooOld is the person's own gallery-dl when it is too old to
// drive, so passed over.
func GalleryDlChosenTooOld(dir string) (Installed, bool) {
	mine, ok := Chosen(dir, HelperGalleryDl)
	return mine, ok && galleryTooOld(mine.Version)
}

// InstallGalleryDl installs the latest gallery-dl into dir unless the
// installed one is as new. The person's own copy is theirs to update.
func InstallGalleryDl(ctx context.Context, dir string, client *http.Client, progress Progress) (Installed, error) {
	if mine, ok := Chosen(dir, HelperGalleryDl); ok {
		return mine, nil
	}
	asset, ok := GalleryDlAsset()
	if !ok {
		return Installed{}, fmt.Errorf("%w: a gallery-dl build for this system: install gallery-dl with the system's packages (Homebrew, pip) and choose it", ErrMissing)
	}
	return installSigned(ctx, dir, client, signedRelease{
		tool: "gallery-dl", src: galleryDlSource, sums: "SHA256SUMS", asset: asset,
	}, progress)
}

// GalleryAccepts is what a gallery-dl release accepts of the options we
// pass, past those every release from GalleryDlOldest has. argparse refuses
// an option it doesn't know, so one too many fails the whole download.
type GalleryAccepts struct {
	// NoInput is --no-input (1.27.1): never a question on the console.
	NoInput bool
	// UserAgent is --user-agent (1.24.0); before it, -o user-agent=….
	UserAgent bool
}

// GalleryAcceptsOf is GalleryAccepts for version as gallery-dl prints it
// (1.26.9, 1.31.0-dev). A version that can't be read is taken as a recent
// one.
func GalleryAcceptsOf(version string) GalleryAccepts {
	have, ok := galleryRelease(version)
	atLeast := func(v string) bool {
		want, _ := galleryRelease(v)
		if !ok {
			return true
		}
		for i := range have {
			if have[i] != want[i] {
				return have[i] > want[i]
			}
		}
		return true
	}
	return GalleryAccepts{NoInput: atLeast("1.27.1"), UserAgent: atLeast("1.24.0")}
}
