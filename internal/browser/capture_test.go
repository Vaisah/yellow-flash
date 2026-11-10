// Ported from Mister Download Manager, crates/ipc/src/capture.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	chromeUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	edgeUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0"
	firefoxUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:145.0) Gecko/20100101 Firefox/145.0"
	chrome    = "Google Chrome"
	edge      = "Microsoft Edge"
	firefox   = "Mozilla Firefox"
)

func TestBrowsersByName(t *testing.T) {
	cases := []struct {
		ua     string
		brands []string
		want   string
	}{
		{chromeUA, nil, chrome},
		{edgeUA, nil, edge},
		{firefoxUA, nil, firefox},
		{chromeUA, []string{"Not.A/Brand", "Chromium", "Brave"}, "Brave Browser"},
		{chromeUA, []string{"Chromium", "Google Chrome"}, chrome},
		{chromeUA, []string{"Opera"}, "Opera"},
		// Firefox's forks by runtime.getBrowserInfo's name; Firefox itself by its UA.
		{firefoxUA, []string{"Zen"}, "Zen Browser"},
		{firefoxUA, []string{"LibreWolf"}, "LibreWolf"},
		{firefoxUA, []string{"Firefox"}, firefox},
		// Helium's brands are Chrome's (spoofed on purpose).
		{chromeUA, []string{"Chromium", "Google Chrome", "Not.A/Brand"}, chrome},
	}
	for _, c := range cases {
		if got := BrowserNamed(c.ua, c.brands); got != c.want {
			t.Errorf("BrowserNamed(%q, %v) = %q, want %q", c.ua, c.brands, got, c.want)
		}
	}
}

func TestTheVideoButtonFollowsItsOptions(t *testing.T) {
	r := DefaultRules()
	r.VideoPanelSites = "*.example.com tube.test"
	if !r.PanelOn("https://videos.site/watch?v=1", chrome) {
		t.Error("an unlisted site")
	}
	if r.PanelOn("https://www.example.com/v", chrome) {
		t.Error("a listed site")
	}
	if r.PanelOn("https://example.com/v", chrome) {
		t.Error("*. takes the domain too")
	}
	if r.PanelOn("https://TUBE.test/v", firefox) {
		t.Error("any case")
	}
	r.Browsers = []BrowserCapture{{firefox, false}}
	if r.PanelOn("https://videos.site/", firefox) {
		t.Error("a browser not captured")
	}
	if !r.PanelOn("https://videos.site/", chrome) {
		t.Error("another browser")
	}
	r.VideoPanel = false
	if r.PanelOn("https://videos.site/", chrome) {
		t.Error("the button is off")
	}
	r.VideoPanel = true
	r.AdvancedIntegration = false
	if r.PanelOn("https://videos.site/", chrome) {
		t.Error("integration is off")
	}
}

func TestSmallFilesByOptions(t *testing.T) {
	r := DefaultRules()
	if r.TooSmall(10) {
		t.Error("any size by default")
	}
	r.CaptureMinKB = 100
	if !r.TooSmall(15 * 1024) {
		t.Error("15 KB is under 100")
	}
	if r.TooSmall(100*1024) || r.TooSmall(0) || r.TooSmall(-1) {
		t.Error("100 KB, or an unknown size, is taken")
	}
}

func TestASiteIsAddedOnce(t *testing.T) {
	cases := []struct{ sites, host, want string }{
		{"", "Video.Site", "video.site"},
		{"a.test", "b.test", "a.test b.test"},
		{"*.b.test", "www.b.test", "*.b.test"}, // already covered
		{"a.test", " ", "a.test"},
		{"a.test", "b.test *", "a.test"}, // one host only
		{"a.test", "*", "a.test"},
	}
	for _, c := range cases {
		if got := AddSite(c.sites, c.host); got != c.want {
			t.Errorf("AddSite(%q, %q) = %q, want %q", c.sites, c.host, got, c.want)
		}
	}
}

func accepts(t *testing.T, r CaptureRules, url, name, browser string, want Declined) {
	t.Helper()
	d, ok := r.Accepts(url, name, browser)
	if want == "" && !ok {
		t.Errorf("%s from %s declined: %s", url, browser, d)
	}
	if want != "" && (ok || d != want) {
		t.Errorf("%s from %s: got (%q, %v), want %q", url, browser, d, ok, want)
	}
}

func TestTheDefaultsTakeListedTypesFromAnyBrowser(t *testing.T) {
	r := DefaultRules()
	accepts(t, r, "https://example.com/a/setup.exe", "setup.exe", chrome, "")
	accepts(t, r, "https://example.com/movie.part.r01", "movie.part.r01", firefox, "")
	accepts(t, r, "https://example.com/page.html", "page.html", chrome, FileType)
	// A .torrent, by default; a list saved before keeps what it had.
	accepts(t, r, "https://example.com/dl/set.torrent", "set.torrent", chrome, "")
	saved := ParseRules(`{"autoTypes":"ZIP EXE"}`)
	accepts(t, saved, "https://example.com/dl/set.torrent", "set.torrent", chrome, FileType)
	if !saved.AdvancedIntegration || saved.ExcludedSites != DefaultExcludedSites {
		t.Error("what the saved rules leave out keeps its default")
	}
}

func TestExcludedSitesAddressesAndBrowsersAreLeftToTheBrowser(t *testing.T) {
	r := DefaultRules()
	accepts(t, r, "https://dl.update.microsoft.com/x.exe", "x.exe", chrome, Site)
	r.ExcludedAddresses = []string{"https://example.com/private/*"}
	accepts(t, r, "https://example.com/private/a.zip", "a.zip", chrome, Address)
	accepts(t, r, "https://example.com/public/a.zip", "a.zip", chrome, "")
	r.Browsers = []BrowserCapture{{edge, false}, {chrome, true}}
	accepts(t, r, "https://example.com/a.zip", "a.zip", edge, BrowserOff)
	accepts(t, r, "https://example.com/a.zip", "a.zip", chrome, "")
	r.AdvancedIntegration = false
	accepts(t, r, "https://example.com/a.zip", "a.zip", chrome, IntegrationOff)
}

func TestHostsAndPatterns(t *testing.T) {
	if got := HostOf("https://user:pw@Example.COM:8080/x?y"); got != "example.com" {
		t.Errorf("HostOf = %q", got)
	}
	if got := HostOf("http://[::1]:80/"); got != "::1" {
		t.Errorf("HostOf IPv6 = %q", got)
	}
	checks := []struct {
		name string
		got  bool
		want bool
	}{
		{"subdomain", HostMatches("a.b.example.com", "*.example.com"), true},
		{"the domain itself", HostMatches("example.com", "*.example.com"), true},
		{"not a subdomain", HostMatches("badexample.com", "*.example.com"), false},
		{"wildcard", wildcard("*/files/*.zip", "https://x.org/files/a.zip"), true},
		{"wildcard, another type", wildcard("*/files/*.zip", "https://x.org/files/a.rar"), false},
		{"no scheme", AddressMatches("example.com/private/*", "https://Example.com/private/a.zip"), true},
		{"scheme", AddressMatches("https://example.com/private/*", "https://example.com/private/a"), true},
		{"other scheme", AddressMatches("https://example.com/private/*", "http://example.com/private/a"), false},
		{"blank", AddressMatches(" ", "https://x.org/"), false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v", c.name, c.got)
		}
	}
}

func TestAnEarlierDefaultListBecomesTodaysAndAnEditedOneStays(t *testing.T) {
	for _, old := range earlierAutoTypes {
		if got, ok := UpgradedTypes(old); !ok || got != DefaultAutoTypes {
			t.Errorf("not upgraded: %.40s…", old)
		}
		// In another order or case: the same list, never edited.
		f := strings.Fields(strings.ToLower(old))
		slices.Reverse(f)
		if _, ok := UpgradedTypes(strings.Join(f, "  ")); !ok {
			t.Errorf("shuffled not upgraded: %.40s…", old)
		}
	}
	// The first list: a .dmg, .deb or .webm is taken once upgraded.
	r := DefaultRules()
	r.AutoTypes = earlierAutoTypes[0]
	r = r.Upgraded()
	for _, name := range []string{"MDM.dmg", "mdm_0.3.0_amd64.deb", "a.AppImage", "clip.webm", "x.tar.zst"} {
		if !r.AutoType(name) {
			t.Error(name)
		}
	}
	// Edited by the user: one type more, one less, or their own list.
	more := earlierAutoTypes[0] + " DMG"
	less := strings.Replace(earlierAutoTypes[1], " ZIP", "", 1)
	for _, edited := range []string{more, less, "ZIP RAR", ""} {
		if _, ok := UpgradedTypes(edited); ok {
			t.Errorf("an edited list was upgraded: %q", edited)
		}
		r := DefaultRules()
		r.AutoTypes = edited
		if r.Upgraded().AutoTypes != edited {
			t.Errorf("an edited list changed: %q", edited)
		}
	}
	// Today's needs nothing; it holds every earlier default's types.
	if _, ok := UpgradedTypes(DefaultAutoTypes); ok {
		t.Error("today's list was upgraded")
	}
	today := strings.Fields(DefaultAutoTypes)
	for _, old := range earlierAutoTypes {
		for _, ty := range strings.Fields(old) {
			if !slices.Contains(today, ty) {
				t.Errorf("today's list lacks %s", ty)
			}
		}
	}
}

func TestTodaysBigFilesAreTakenAndEverydayBrowsingIsNot(t *testing.T) {
	earlier := DefaultRules()
	earlier.AutoTypes = earlierAutoTypes[2]
	if earlier.AutoType("model.Q4_K_M.gguf") {
		t.Error("the earlier list took a .gguf")
	}
	r := earlier.Upgraded()
	for _, name := range []string{
		"model.Q4_K_M.gguf", "model-00001-of-00004.safetensors", "sd15.ckpt", "weights.pth",
		"model.onnx", "train-00000.parquet", "scene.glb", "part.stl", "character.fbx",
		"house.blend", "bracket.step", "IMG_0001.CR3", "cover.psd", "book.epub", "report.docx",
		"budget.xlsx", "disk.vhdx", "backup.7z.001", "backup.z01", "movie.r27",
	} {
		if !r.AutoType(name) {
			t.Errorf("not taken: %s", name)
		}
	}
	for _, name := range []string{
		"index.html", "app.js", "style.css", "config.json", "notes.txt",
		"photo.jpg", "logo.png", "icon.svg", "addon.xpi", "addon.crx",
	} {
		if r.AutoType(name) {
			t.Errorf("taken: %s", name)
		}
	}
}

func TestALoneStarTakesEveryFileWithAType(t *testing.T) {
	r := DefaultRules()
	r.AutoTypes = "*"
	for _, name := range []string{"model.gguf", "photo.jpg", "anything.xyz123"} {
		accepts(t, r, "https://x.test/f", name, chrome, "")
	}
	if r.AutoType("no_extension") {
		t.Error("a name without a type")
	}
}

func TestACopiedAddressIsOfferedOnlyAsItAlwaysWas(t *testing.T) {
	// What people copy to paste somewhere else, never a download by default.
	for _, name := range []string{
		"photo.jpg", "logo.png", "page.html", "font.ttf", "scene.glb",
		"model.gguf", "a.safetensors", "jane.ma", "site.ai", "cover.psd",
	} {
		if ClipboardType(DefaultAutoTypes, name) {
			t.Errorf("%s offered", name)
		}
		if ClipboardType("*", name) {
			t.Errorf("%s offered, a lone star", name)
		}
		if ClipboardType("* ZIP", name) {
			t.Errorf("%s offered, a star and more", name)
		}
	}
	// Every type offered before the big-file types still is.
	for _, ty := range strings.Fields(earlierAutoTypes[2]) {
		name := "a." + strings.ReplaceAll(ty, "*", "1")
		if !ClipboardType(DefaultAutoTypes, name) {
			t.Errorf("%s not offered", name)
		}
	}
	// A type the user listed themselves; and one they took off.
	if !ClipboardType("ZIP XYZ", "a.xyz") {
		t.Error("the user's own type")
	}
	if ClipboardType("ZIP", "a.exe") {
		t.Error("a type taken off")
	}
}

func TestEachRefusalHasANameAndAReason(t *testing.T) {
	for _, d := range AllDeclined {
		if got, ok := ParseDeclined(string(d)); !ok || got != d {
			t.Errorf("ParseDeclined(%q) = %q, %v", d, got, ok)
		}
		if !strings.Contains(d.Reason(), "Options >") {
			t.Errorf("%s: %s", d, d.Reason())
		}
	}
	if _, ok := ParseDeclined("FileType"); ok {
		t.Error("an unknown code")
	}
	r := DefaultRules()
	r.Browsers = []BrowserCapture{{edge, false}}
	if _, off := r.CaptureOff(chrome); off {
		t.Error("chrome is captured")
	}
	if d, _ := r.CaptureOff(edge); d != BrowserOff {
		t.Errorf("edge: %q", d)
	}
	r.AdvancedIntegration = false
	if d, _ := r.CaptureOff(chrome); d != IntegrationOff {
		t.Errorf("integration off: %q", d)
	}
	if r.Captures(chrome) {
		t.Error("captures with integration off")
	}
}

func TestRulesAreReadFromTheDownloadList(t *testing.T) {
	dir := t.TempDir()
	if got := LoadRules(dir); got.AutoTypes != DefaultAutoTypes {
		t.Error("no list: the defaults")
	}
	list := filepath.Join(dir, "downloads.json")
	write := func(path, s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(list+".bak", `{"settings":{"browser.capture":"{\"autoTypes\":\"ISO\"}"}}`)
	write(list, `{"settings":{"browser.capture":"{\"autoTypes\":\"ZIP\",\"captureMinKB\":5}"}}`)
	if got := LoadRules(dir); got.AutoTypes != "ZIP" || got.CaptureMinKB != 5 {
		t.Errorf("from the list: %+v", got)
	}
	write(list, `{"settings":`) // cut short
	if got := LoadRules(dir); got.AutoTypes != "ISO" {
		t.Errorf("from the backup: %q", got.AutoTypes)
	}
}
