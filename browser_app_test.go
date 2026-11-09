package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
	"github.com/vaisah/internal/ipc"
)

// What the app saves is what the native host reads at its next hand-off.
func TestBrowserSettingsReachTheHost(t *testing.T) {
	dir := t.TempDir()
	store, err := jsonstore.Open(filepath.Join(dir, "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &App{store: store, dataDir: dir}
	if got := a.GetBrowserSettings(); !reflect.DeepEqual(got, browser.DefaultRules()) {
		t.Fatalf("defaults %+v", got)
	}
	r := browser.DefaultRules()
	r.AutoTypes = "  zip   ISO "
	r.ExcludedSites = "a.test\n b.test"
	r.ExcludedAddresses = []string{" https://x.test/private/* ", "  "}
	r.CaptureMinKB = -5
	r.Browsers = []browser.BrowserCapture{{Name: "Google Chrome", Capture: false}}
	saved, err := a.SaveBrowserSettings(r)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AutoTypes != "zip ISO" || saved.ExcludedSites != "a.test b.test" || saved.CaptureMinKB != 0 ||
		!reflect.DeepEqual(saved.ExcludedAddresses, []string{"https://x.test/private/*"}) {
		t.Errorf("saved %+v", saved)
	}
	if err := a.updateBrowserRules(func(r *browser.CaptureRules) {
		r.ExcludedSites = browser.AddSite(r.ExcludedSites, "C.test")
		r.VideoPanelSites = browser.AddSite(r.VideoPanelSites, "tube.test")
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	host := browser.LoadRules(dir)
	if host.ExcludedSites != "a.test b.test c.test" || host.VideoPanelSites != "tube.test" || host.Captures("Google Chrome") {
		t.Errorf("the host reads %+v", host)
	}
	if !reflect.DeepEqual(host, a.GetBrowserSettings()) {
		t.Errorf("the host reads %+v, the app %+v", host, a.GetBrowserSettings())
	}
}

// What can't be done yet is refused while the browser still holds the
// download; everything else waits for the UI.
func TestBrowserCommandsTheAppCannotDoAreRefused(t *testing.T) {
	a := &App{commands: make(chan ipc.Incoming, 8)}
	for _, cmd := range []ipc.Command{
		{Browser: true, URL: "https://x.test/a.zip", Private: true},
		{Browser: true, URL: "https://x.test/get", Post: "a=1"},
		{Browser: true, VideoPage: "https://tube.test/watch"},
		{Browser: true, Links: []string{"https://x.test/a.zip"}},
	} {
		if unsupportedFromBrowser(cmd) == "" || a.handleCommand(ipc.Incoming{Cmd: cmd}) {
			t.Errorf("taken: %+v", cmd)
		}
	}
	for _, cmd := range []ipc.Command{
		{Browser: true, URL: "https://x.test/a.zip", Cookies: "a=1"},
		{Browser: true, ExcludeSite: "x.test"},
		{URL: "https://x.test/get", Post: "a=1"}, // not from the browser: the command line's
	} {
		if !a.handleCommand(ipc.Incoming{Cmd: cmd}) {
			t.Errorf("refused: %+v", cmd)
		}
	}
}
