// Ported from Mister Download Manager, crates/ipc/src/browsers.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package browser

// The browsers the extension runs in, and where each reads native messaging
// host manifests: one table for the native host (which writes the
// manifests) and the app (which lists the browsers and checks the manifests
// are there).
//
// Sources, as MDM's: each browser's own code or packaging (Chromium's
// launch_context_win.cc and chrome_paths*.cc, Firefox's nsXREDirProvider.cpp,
// Helium's patches, Brave's brave_main_delegate.cc). Folders marked
// "unverified" come from other projects' installers and cost nothing when
// absent.
//
// Everyone's folders on Linux (LinuxSystemHostDirs) are each browser's
// documented one: Chrome's /etc/opt/chrome and every other Chromium build's
// /etc/chromium (chrome_paths.cc), Edge's /etc/opt/edge, Firefox's
// /usr/lib/mozilla or /usr/lib64/mozilla (MDN, "Native manifests"). Opera,
// Helium, Thorium and Firefox's forks document none.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Family says which manifest a browser reads: Chromium's (allowed_origins)
// or Firefox's (allowed_extensions).
type Family int

const (
	Chromium Family = iota
	Gecko
)

func (f Family) String() string {
	if f == Gecko {
		return "gecko"
	}
	return "chromium"
}

// Browser is a browser known by name.
type Browser struct {
	// Name is its name in the app (Options, the offer window).
	Name   string
	Family Family
	// MacApps are its app bundles, in /Applications or ~/Applications.
	MacApps []string
	// LinuxCmds are its commands on the PATH.
	LinuxCmds []string
	// MacData are its data folders under ~/Library/Application Support. A
	// Chromium browser reads <folder>/NativeMessagingHosts; for a Gecko one
	// the folder only says it is there.
	MacData []string
	// LinuxConfig: the same, under $XDG_CONFIG_HOME (~/.config).
	LinuxConfig []string
	// LinuxHome are Gecko data folders under $HOME (.zen).
	LinuxHome []string
	// LinuxSystem are the folders for everyone it reads host manifests from
	// as well, after its user folder; none known for some.
	LinuxSystem []string
	// MacHosts is the folder under ~/Library/Application Support whose
	// NativeMessagingHosts a Chromium browser reads when it isn't its own
	// data folder (Brave reads Chrome's).
	MacHosts string
}

// Everyone's folders on Linux.
const (
	etcChrome   = "/etc/opt/chrome/native-messaging-hosts"
	etcChromium = "/etc/chromium/native-messaging-hosts"
	etcEdge     = "/etc/opt/edge/native-messaging-hosts"
)

// mozilla: Firefox's, lib64's on 64-bit Fedora and openSUSE.
var mozilla = []string{"/usr/lib/mozilla/native-messaging-hosts", "/usr/lib64/mozilla/native-messaging-hosts"}

// SystemDir is a Linux folder for everyone with the family whose manifest
// goes there.
type SystemDir struct {
	Dir    string
	Family Family
}

// LinuxSystemHostDirs are every folder for everyone a browser reads host
// manifests from: where packages put theirs, and --register --all-users too.
var LinuxSystemHostDirs = []SystemDir{
	{etcChrome, Chromium}, {etcChromium, Chromium}, {etcEdge, Chromium},
	{mozilla[0], Gecko}, {mozilla[1], Gecko},
}

// Browsers are every browser named, Chromium's first.
var Browsers = []Browser{
	// Each channel keeps its own folder.
	{Name: "Google Chrome", Family: Chromium,
		MacApps:     []string{"Google Chrome.app", "Google Chrome Beta.app", "Google Chrome Dev.app", "Google Chrome Canary.app"},
		LinuxCmds:   []string{"google-chrome", "google-chrome-stable", "google-chrome-beta", "google-chrome-unstable", "google-chrome-canary"},
		MacData:     []string{"Google/Chrome", "Google/Chrome Beta", "Google/Chrome Dev", "Google/Chrome Canary"},
		LinuxConfig: []string{"google-chrome", "google-chrome-beta", "google-chrome-unstable", "google-chrome-canary"},
		LinuxSystem: []string{etcChrome}},
	{Name: "Chromium", Family: Chromium,
		MacApps: []string{"Chromium.app"}, LinuxCmds: []string{"chromium", "chromium-browser"},
		MacData: []string{"Chromium"}, LinuxConfig: []string{"chromium"},
		LinuxSystem: []string{etcChromium}},
	{Name: "Microsoft Edge", Family: Chromium,
		MacApps:     []string{"Microsoft Edge.app", "Microsoft Edge Beta.app", "Microsoft Edge Dev.app", "Microsoft Edge Canary.app"},
		LinuxCmds:   []string{"microsoft-edge", "microsoft-edge-stable", "microsoft-edge-beta", "microsoft-edge-dev"},
		MacData:     []string{"Microsoft Edge", "Microsoft Edge Beta", "Microsoft Edge Dev", "Microsoft Edge Canary"},
		LinuxConfig: []string{"microsoft-edge", "microsoft-edge-beta", "microsoft-edge-dev"},
		LinuxSystem: []string{etcEdge}},
	// Brave and Brave Origin, each channel in its own folder, all named
	// "Brave" to the extension. On Linux each reads its own folder; on macOS
	// all read Chrome's; on Windows, Chromium's and Chrome's keys.
	{Name: "Brave", Family: Chromium,
		MacApps: []string{"Brave Browser.app", "Brave Browser Beta.app", "Brave Browser Nightly.app",
			"Brave Origin.app", "Brave Origin Beta.app", "Brave Origin Nightly.app"},
		LinuxCmds: []string{"brave-browser", "brave", "brave-browser-beta", "brave-browser-nightly",
			"brave-origin", "brave-origin-beta", "brave-origin-nightly"},
		MacData: []string{"BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Browser-Beta", "BraveSoftware/Brave-Browser-Nightly",
			"BraveSoftware/Brave-Origin", "BraveSoftware/Brave-Origin-Beta", "BraveSoftware/Brave-Origin-Nightly"},
		LinuxConfig: []string{"BraveSoftware/Brave-Browser", "BraveSoftware/Brave-Browser-Beta", "BraveSoftware/Brave-Browser-Dev",
			"BraveSoftware/Brave-Browser-Nightly", "BraveSoftware/Brave-Origin", "BraveSoftware/Brave-Origin-Beta",
			"BraveSoftware/Brave-Origin-Dev", "BraveSoftware/Brave-Origin-Nightly"},
		MacHosts: "Google/Chrome", LinuxSystem: []string{etcChrome}},
	{Name: "Vivaldi", Family: Chromium,
		MacApps: []string{"Vivaldi.app"}, LinuxCmds: []string{"vivaldi", "vivaldi-stable", "vivaldi-snapshot"},
		MacData: []string{"Vivaldi"}, LinuxConfig: []string{"vivaldi", "vivaldi-snapshot"},
		LinuxSystem: []string{etcChromium}},
	// Opera's folders: unverified.
	{Name: "Opera", Family: Chromium,
		MacApps: []string{"Opera.app"}, LinuxCmds: []string{"opera"},
		MacData: []string{"com.operasoftware.Opera"}, LinuxConfig: []string{"opera"}},
	// Helium reads its own folder, then Chrome's; .dev is an unofficial build's.
	{Name: "Helium", Family: Chromium,
		MacApps: []string{"Helium.app"}, LinuxCmds: []string{"helium", "helium-browser"},
		MacData: []string{"net.imput.helium"}, LinuxConfig: []string{"net.imput.helium", "net.imput.helium.dev"}},
	// Thorium's and Arc's folders: unverified.
	{Name: "Thorium", Family: Chromium,
		MacApps: []string{"Thorium.app"}, LinuxCmds: []string{"thorium-browser"},
		MacData: []string{"Thorium"}, LinuxConfig: []string{"thorium"}},
	{Name: "Arc", Family: Chromium, MacApps: []string{"Arc.app"}, MacData: []string{"Arc/User Data"}},
	// The Snap (Ubuntu's own Firefox) keeps its profile in its snap folder and
	// never makes ~/.mozilla; it reaches the host through the desktop portal,
	// which reads ~/.mozilla's, then Mozilla's folders for everyone.
	{Name: "Mozilla Firefox", Family: Gecko,
		MacApps: []string{"Firefox.app"}, LinuxCmds: []string{"firefox"},
		MacData: []string{"Mozilla", "Firefox"}, LinuxConfig: []string{"mozilla"},
		LinuxHome: []string{".mozilla", "snap/firefox/common/.mozilla"}, LinuxSystem: mozilla},
	{Name: "Zen Browser", Family: Gecko,
		MacApps: []string{"Zen.app", "Zen Browser.app"}, LinuxCmds: []string{"zen", "zen-browser", "zen-bin"},
		MacData: []string{"Zen"}, LinuxConfig: []string{"zen"}, LinuxHome: []string{".zen"}},
	{Name: "LibreWolf", Family: Gecko,
		MacApps: []string{"LibreWolf.app"}, LinuxCmds: []string{"librewolf"},
		MacData: []string{"LibreWolf"}, LinuxHome: []string{".librewolf"}},
	// Floorp's and Waterfox's: unverified (they read Firefox's folder).
	{Name: "Floorp", Family: Gecko,
		MacApps: []string{"Floorp.app"}, LinuxCmds: []string{"floorp"},
		MacData: []string{"Floorp"}, LinuxHome: []string{".floorp"}},
	{Name: "Waterfox", Family: Gecko,
		MacApps: []string{"Waterfox.app"}, LinuxCmds: []string{"waterfox"},
		MacData: []string{"Waterfox"}, LinuxHome: []string{".waterfox"}},
}

// Named returns the browser named name in the table.
func Named(name string) (*Browser, bool) {
	for i := range Browsers {
		if Browsers[i].Name == name {
			return &Browsers[i], true
		}
	}
	return nil, false
}

// WindowsProgram is a browser's program on Windows, with the folder that
// tells apart those that share a program's name (Chrome, Chromium and
// Helium all install a chrome.exe).
type WindowsProgram struct {
	Exe, Folder, Name string
}

// WindowsPrograms are the programs of the browsers in the table.
var WindowsPrograms = []WindowsProgram{
	{"chrome.exe", `\google\chrome\`, "Google Chrome"},
	{"chrome.exe", `\imput\helium\`, "Helium"},
	{"chrome.exe", `\chromium\`, "Chromium"},
	{"msedge.exe", "", "Microsoft Edge"},
	{"brave.exe", "", "Brave"},
	{"vivaldi.exe", "", "Vivaldi"},
	{"opera.exe", "", "Opera"},
	{"thorium.exe", "", "Thorium"},
	{"firefox.exe", "", "Mozilla Firefox"},
	{"zen.exe", "", "Zen Browser"},
	{"librewolf.exe", "", "LibreWolf"},
	{"floorp.exe", "", "Floorp"},
	{"waterfox.exe", "", "Waterfox"},
}

// WindowsNamed returns the browser program (a full path) is, by its file
// name and folder; "" for a program not in the table (or a chrome.exe in
// another folder).
func WindowsNamed(program string) string {
	lower := strings.ToLower(program)
	file := lower
	if i := strings.LastIndexAny(lower, `\/`); i >= 0 {
		file = lower[i+1:]
	}
	for _, p := range WindowsPrograms {
		if p.Exe == file && (p.Folder == "" || strings.Contains(lower, p.Folder)) {
			return p.Name
		}
	}
	return ""
}

// CommandProgram returns the program a shell\open\command value starts
// (`"C:\…\zen.exe" -osint -url "%1"` or unquoted `C:\…\x.exe %1`).
func CommandProgram(command string) (string, bool) {
	c := strings.TrimSpace(command)
	var program string
	if rest, ok := strings.CutPrefix(c, `"`); ok {
		program, _, _ = strings.Cut(rest, `"`)
	} else {
		end := strings.Index(strings.ToLower(c), ".exe")
		if end < 0 {
			return "", false
		}
		program = c[:end+4]
	}
	program = strings.TrimSpace(program)
	return program, program != ""
}

// WindowsCanRunExtension: Windows lists Internet Explorer as a browser; it
// can't run the extension.
func WindowsCanRunExtension(program string) bool {
	return !strings.HasSuffix(strings.ToLower(program), "iexplore.exe")
}

// HostDir is a folder to write the host manifest into, with the family
// whose manifest goes there.
type HostDir struct {
	Dir    string
	Family Family
}

// HostDirs returns, on Linux and macOS (mac), the folders to write the host
// manifest into, for this user (home, config = $XDG_CONFIG_HOME) or for
// everyone. A user's folder is given only for a browser that is there.
//
// Firefox and its forks all read Mozilla's own user folder,
// ~/.mozilla/native-messaging-hosts or ~/Library/Application Support/
// Mozilla/NativeMessagingHosts, even when they keep their data elsewhere:
// it is given, to be created, when any of them is there.
//
// host (Linux, for one user, "" otherwise): the helper being registered. A
// browser that already reads a manifest for everyone naming it (a
// package's) gets no user folder, so nothing is left in the home folder
// when the package goes. Mozilla's user folder is given while one Gecko
// browser there doesn't read such a manifest.
func HostDirs(home, config string, mac, allUsers bool, host string) []HostDir {
	return hostDirsIn(string(filepath.Separator), home, config, mac, allUsers, host)
}

// hostDirsIn is HostDirs with everyone's folders under root.
func hostDirsIn(root, home, config string, mac, allUsers bool, host string) []HostDir {
	system := func(d string) string { return filepath.Join(root, strings.TrimPrefix(d, "/")) }
	var dirs []HostDir
	if allUsers {
		if mac {
			for _, d := range []string{
				"/Library/Google/Chrome/NativeMessagingHosts",
				"/Library/Application Support/Chromium/NativeMessagingHosts",
				"/Library/Microsoft/Edge/NativeMessagingHosts",
			} {
				dirs = append(dirs, HostDir{system(d), Chromium})
			}
			dirs = append(dirs, HostDir{system("/Library/Application Support/Mozilla/NativeMessagingHosts"), Gecko})
		} else {
			for _, s := range LinuxSystemHostDirs {
				dirs = append(dirs, HostDir{system(s.Dir), s.Family})
			}
		}
		// Everyone's: only where the browser's folder is.
		return slices.DeleteFunc(dirs, func(d HostDir) bool { return !isDir(filepath.Dir(d.Dir)) })
	}
	covered := func(b *Browser) bool {
		if mac || host == "" {
			return false
		}
		for _, d := range b.LinuxSystem {
			if NamesProgram(filepath.Join(system(d), HostManifest), host) {
				return true
			}
		}
		return false
	}
	support := filepath.Join(home, "Library", "Application Support")
	data := func(b *Browser) []string {
		var v []string
		if mac {
			for _, d := range b.MacData {
				v = append(v, filepath.Join(support, d))
			}
			return v
		}
		for _, d := range b.LinuxConfig {
			v = append(v, filepath.Join(config, d))
		}
		for _, d := range b.LinuxHome {
			v = append(v, filepath.Join(home, d))
		}
		return v
	}
	push := func(d string, f Family) {
		if !slices.ContainsFunc(dirs, func(x HostDir) bool { return x.Dir == d }) {
			dirs = append(dirs, HostDir{d, f})
		}
	}
	geckoThere := false
	for i := range Browsers {
		b := &Browsers[i]
		there := slices.DeleteFunc(data(b), func(d string) bool { return !isDir(d) })
		if len(there) == 0 || covered(b) {
			continue
		}
		switch {
		case b.Family == Gecko:
			geckoThere = true
		case mac && b.MacHosts != "":
			// Brave on macOS: Chrome's folder, made if need be.
			push(filepath.Join(support, b.MacHosts, "NativeMessagingHosts"), Chromium)
		default:
			for _, d := range there {
				push(filepath.Join(d, "NativeMessagingHosts"), Chromium)
			}
		}
	}
	if geckoThere {
		if mac {
			push(filepath.Join(support, "Mozilla", "NativeMessagingHosts"), Gecko)
			// LibreWolf reads its own first.
			if isDir(filepath.Join(support, "LibreWolf")) {
				push(filepath.Join(support, "LibreWolf", "NativeMessagingHosts"), Gecko)
			}
		} else {
			push(filepath.Join(home, ".mozilla", "native-messaging-hosts"), Gecko)
			if isDir(filepath.Join(home, ".librewolf")) {
				push(filepath.Join(home, ".librewolf", "native-messaging-hosts"), Gecko)
			}
		}
	}
	return dirs
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// NamesProgram reports whether the host manifest at manifest starts program
// (the same file), and it runs.
func NamesProgram(manifest, program string) bool {
	named, ok := ManifestProgram(manifest)
	if !ok {
		return false
	}
	a, err1 := canonical(named)
	b, err2 := canonical(program)
	return err1 == nil && err2 == nil && a == b && runs(named)
}

func canonical(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// runs reports a program file that can be run.
func runs(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	// Windows reads its manifests through the registry, not these folders.
	return runtime.GOOS == "windows" || fi.Mode().Perm()&0o111 != 0
}

// ManifestProgram returns the program the host manifest at manifest starts
// (its path; a relative one, which Windows allows, is beside the manifest),
// when the manifest can be read.
func ManifestProgram(manifest string) (string, bool) {
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return "", false
	}
	var m struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Path == nil {
		return "", false
	}
	p := strings.TrimSpace(*m.Path)
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(manifest), p)
	}
	return p, true
}
