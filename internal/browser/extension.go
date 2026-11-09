// Ported from Mister Download Manager, crates/ipc/src/extension.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package browser

// The browser extension's identity and whether a browser has it, shared by
// the native messaging host (which serves it and notes each browser that
// starts it) and the app (which offers to install it until one has).
//
// Yellow Flash has its own identity: its own key, extension ID, host name
// and Firefox ID, so it never answers MDM's extension nor MDM's host ours.

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

// HostName is the native messaging host's name, in every manifest and in
// the extension's connectNative call.
const HostName = "com.yellowflash.host"

// HostManifest is the host manifest's file name, in every folder.
const HostManifest = HostName + ".json"

// ManifestKey is the public key in extension/manifest.json (base64 DER
// SubjectPublicKeyInfo); it fixes UnpackedID. Its private half is not kept:
// an unpacked extension needs only this, and a store signs with its own.
const ManifestKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA7k7oLr2BwOZiI9nwrA0St0wnci6ACzayv6dzMMjX8o5zajGrWRc86N+7BSWoeclNAz7nso3EVauPgSSOCWJ6cbadHXRBn19YJl9dpt5+9b9u0BQEJd35OvsY7QAlqMCkXWHNzfRRSDH5DWJnRLXfPFKdjmbnLlv+JL3mpBNbvtWcw/YXSMU2MqXQW2wn8sAizGBwjAoS7GUYl97j0SL5jR5HtY0JL/A8QFUKLH63fG51AM4BtSOn3FjZL7gDYItId05zCF7j4k8K1pTOXr2+riYIxduGpXv4AMEjUTqBfcAbvcc3rBGQmvgI52vtSUhD9cgCO9vXMjDMWFAJiZXZhwIDAQAB"

// UnpackedID is the extension's ID loaded unpacked (from ManifestKey).
const UnpackedID = "fhlfnlgbgkmofdmohiohgmiojleegaah"

// StoreID is its ID in the Chrome Web Store, which signs it with the
// store's own key; a placeholder until listed, and then nothing names it.
const StoreID = "CHROME_STORE_ID_PENDING"

// OtherStoreIDs are its IDs in Microsoft Edge Add-ons and Opera add-ons;
// placeholders until listed there, never served then.
var OtherStoreIDs = [2]string{"EDGE_ADDONS_ID_PENDING", "OPERA_ADDONS_ID_PENDING"}

// FirefoxID is the Firefox add-on's ID (extension/manifest.firefox.json).
const FirefoxID = "integration@yellowflash"

// FirefoxPage is the add-on's page on addons.mozilla.org; "" until listed.
const FirefoxPage = ""

// StoreUpdateURL is Chrome's update service: an external-install entry
// naming it makes Chrome offer the store's extension at its next start.
const StoreUpdateURL = "https://clients2.google.com/service/update2/crx"

// ValidID reports a Chromium extension ID: 32 letters from a to p.
func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 'a' || id[i] > 'p' {
			return false
		}
	}
	return true
}

// IDFromKey returns the extension ID a manifest key (base64 DER) gives:
// the first 16 bytes of its SHA-256, each hex digit as a letter from a to p.
func IDFromKey(key string) (string, bool) {
	der, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", false
	}
	if _, err := x509.ParsePKIXPublicKey(der); err != nil {
		return "", false
	}
	sum := sha256.Sum256(der)
	id := make([]byte, 32)
	for i, b := range sum[:16] {
		id[2*i] = 'a' + b>>4
		id[2*i+1] = 'a' + b&15
	}
	return string(id), true
}

// StoreIDValid returns the store's ID, once it is a real one.
func StoreIDValid() (string, bool) {
	return StoreID, ValidID(StoreID)
}

// ChromiumIDs are the Chromium extensions the native host serves: unpacked,
// and each store's once listed.
func ChromiumIDs() []string {
	ids := []string{UnpackedID}
	if id, ok := StoreIDValid(); ok {
		ids = append(ids, id)
	}
	for _, id := range OtherStoreIDs {
		if ValidID(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// AllowedOrigins are the chrome-extension:// origins a Chromium host
// manifest lists.
func AllowedOrigins() []string {
	var v []string
	for _, id := range ChromiumIDs() {
		v = append(v, "chrome-extension://"+id+"/")
	}
	return v
}

// StorePage is the extension's page in the Chrome Web Store, once listed.
func StorePage() (string, bool) {
	id, ok := StoreIDValid()
	if !ok {
		return "", false
	}
	return "https://chromewebstore.google.com/detail/yellow-flash/" + id, true
}

// GuideDoc is the tutorial that adds the extension, in docs/.
const GuideDoc = "guide-catch-browser-downloads.md"

// HelpDoc is the help page for a browser that doesn't reach Yellow Flash,
// at the section for why (the native host's reasons and the app's checks).
func HelpDoc(why string) string { return "browser-help.md#" + why }

// seenFile marks the data folder once a browser has started the host for
// the extension.
const seenFile = "extension-seen"

// Seen is a browser whose extension has reached the app, with its version
// ("" when unknown).
type Seen struct {
	Browser, Version string
}

// MarkSeen notes, in dataDir, that browser's extension (at version) started
// the host: the extension is installed there. One line per browser.
func MarkSeen(dataDir, browser, version string) error {
	file := filepath.Join(dataDir, seenFile)
	raw, err := os.ReadFile(file)
	known := err == nil
	lines := parseSeen(string(raw))
	entry := Seen{browser, version}
	if known && (browser == "" || containsSeen(lines, entry)) {
		return nil
	}
	kept := lines[:0]
	for _, l := range lines {
		if l.Browser != browser {
			kept = append(kept, l)
		}
	}
	kept = append(kept, entry)
	var b strings.Builder
	for _, l := range kept {
		b.WriteString(l.Browser + "\t" + l.Version + "\n")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(b.String()), 0o600)
}

func containsSeen(lines []Seen, e Seen) bool {
	for _, l := range lines {
		if l == e {
			return true
		}
	}
	return false
}

// parseSeen reads extension-seen's lines; an older file holds only a
// browser's name.
func parseSeen(text string) []Seen {
	var v []Seen
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		b, ver, _ := strings.Cut(l, "\t")
		v = append(v, Seen{b, ver})
	}
	return v
}

// AnySeen reports whether any browser's extension has reached the app.
func AnySeen(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, seenFile))
	return err == nil
}

// SeenBrowsers are the browsers whose extension has reached the app.
func SeenBrowsers(dataDir string) []Seen {
	raw, err := os.ReadFile(filepath.Join(dataDir, seenFile))
	if err != nil {
		return nil
	}
	return parseSeen(string(raw))
}
