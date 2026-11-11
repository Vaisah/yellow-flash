package main

// What the window's own dialogs need beyond the downloads themselves: a
// finished file's checksum, the download folder's disk space and its
// cleaner, the speed limiter, site logins, and the General options
// (launch at sign-in, the Download Complete window, browser capture).

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vaisah/internal/debrid"
	domain "github.com/vaisah/internal/domain/download"
	"github.com/vaisah/internal/infrastructure/filesystem"
	"github.com/vaisah/internal/platform"
)

/* ---------- checksum ---------- */

// FileChecksum is a finished download's hash in hex: algorithm "md5",
// "sha1", "sha256" or "sha512".
func (a *App) FileChecksum(id, algorithm string) (string, error) {
	d, err := a.GetDownload(id)
	if err != nil {
		return "", err
	}
	if d.Status != domain.Completed {
		return "", errors.New("the download hasn't finished")
	}
	var h hash.Hash
	switch strings.ToLower(algorithm) {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return "", fmt.Errorf("unknown algorithm %q", algorithm)
	}
	f, err := os.Open(filepath.Join(d.Destination, d.Filename))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

/* ---------- disk ---------- */

// DiskInfo is a folder's disk.
type DiskInfo struct {
	Dir   string `json:"dir"`
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
	// UsedPercent is 0–100 (-1: unknown).
	UsedPercent int `json:"usedPercent"`
}

// DiskSpace is the disk of dir ("": the download folder).
func (a *App) DiskSpace(dir string) DiskInfo {
	if strings.TrimSpace(dir) == "" {
		dir = a.GetSettings().DownloadDirectory
	}
	info := DiskInfo{Dir: dir, UsedPercent: -1}
	if v, ok := platform.VolumeOf(dir); ok {
		info.Free, info.Total = v.Free, v.Total
		if v.Total > 0 && v.Free <= v.Total {
			info.UsedPercent = int((v.Total - v.Free) * 100 / v.Total)
		}
	}
	return info
}

// CleanResult is what the disk cleaner removed.
type CleanResult struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// CleanDisk removes what Yellow Flash left behind and can't use: the
// partial files of downloads in its list that are finished or cancelled,
// and archive extractions' leftover staging folders. Nothing else: a .part
// file of another program's (a browser's) is never touched.
func (a *App) CleanDisk() (CleanResult, error) {
	if err := a.ready(); err != nil {
		return CleanResult{}, err
	}
	all, err := a.service.List(context.Background())
	if err != nil {
		return CleanResult{}, err
	}
	var res CleanResult
	removeFile := func(p string) {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() && os.Remove(p) == nil {
			res.Files++
			res.Bytes += fi.Size()
		}
	}
	removeDir := func(p string) {
		var size int64
		_ = filepath.WalkDir(p, func(_ string, e os.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				if fi, err := e.Info(); err == nil {
					size += fi.Size()
				}
			}
			return nil
		})
		if os.RemoveAll(p) == nil {
			res.Files++
			res.Bytes += size
		}
	}
	dirs := map[string]bool{a.GetSettings().DownloadDirectory: true}
	for _, d := range all {
		dirs[d.Destination] = true
		if (d.Status == domain.Completed || d.Status == domain.Cancelled) && strings.HasSuffix(d.TempPath, filesystem.TempSuffix) {
			removeFile(d.TempPath)
		}
	}
	for dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), ".yf-extract-") {
				removeDir(filepath.Join(dir, e.Name()))
			}
		}
	}
	return res, nil
}

/* ---------- speed limiter ---------- */

// SpeedLimit is the limiter: on, and its rate in KiB/s.
type SpeedLimit struct {
	Enabled bool  `json:"enabled"`
	KBps    int64 `json:"kbps"`
}

const keySpeedLast = "ui.speedLimitLast"

// GetSpeedLimit is the limiter as the connection settings hold it (off:
// the last rate used, for the dialog).
func (a *App) GetSpeedLimit() SpeedLimit {
	if n := a.GetNetworkSettings(); n.SpeedLimitKBps > 0 {
		return SpeedLimit{Enabled: true, KBps: n.SpeedLimitKBps}
	}
	last := SpeedLimit{KBps: 2048}
	if a.store != nil {
		if raw, ok, _ := a.store.Settings().Get(context.Background(), keySpeedLast); ok {
			_ = json.Unmarshal([]byte(raw), &last.KBps)
		}
	}
	return last
}

// SetSpeedLimit turns the limiter on at its rate, or off; it applies at once.
func (a *App) SetSpeedLimit(s SpeedLimit) (SpeedLimit, error) {
	if s.Enabled && s.KBps < 1 {
		return SpeedLimit{}, errors.New("the speed must be at least 1 KB/s")
	}
	n := a.GetNetworkSettings()
	n.SpeedLimitKBps = 0
	if s.Enabled {
		n.SpeedLimitKBps = s.KBps
	}
	if s.KBps > 0 {
		raw, _ := json.Marshal(s.KBps)
		_ = a.store.Settings().Set(context.Background(), keySpeedLast, string(raw))
	}
	if _, err := a.SaveNetworkSettings(n); err != nil {
		return SpeedLimit{}, err
	}
	return a.GetSpeedLimit(), nil
}

/* ---------- site logins ---------- */

// SiteLogin is a saved login for a site: its password is in the system's
// keychain, never shown or saved here.
type SiteLogin struct {
	Site string `json:"site"`
	User string `json:"user"`
}

const keySiteLogins = "site.logins"

func siteKey(site string) string { return "site/" + site }

// ListSiteLogins is the saved logins.
func (a *App) ListSiteLogins() []SiteLogin {
	l := []SiteLogin{}
	if a.store != nil {
		if raw, ok, _ := a.store.Settings().Get(context.Background(), keySiteLogins); ok {
			_ = json.Unmarshal([]byte(raw), &l)
		}
	}
	return l
}

func (a *App) saveSiteLogins(l []SiteLogin) error {
	raw, _ := json.Marshal(l)
	return a.store.Settings().Set(context.Background(), keySiteLogins, string(raw))
}

// cleanSite is a site as typed ("https://Host.com/x", "host.com") as its
// host name.
func cleanSite(site string) string {
	site = strings.TrimSpace(strings.ToLower(site))
	if u, err := url.Parse(site); err == nil && u.Host != "" {
		site = u.Hostname()
	}
	site, _, _ = strings.Cut(site, "/")
	return strings.TrimPrefix(site, "www.")
}

// SaveSiteLogin saves (or replaces) a site's login; downloads from it, and
// from its subdomains, use it.
func (a *App) SaveSiteLogin(site, user, password string) ([]SiteLogin, error) {
	site, user = cleanSite(site), strings.TrimSpace(user)
	if site == "" || strings.ContainsAny(site, " :@") || user == "" {
		return nil, errors.New("give the site (host.com) and the user name")
	}
	secrets, _ := a.debridSecrets()
	if err := secrets.Put(context.Background(), siteKey(site), map[string]string{"user": user, "password": password}); err != nil {
		return nil, fmt.Errorf("the password couldn't be kept: %w", err)
	}
	l := slices.DeleteFunc(a.ListSiteLogins(), func(s SiteLogin) bool { return s.Site == site })
	l = append(l, SiteLogin{Site: site, User: user})
	if err := a.saveSiteLogins(l); err != nil {
		return nil, err
	}
	return l, nil
}

// RemoveSiteLogin forgets a site's login and its password.
func (a *App) RemoveSiteLogin(site string) ([]SiteLogin, error) {
	site = cleanSite(site)
	secrets, _ := a.debridSecrets()
	_ = secrets.Delete(context.Background(), siteKey(site))
	l := slices.DeleteFunc(a.ListSiteLogins(), func(s SiteLogin) bool { return s.Site == site })
	return l, a.saveSiteLogins(l)
}

// basicAuth is an Authorization header's value for user and password.
func basicAuth(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// loginFor is the saved login's Authorization for rawURL's host ("": none).
func (a *App) loginFor(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	logins := a.ListSiteLogins()
	if len(logins) == 0 {
		return ""
	}
	secrets, _ := a.debridSecrets()
	for _, l := range logins {
		if debrid.HostMatches(u.Hostname(), []string{l.Site}) {
			if m, err := secrets.Get(context.Background(), siteKey(l.Site)); err == nil {
				return basicAuth(m["user"], m["password"])
			}
		}
	}
	return ""
}

/* ---------- General options ---------- */

const keyShowComplete = "ui.showComplete"

// Options is Options → General's and File Types' switches that aren't
// Settings'.
type Options struct {
	// LaunchOnStartup starts Yellow Flash when the user signs in.
	LaunchOnStartup bool `json:"launchOnStartup"`
	// ShowComplete opens the Download Complete window when one finishes.
	ShowComplete bool `json:"showComplete"`
	// BrowserCapture: the browsers' downloads are taken (the extension asks
	// the native host, which reads this).
	BrowserCapture bool `json:"browserCapture"`
	// FileTypes are the extensions captured from the browsers, space
	// separated.
	FileTypes string `json:"fileTypes"`
}

// GetOptions is Options → General and File Types.
func (a *App) GetOptions() Options {
	o := Options{ShowComplete: true, LaunchOnStartup: platform.Autostart(AppName)}
	if a.store != nil {
		if raw, ok, _ := a.store.Settings().Get(context.Background(), keyShowComplete); ok {
			o.ShowComplete = raw == "true"
		}
		r := a.GetBrowserSettings()
		o.FileTypes, o.BrowserCapture = r.AutoTypes, r.AdvancedIntegration
	}
	return o
}

// SaveOptions applies and saves Options → General and File Types.
func (a *App) SaveOptions(o Options) (Options, error) {
	var problems []string
	if o.LaunchOnStartup != platform.Autostart(AppName) {
		exe, err := os.Executable()
		if err == nil {
			err = platform.SetAutostart(AppName, exe, o.LaunchOnStartup)
		}
		if err != nil {
			problems = append(problems, "launch at sign-in: "+err.Error())
		}
	}
	if err := a.store.Settings().Set(context.Background(), keyShowComplete, fmt.Sprint(o.ShowComplete)); err != nil {
		return Options{}, err
	}
	rules := a.GetBrowserSettings()
	turnedOn := o.BrowserCapture && !rules.AdvancedIntegration
	rules.AutoTypes, rules.AdvancedIntegration = o.FileTypes, o.BrowserCapture
	if _, err := a.SaveBrowserSettings(rules); err != nil {
		problems = append(problems, "browser capture: "+err.Error())
	}
	// Turned on: the browsers are told where the native host is (when it
	// ships beside the app; a portable copy registers nothing).
	if bi := a.GetBrowserIntegration(); turnedOn && bi.Host != "" && !bi.Portable {
		if _, err := a.RegisterBrowserIntegration(); err != nil {
			problems = append(problems, "registering with the browsers: "+err.Error())
		}
	}
	if len(problems) > 0 {
		return a.GetOptions(), errors.New(strings.Join(problems, "; "))
	}
	return a.GetOptions(), nil
}
