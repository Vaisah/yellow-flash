// Ported from Mister Download Manager, crates/remote/src/devices.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package remote

// The paired devices (remote/devices.json, readable by the user only).
// Each has a token of its own (256 random bits) that its browser keeps and
// sends with every request; the file holds only the token's SHA-256, so the
// file alone opens nothing. A device removed in Options is refused from its
// next request on.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/vaisah/internal/ipc"
)

// Device is one paired device.
type Device struct {
	// ID is a short id of its own (Options' Remove names it).
	ID string `json:"id"`
	// Name is the one it was given when it paired ("Pixel 8").
	Name string `json:"name"`
	// Added and Seen are when it paired and was last seen, Unix seconds.
	Added int64 `json:"added"`
	Seen  int64 `json:"seen"`
	// hash is its token's SHA-256, hex.
	hash string
}

type deviceFile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Added int64  `json:"added"`
	Seen  int64  `json:"seen"`
	Hash  string `json:"hash"`
}

// MaxName is the longest device name kept (characters).
const MaxName = 40

// Devices are the paired devices, kept in a file.
type Devices struct {
	mu   sync.Mutex
	path string // "": in memory only (tests)
	list []Device
	// savedSeen is when the last-seen times were last written (they change
	// at every request).
	savedSeen time.Time
	// OnSaveError is told when the list could not be saved.
	OnSaveError func(error)
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// A zeroed token would open everything: stop.
		panic(err)
	}
	return b
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CleanName is a name as given, made safe to show: no control characters,
// at most MaxName, trimmed.
func CleanName(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if unicode.IsControl(r) || r == '�' {
			continue
		}
		if n == MaxName {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// LoadDevices is the list kept at path ("": in memory only).
func LoadDevices(path string) *Devices {
	d := &Devices{path: path, savedSeen: time.Now()}
	if path == "" {
		return d
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	var kept []deviceFile
	if json.Unmarshal(b, &kept) != nil {
		return d
	}
	for _, k := range kept {
		if k.ID != "" && len(k.Hash) == 64 {
			d.list = append(d.list, Device{ID: k.ID, Name: k.Name, Added: k.Added, Seen: k.Seen, hash: k.Hash})
		}
	}
	return d
}

func (d *Devices) saveLocked() {
	d.savedSeen = time.Now()
	if d.path == "" {
		return
	}
	kept := make([]deviceFile, 0, len(d.list))
	for _, v := range d.list {
		kept = append(kept, deviceFile{ID: v.ID, Name: v.Name, Added: v.Added, Seen: v.Seen, Hash: v.hash})
	}
	b, _ := json.MarshalIndent(kept, "", "  ")
	err := ipc.PrivateDir(filepath.Dir(d.path))
	if err == nil {
		err = ipc.PrivateWrite(d.path, b)
	}
	if err != nil && d.OnSaveError != nil {
		d.OnSaveError(err)
	}
}

// List are the paired devices.
func (d *Devices) List() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.list)
}

// Add pairs a new device called name: its record and its token (given
// once, to the device).
func (d *Devices) Add(name string) (Device, string) {
	token := hex.EncodeToString(random(32))
	d.mu.Lock()
	defer d.mu.Unlock()
	id := hex.EncodeToString(random(4))
	for slices.ContainsFunc(d.list, func(v Device) bool { return v.ID == id }) {
		id = hex.EncodeToString(random(4))
	}
	name = CleanName(name)
	if name == "" {
		name = "?"
	}
	now := time.Now().Unix()
	dev := Device{ID: id, Name: name, Added: now, Seen: now, hash: hashToken(token)}
	d.list = append(d.list, dev)
	d.saveLocked()
	return dev, token
}

// Check is the device token belongs to (its last-seen time brought up to
// date); false when none.
func (d *Devices) Check(token string) (Device, bool) {
	h := []byte(hashToken(token))
	d.mu.Lock()
	defer d.mu.Unlock()
	// Every entry is compared in full, whatever matches.
	found := -1
	for i, v := range d.list {
		if subtle.ConstantTimeCompare([]byte(v.hash), h) == 1 {
			found = i
		}
	}
	if found < 0 {
		return Device{}, false
	}
	d.list[found].Seen = time.Now().Unix()
	// Last-seen times are written at most every ten minutes.
	if time.Since(d.savedSeen) > 10*time.Minute {
		d.saveLocked()
	}
	return d.list[found], true
}

// Remove unpairs device id; true when it was there.
func (d *Devices) Remove(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	before := len(d.list)
	d.list = slices.DeleteFunc(d.list, func(v Device) bool { return v.ID == id })
	if len(d.list) == before {
		return false
	}
	d.saveLocked()
	return true
}
