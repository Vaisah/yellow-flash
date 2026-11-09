// Ported from Mister Download Manager, crates/ipc/src/browsers.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func mkdirs(t *testing.T, base string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func user(home string, mac bool) []HostDir {
	return HostDirs(home, filepath.Join(home, ".config"), mac, false, "")
}

func want(t *testing.T, got []HostDir, w ...HostDir) {
	t.Helper()
	if len(got) == 0 && len(w) == 0 {
		return
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("got %v\nwant %v", got, w)
	}
}

func TestNothingInstalledNothingWritten(t *testing.T) {
	h := t.TempDir()
	want(t, user(h, false))
	want(t, user(h, true))
}

// A Zen-only user has no ~/.mozilla; Zen reads it all the same.
func TestZenAloneGetsMozillasFolder(t *testing.T) {
	h := t.TempDir()
	mkdirs(t, h, ".zen")
	want(t, user(h, false), HostDir{filepath.Join(h, ".mozilla", "native-messaging-hosts"), Gecko})
	m := t.TempDir()
	mkdirs(t, m, "Library/Application Support/Zen")
	want(t, user(m, true), HostDir{filepath.Join(m, "Library", "Application Support", "Mozilla", "NativeMessagingHosts"), Gecko})
}

// Firefox with only the newer ~/.config/mozilla, or only the Snap's folder,
// still reads ~/.mozilla.
func TestFirefoxElsewhereGetsMozillasFolder(t *testing.T) {
	for _, d := range []string{".config/mozilla/firefox", "snap/firefox/common/.mozilla/firefox"} {
		h := t.TempDir()
		mkdirs(t, h, d)
		want(t, user(h, false), HostDir{filepath.Join(h, ".mozilla", "native-messaging-hosts"), Gecko})
	}
}

// Helium reads its own folder (then Chrome's, which a Helium-only user
// doesn't have).
func TestHeliumGetsItsOwnFolder(t *testing.T) {
	h := t.TempDir()
	mkdirs(t, h, ".config/net.imput.helium")
	want(t, user(h, false), HostDir{filepath.Join(h, ".config", "net.imput.helium", "NativeMessagingHosts"), Chromium})
	m := t.TempDir()
	mkdirs(t, m, "Library/Application Support/net.imput.helium")
	want(t, user(m, true), HostDir{filepath.Join(m, "Library", "Application Support", "net.imput.helium", "NativeMessagingHosts"), Chromium})
}

func TestSeveralBrowsersEachOnce(t *testing.T) {
	h := t.TempDir()
	mkdirs(t, h, ".config/google-chrome", ".mozilla", ".zen", ".librewolf")
	want(t, user(h, false),
		HostDir{filepath.Join(h, ".config", "google-chrome", "NativeMessagingHosts"), Chromium},
		HostDir{filepath.Join(h, ".mozilla", "native-messaging-hosts"), Gecko},
		HostDir{filepath.Join(h, ".librewolf", "native-messaging-hosts"), Gecko})
}

// Helium installs a chrome.exe; only Chrome's folder makes it Google Chrome.
func TestWindowsProgramsByFileAndFolder(t *testing.T) {
	cases := map[string]string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`:        "Google Chrome",
		`C:\Users\a\AppData\Local\imput\Helium\Application\chrome.exe`: "Helium",
		`C:\Users\a\AppData\Local\Chromium\Application\chrome.exe`:     "Chromium",
		`D:\Tools\chrome.exe`:                  "", // a chrome.exe elsewhere
		`C:\Program Files\Zen Browser\ZEN.EXE`: "Zen Browser",
		`C:\x\unknown.exe`:                     "",
	}
	for p, w := range cases {
		if got := WindowsNamed(p); got != w {
			t.Errorf("WindowsNamed(%q) = %q, want %q", p, got, w)
		}
	}
	for _, p := range WindowsPrograms {
		if _, ok := Named(p.Name); !ok {
			t.Errorf("%s is not in the table", p.Name)
		}
	}
}

func TestProgramsFromOpenCommands(t *testing.T) {
	if p, _ := CommandProgram(`"C:\Program Files\Zen Browser\zen.exe" -osint -url "%1"`); p != `C:\Program Files\Zen Browser\zen.exe` {
		t.Errorf("quoted: %q", p)
	}
	if p, _ := CommandProgram(`C:\Browsers\x.EXE %1`); p != `C:\Browsers\x.EXE` {
		t.Errorf("unquoted: %q", p)
	}
	for _, c := range []string{`""`, "rundll32"} {
		if p, ok := CommandProgram(c); ok {
			t.Errorf("%q gave %q", c, p)
		}
	}
	if WindowsCanRunExtension(`C:\Program Files\Internet Explorer\iexplore.exe`) {
		t.Error("Internet Explorer")
	}
	if !WindowsCanRunExtension(`C:\x\zen.exe`) {
		t.Error("Zen")
	}
}

func TestNamesAreUniqueAndFound(t *testing.T) {
	for _, b := range Browsers {
		if got, ok := Named(b.Name); !ok || got.Name != b.Name {
			t.Errorf("%s not found", b.Name)
		}
	}
	if _, ok := Named("Netscape"); ok {
		t.Error("Netscape found")
	}
}

// Brave Origin keeps its own folder on Linux and reads its manifest there;
// a user with both gets both.
func TestBraveOriginGetsItsOwnFolder(t *testing.T) {
	h := t.TempDir()
	mkdirs(t, h, ".config/BraveSoftware/Brave-Origin")
	want(t, user(h, false), HostDir{filepath.Join(h, ".config", "BraveSoftware", "Brave-Origin", "NativeMessagingHosts"), Chromium})
	mkdirs(t, h, ".config/BraveSoftware/Brave-Browser")
	if n := len(user(h, false)); n != 2 {
		t.Errorf("both: %d folders", n)
	}
}

// Brave, Brave Origin and their channels read Chrome's folder on macOS, even
// with no Chrome.
func TestBraveOnMacOSGetsChromesFolder(t *testing.T) {
	for _, folder := range []string{"BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Origin"} {
		m := t.TempDir()
		mkdirs(t, m, "Library/Application Support/"+folder)
		want(t, user(m, true), HostDir{filepath.Join(m, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts"), Chromium})
	}
}

// Every folder the table lists, alone in a home folder, gets the manifest
// where that browser reads it.
func TestEveryListedFolderIsCovered(t *testing.T) {
	for _, b := range Browsers {
		for _, mac := range []bool{false, true} {
			folders := b.MacData
			if !mac {
				folders = append(slices.Clone(b.LinuxConfig), b.LinuxHome...)
			}
			for _, folder := range folders {
				home := t.TempDir()
				support := filepath.Join(home, "Library", "Application Support")
				var data string
				switch {
				case mac:
					data = filepath.Join(support, folder)
				case slices.Contains(b.LinuxHome, folder):
					data = filepath.Join(home, folder)
				default:
					data = filepath.Join(home, ".config", folder)
				}
				if err := os.MkdirAll(data, 0o755); err != nil {
					t.Fatal(err)
				}
				var w string
				switch {
				case b.Family == Chromium && mac && b.MacHosts != "":
					w = filepath.Join(support, b.MacHosts, "NativeMessagingHosts")
				case b.Family == Chromium:
					w = filepath.Join(data, "NativeMessagingHosts")
				case mac:
					w = filepath.Join(support, "Mozilla", "NativeMessagingHosts")
				default:
					w = filepath.Join(home, ".mozilla", "native-messaging-hosts")
				}
				got := user(home, mac)
				if !slices.Contains(got, HostDir{w, b.Family}) {
					t.Errorf("%s (%s, mac %v): %v lacks %s", b.Name, folder, mac, got, w)
				}
			}
		}
	}
}

// A browser that reads a package's manifest for everyone, naming this
// helper, gets no user folder; a manifest naming another program, or one
// that doesn't run, doesn't count. Mozilla's user folder stays while a fork
// that reads only it is there.
func TestAManifestForEveryoneCoversItsBrowsers(t *testing.T) {
	tmp := t.TempDir()
	root, home := filepath.Join(tmp, "root"), filepath.Join(tmp, "home")
	mkdirs(t, home, ".config/google-chrome", ".config/BraveSoftware/Brave-Browser", ".config/opera", ".mozilla")
	helper := filepath.Join(root, "usr", "bin", "yellowflash-nmhost")
	mkdirs(t, filepath.Dir(helper), ".")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	system := func(d, program string) {
		dir := filepath.Join(root, strings.TrimPrefix(d, "/"))
		mkdirs(t, dir, ".")
		raw, _ := json.Marshal(map[string]string{"name": "x", "path": program, "type": "stdio"})
		if err := os.WriteFile(filepath.Join(dir, HostManifest), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirs := func(host string) []string {
		var v []string
		for _, d := range hostDirsIn(root, home, filepath.Join(home, ".config"), false, false, host) {
			rel, _ := filepath.Rel(home, d.Dir)
			v = append(v, filepath.ToSlash(rel))
		}
		return v
	}
	const (
		chromeDir = ".config/google-chrome/NativeMessagingHosts"
		braveDir  = ".config/BraveSoftware/Brave-Browser/NativeMessagingHosts"
		operaDir  = ".config/opera/NativeMessagingHosts"
		mozDir    = ".mozilla/native-messaging-hosts"
	)
	check := func(what string, got []string, w ...string) {
		t.Helper()
		if !slices.Equal(got, w) {
			t.Errorf("%s: got %v, want %v", what, got, w)
		}
	}
	check("nothing for everyone", dirs(helper), chromeDir, braveDir, operaDir, mozDir)

	// Chrome's for everyone: Chrome and Brave read it, Opera doesn't.
	system(etcChrome, helper)
	check("Chrome's", dirs(helper), operaDir, mozDir)
	// Unregistering (no helper given) still sees every user folder.
	check("unregistering", dirs(""), chromeDir, braveDir, operaDir, mozDir)
	// Firefox's, in lib64 (Fedora's).
	system(mozilla[1], helper)
	check("Firefox's", dirs(helper), operaDir)
	// Zen reads only Mozilla's user folder: it stays.
	mkdirs(t, home, ".zen")
	check("Zen", dirs(helper), operaDir, mozDir)
	if err := os.Remove(filepath.Join(home, ".zen")); err != nil {
		t.Fatal(err)
	}

	// Another helper (a portable copy) isn't covered by the package's.
	other := filepath.Join(tmp, "copy", "yellowflash-nmhost")
	mkdirs(t, filepath.Dir(other), ".")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	check("another helper", dirs(other), chromeDir, braveDir, operaDir, mozDir)
	if runtime.GOOS == "windows" {
		return // no execute bit to take away
	}
	// A manifest naming a helper that can't run covers nothing.
	if err := os.Chmod(helper, 0o644); err != nil {
		t.Fatal(err)
	}
	check("a helper that can't run", dirs(helper), chromeDir, braveDir, operaDir, mozDir)
}

// The folders for everyone each browser reads are in the list packages ship.
func TestEveryBrowsersSystemFolderIsShipped(t *testing.T) {
	for _, b := range Browsers {
		for _, d := range b.LinuxSystem {
			if !slices.Contains(LinuxSystemHostDirs, SystemDir{d, b.Family}) {
				t.Errorf("%s reads %s", b.Name, d)
			}
		}
	}
}

func TestAManifestsProgram(t *testing.T) {
	d := t.TempDir()
	m := filepath.Join(d, "host.json")
	abs := filepath.Join(d, "elsewhere", "host")
	raw, _ := json.Marshal(map[string]string{"name": "x", "path": abs, "type": "stdio"})
	write := func(s []byte) {
		if err := os.WriteFile(m, s, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(raw)
	if p, ok := ManifestProgram(m); !ok || p != abs {
		t.Errorf("absolute: %q", p)
	}
	write([]byte(`{"name":"x","path":"host.exe"}`))
	if p, ok := ManifestProgram(m); !ok || p != filepath.Join(d, "host.exe") {
		t.Errorf("relative: %q", p)
	}
	for _, bad := range []string{`{"name":"x"}`, `{"path":""}`, "not json"} {
		write([]byte(bad))
		if p, ok := ManifestProgram(m); ok {
			t.Errorf("%s gave %q", bad, p)
		}
	}
	if _, ok := ManifestProgram(filepath.Join(d, "none.json")); ok {
		t.Error("no file")
	}
}
