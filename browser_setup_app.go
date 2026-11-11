package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Setting up the browsers: which are installed, which already have the
// extension, and adding it to one. Browsers let no program add an
// extension by itself: from a store the person confirms it in the browser;
// until the extension is listed, they load it from the folder Yellow Flash
// installs beside itself.

// browserSetupDoneKey marks the setup window as seen (closed with Done or
// Not now), so it isn't offered at every start.
const browserSetupDoneKey = "ui.browserSetupDone"

// extensionFolder is the installed extension's folder name beside the app.
const extensionFolder = "browser-extension"

// BrowserTarget is one installed browser in the setup window.
type BrowserTarget struct {
	Name string `json:"name"`
	// Family is "chromium" or "firefox".
	Family string `json:"family"`
	// Connected: its extension has reached Yellow Flash.
	Connected bool   `json:"connected"`
	Version   string `json:"version"`
	// Store is the page that adds the extension, once it is listed.
	Store string `json:"store"`
	// Unpacked: it can load the extension from ExtensionDir (Chromium).
	Unpacked bool `json:"unpacked"`
}

// BrowserSetup is the setup window's state.
type BrowserSetup struct {
	Browsers []BrowserTarget `json:"browsers"`
	// ExtensionDir is the extension's folder, to load unpacked; "" if absent.
	ExtensionDir string `json:"extensionDir"`
	// HostReady: the browser helper is beside the app and registers.
	HostReady bool `json:"hostReady"`
	Portable  bool `json:"portable"`
	// Offer: open the window at start (not set up, not dismissed).
	Offer bool `json:"offer"`
}

// extensionDir is the extension beside the app (an installed copy), or the
// repository's while developing (build/bin → extension).
func extensionDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, p := range []string{filepath.Join(dir, extensionFolder), filepath.Join(dir, "..", "..", "extension")} {
		if fi, err := os.Stat(filepath.Join(p, "manifest.json")); err == nil && fi.Mode().IsRegular() {
			return filepath.Clean(p)
		}
	}
	return ""
}

// GetBrowserSetup lists the installed browsers and whether each has the
// extension.
func (a *App) GetBrowserSetup() BrowserSetup {
	_, portable := ipc.PortableDir()
	seen := browser.SeenBrowsers(a.dataDir)
	s := BrowserSetup{
		Browsers:     []BrowserTarget{},
		ExtensionDir: extensionDir(),
		HostReady:    !portable && browserHostPath() != "",
		Portable:     portable,
	}
	anyConnected := false
	for _, b := range browser.InstalledBrowsers() {
		t := BrowserTarget{Name: b.Name, Family: "chromium"}
		if b.Family == browser.Gecko {
			t.Family = "firefox"
		}
		for _, x := range seen {
			if browser.SameBrowser(x.Browser, b.Name) {
				t.Connected, t.Version = true, x.Version
			}
		}
		t.Store, _ = browser.StorePageFor(b.Name)
		t.Unpacked = b.Family == browser.Chromium && s.ExtensionDir != ""
		anyConnected = anyConnected || t.Connected
		s.Browsers = append(s.Browsers, t)
	}
	done, _, _ := a.store.Settings().Get(context.Background(), browserSetupDoneKey)
	s.Offer = s.HostReady && done != "1" && !anyConnected && len(s.Browsers) > 0
	return s
}

// AddExtensionTo opens the browser named where its extension is added: the
// store's page once listed ("store"), else its extensions page, with the
// extension's folder copied to the clipboard for Load unpacked
// ("unpacked"). Browser capture is turned on and the helper registered
// first, so the extension finds Yellow Flash as soon as it is added.
func (a *App) AddExtensionTo(name string) (string, error) {
	var target *browser.Installed
	for _, b := range browser.InstalledBrowsers() {
		if b.Name == name {
			target = &b
			break
		}
	}
	if target == nil {
		return "", errors.New(name + " is not installed")
	}
	if _, portable := ipc.PortableDir(); portable {
		return "", errors.New("a portable copy doesn't connect to browsers: install Yellow Flash to use the extension")
	}
	if browserHostPath() == "" {
		return "", errors.New("the browser helper (yellowflash-nmhost) is missing: reinstall Yellow Flash")
	}
	if err := a.updateBrowserRules(func(r *browser.CaptureRules) { r.AdvancedIntegration = true }); err != nil {
		return "", err
	}
	if _, err := a.RegisterBrowserIntegration(); err != nil {
		return "", err
	}

	mode, address := "store", ""
	if page, ok := browser.StorePageFor(name); ok {
		address = page
	} else if dir := extensionDir(); target.Family == browser.Chromium && dir != "" {
		mode, address = "unpacked", browser.ExtensionsPage(name)
		if a.ctx != nil {
			_ = wails.ClipboardSetText(a.ctx, dir)
		}
	} else {
		return "", errors.New("the extension isn't available for " + name + " yet")
	}
	cmd := exec.Command(target.Program, address)
	if err := cmd.Start(); err != nil {
		return "", errors.New("could not open " + name + ": " + err.Error())
	}
	go func() { _ = cmd.Wait() }()
	return mode, nil
}

// FinishBrowserSetup stops offering the setup window at start (it stays in
// Options).
func (a *App) FinishBrowserSetup() error {
	return a.store.Settings().Set(context.Background(), browserSetupDoneKey, "1")
}
