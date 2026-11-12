package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vaisah/internal/browser"
	"github.com/vaisah/internal/ipc"
	"github.com/vaisah/internal/platform"
	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Browser integration: the capture rules the native host applies at each
// hand-off, what the app does with the commands it sends, and registering
// the host with the browsers.

// BrowserIntegration is the integration's state for the UI.
type BrowserIntegration struct {
	// Host is the native host's path; "" when it is not beside the app.
	Host string `json:"host"`
	// Portable: a portable copy registers nothing.
	Portable bool `json:"portable"`
	// Seen are the browsers whose extension has reached the app.
	Seen []browser.Seen `json:"seen"`
}

// browserRulesMu keeps read-change-save of the rules whole.
var browserRulesMu sync.Mutex

// GetBrowserSettings returns the capture rules (Options → General and File
// types).
func (a *App) GetBrowserSettings() browser.CaptureRules {
	raw, _, _ := a.store.Settings().Get(context.Background(), browser.SettingsKey)
	return browser.ParseRules(raw)
}

// SaveBrowserSettings saves the capture rules; the native host reads them
// at its next hand-off.
func (a *App) SaveBrowserSettings(r browser.CaptureRules) (browser.CaptureRules, error) {
	browserRulesMu.Lock()
	defer browserRulesMu.Unlock()
	return a.saveBrowserRules(r)
}

func (a *App) saveBrowserRules(r browser.CaptureRules) (browser.CaptureRules, error) {
	r.AutoTypes = strings.Join(strings.Fields(r.AutoTypes), " ")
	r.ExcludedSites = strings.Join(strings.Fields(r.ExcludedSites), " ")
	r.VideoPanelSites = strings.Join(strings.Fields(r.VideoPanelSites), " ")
	var addresses []string
	for _, s := range r.ExcludedAddresses {
		if s = strings.TrimSpace(s); s != "" {
			addresses = append(addresses, s)
		}
	}
	r.ExcludedAddresses = addresses
	r.CaptureMinKB = max(0, r.CaptureMinKB)
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	return r, a.store.Settings().Set(context.Background(), browser.SettingsKey, string(raw))
}

// updateBrowserRules changes the saved rules.
func (a *App) updateBrowserRules(change func(*browser.CaptureRules)) error {
	browserRulesMu.Lock()
	defer browserRulesMu.Unlock()
	r := a.GetBrowserSettings()
	change(&r)
	_, err := a.saveBrowserRules(r)
	return err
}

// unsupportedFromBrowser says what the app can't yet do for a command
// from the browser. Such a command is refused at once, so the extension
// leaves the download to the browser instead of losing it.
func unsupportedFromBrowser(cmd ipc.Command) string {
	switch {
	case !cmd.Browser:
		return ""
	case cmd.Private:
		// Never kept in the list, as private browsing asks: not yet.
		return "a private window's download"
	case cmd.Post != "":
		return "a form's download (POST)"
	case len(cmd.Links) > 0:
		return "a list of links"
	}
	return ""
}

// browserSite handles the toolbar button's and the video button's site
// lists: leave a site's downloads to the browser, or keep the video
// button off it.
func (a *App) browserSite(ctx context.Context, cmd ipc.Command) {
	var err error
	var text string
	switch {
	case cmd.ExcludeSite != "":
		err = a.updateBrowserRules(func(r *browser.CaptureRules) {
			r.ExcludedSites = browser.AddSite(r.ExcludedSites, cmd.ExcludeSite)
		})
		text = "Downloads from " + cmd.ExcludeSite + " now stay with the browser"
	case cmd.PanelHideSite != "":
		err = a.updateBrowserRules(func(r *browser.CaptureRules) {
			r.VideoPanelSites = browser.AddSite(r.VideoPanelSites, cmd.PanelHideSite)
		})
		text = "The download button over videos stays off on " + cmd.PanelHideSite
	}
	if err != nil {
		a.notice(ctx, "error", "Could not save the browser settings: "+err.Error())
		return
	}
	wails.EventsEmit(ctx, "browser:settings", a.GetBrowserSettings())
	a.notice(ctx, "info", text)
}

// browserHostPath is the native host beside the app, if it is there.
func browserHostPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	name := "yellowflash-nmhost"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return p
}

// GetBrowserIntegration says whether the native host is there and which
// browsers' extension has reached the app.
func (a *App) GetBrowserIntegration() BrowserIntegration {
	_, portable := ipc.PortableDir()
	return BrowserIntegration{Host: browserHostPath(), Portable: portable, Seen: browser.SeenBrowsers(a.dataDir)}
}

// RegisterBrowserIntegration registers the native host with the browsers
// (this user's), returning what it says.
func (a *App) RegisterBrowserIntegration() (string, error) { return runBrowserHost("--register") }

// UnregisterBrowserIntegration removes the registration.
func (a *App) UnregisterBrowserIntegration() (string, error) { return runBrowserHost("--unregister") }

func runBrowserHost(arg string) (string, error) {
	host := browserHostPath()
	if host == "" {
		return "", errors.New("the browser helper (yellowflash-nmhost) is not beside Yellow Flash")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, host, arg)
	platform.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text != "" {
		err = errors.New(text)
	}
	return text, err
}

// registerBrowserHost registers the native host at startup, so a browser
// installed since finds it too (there is no installer step to do it). Not
// for a portable copy, nor with integration turned off, nor without the
// host beside the app.
func (a *App) registerBrowserHost(ctx context.Context) {
	if _, portable := ipc.PortableDir(); portable || browserHostPath() == "" || !a.GetBrowserSettings().AdvancedIntegration {
		return
	}
	if _, err := runBrowserHost("--register"); err != nil {
		wails.LogErrorf(ctx, "register the browser helper: %v", err)
	}
}
