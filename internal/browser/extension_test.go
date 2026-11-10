// Ported from Mister Download Manager, crates/ipc/src/extension.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIDsAreChromiumIDs(t *testing.T) {
	if !ValidID(UnpackedID) {
		t.Error("UnpackedID")
	}
	if ValidID("STORE_ID_PENDING") || ValidID("abc") {
		t.Error("a placeholder is valid")
	}
	// A placeholder store ID is never served, registered or linked.
	_, ok := StoreIDValid()
	if ok != ValidID(StoreID) {
		t.Error("StoreIDValid")
	}
	for _, id := range ChromiumIDs() {
		if !ValidID(id) {
			t.Errorf("served %q", id)
		}
	}
	if _, page := StorePage(); page != ok {
		t.Error("a store page without a store ID")
	}
}

// The unpacked ID is the manifest key's, and it is ours, not MDM's.
func TestTheUnpackedIDIsTheKeys(t *testing.T) {
	id, ok := IDFromKey(ManifestKey)
	if !ok || id != UnpackedID {
		t.Errorf("IDFromKey = %q, %v; UnpackedID is %q", id, ok, UnpackedID)
	}
	if UnpackedID == "oogbclbbninliongijffeeggalppliok" {
		t.Error("MDM's ID")
	}
	if _, ok := IDFromKey("bm90IGEga2V5"); ok {
		t.Error("not a key")
	}
	origins := AllowedOrigins()
	if len(origins) == 0 || origins[0] != "chrome-extension://"+UnpackedID+"/" {
		t.Errorf("AllowedOrigins = %v", origins)
	}
	if !strings.HasSuffix(HostManifest, ".json") || strings.Contains(HostName, "misterdownloadmanager") {
		t.Error("host name")
	}
}

func TestSeenLinesOldAndNew(t *testing.T) {
	if got := parseSeen("Google Chrome\n"); !reflect.DeepEqual(got, []Seen{{"Google Chrome", ""}}) {
		t.Errorf("old: %v", got)
	}
	if got := parseSeen("Google Chrome\t0.2.5\r\nMozilla Firefox\t0.2.5\n"); len(got) != 2 || got[0].Version != "0.2.5" {
		t.Errorf("new: %v", got)
	}
}

func TestEachBrowserIsSeenOnceWithItsLatestVersion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if AnySeen(dir) || SeenBrowsers(dir) != nil {
		t.Fatal("seen before any")
	}
	for _, s := range []Seen{{chrome, "1.0"}, {firefox, "1.0"}, {chrome, "1.1"}, {chrome, "1.1"}, {"", "1.1"}} {
		if err := MarkSeen(dir, s.Browser, s.Version); err != nil {
			t.Fatal(err)
		}
	}
	want := []Seen{{firefox, "1.0"}, {chrome, "1.1"}}
	if got := SeenBrowsers(dir); !AnySeen(dir) || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
