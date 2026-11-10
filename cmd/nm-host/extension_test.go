package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
)

// The extension in extension/ and this host agree on who they are: the
// manifest's key gives the ID the host serves, Firefox's add-on ID, the
// host's name, and the version they ship with.
func TestTheShippedExtensionIsTheOneServed(t *testing.T) {
	root := filepath.Join("..", "..", "extension")
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var chromium struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(read("manifest.json"), &chromium); err != nil {
		t.Fatal(err)
	}
	if id, ok := browser.IDFromKey(chromium.Key); !ok || id != browser.UnpackedID {
		t.Errorf("manifest.json's key gives %q, the host serves %q", id, browser.UnpackedID)
	}
	var firefox struct {
		Version  string `json:"version"`
		Settings struct {
			Gecko struct {
				ID string `json:"id"`
			} `json:"gecko"`
		} `json:"browser_specific_settings"`
	}
	if err := json.Unmarshal(read("manifest.firefox.json"), &firefox); err != nil {
		t.Fatal(err)
	}
	if firefox.Settings.Gecko.ID != browser.FirefoxID {
		t.Errorf("Firefox's add-on ID %q, the host serves %q", firefox.Settings.Gecko.ID, browser.FirefoxID)
	}
	if chromium.Version != ipc.AppVersion || firefox.Version != ipc.AppVersion {
		t.Errorf("versions %s and %s, the app's %s", chromium.Version, firefox.Version, ipc.AppVersion)
	}
	m := regexp.MustCompile(`const HOST = "([^"]+)"`).FindSubmatch(read("background.js"))
	if m == nil || string(m[1]) != browser.HostName {
		t.Errorf("background.js connects to %q, the host is %q", m, browser.HostName)
	}
}
