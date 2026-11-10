// Ported from Mister Download Manager, crates/ipc/src/capture.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package browser holds what the browser integration shares between the
// app and the native messaging host: which downloads the extension may hand
// over (capture rules), the browsers and where each reads native messaging
// host manifests, and the extension's identity.
package browser

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// DefaultAutoTypes is Options → File types' default: IDM 6.42's, .torrent,
// today's installers, packages, archives and open media formats, and what
// people fetch by the gigabyte (AI models and datasets, 3D and CAD, camera
// RAW and design files, books and Office files, disk images, split
// archives). Never what a browser saves as part of everyday browsing
// (pages, scripts, plain text, web images) or installs itself (XPI, CRX).
const DefaultAutoTypes = "00* 3DM 3DS 3GP 3MF 7Z AAB AAC ABC ACE AFDESIGN AFPHOTO AI AIF AIFF APE APK APKM APKS APPIMAGE APPX APPXBUNDLE ARJ ARROW ARW ASF AU AVI AVRO AZW AZW3 BAK BAM BIN BLEND BZ BZ2 C4D CAB CAF CBR CBZ CCD CHD CKPT CPIO CR2 CR3 CRAM CSO DAE DCM DDS DEB DFF DIVX DJVU DMG DNG DOC DOCX DSF DWG DXF EPS EPUB EXE EXR F3D F4V FASTA FASTQ FB2 FBX FEATHER FIG FITS FLAC FLATPAK FQ GCODE GCZ GEM GGML GGUF GLB GLTF GPKG GRIB GRIB2 GZ GZIP H5 HDF5 HDR IGES IGS IMG INDD IPA ISO ISZ JAR JOBLIB JSONL KERAS KRA LAS LAZ LLAMAFILE LWO LZ LZ4 LZH LZMA M2T M2TS M2V M4A M4B M4V MA MAT MAX MB MBTILES MDF MDS MID MIDI MKA MKV MLMODEL MOBI MOV MP3 MP4 MPA MPE MPEG MPG MSI MSIX MSIXBUNDLE MSP MSU MTS MXF NC NDJSON NEF NEMO NII NPY NPZ NRG NSP NSZ NUPKG OBJ ODP ODS ODT OGG OGM OGV ONNX OPUS ORC ORF OTF OVA OVF PAK PARQUET PB PBF PDF PICKLE PK3 PK4 PKG PKL PLJ PLY POTX PPS PPSX PPT PPTX PSB PSD PT PTH QCOW QCOW2 QT R0* R1* R2* R3* R4* R5* R6* R7* R8* R9* RA RAF RAR RDS RM RMVB RPM RTF RUN RVZ RW2 SAFETENSORS SEA SF2 SFZ SHP SIT SITX SKETCH SKP SLDASM SLDPRT SNAP SQLITE SQLITE3 STEP STL STP TAR TBZ TBZ2 TFLITE TGA TGZ TIF TIFF TLZ TORRENT TTA TTC TTF TXZ TZST UNITYPACKAGE USD USDA USDC USDZ VDB VDI VHD VHDX VMDK VOB VPK VSIX WAV WBFS WEBM WHL WIM WMA WMV WRL WTV WUA WV X3D XAPK XCF XCI XLS XLSB XLSM XLSX XZ Z Z0* ZIP ZIPX ZPR ZST ZTL"

// earlierAutoTypes are the lists earlier versions started with; a saved
// list equal to one of them was never edited and becomes today's.
var earlierAutoTypes = [3]string{
	"3GP 7Z AAC ACE AIF APK ARJ ASF AVI BIN BZ2 EXE GZ GZIP IMG ISO LZH M4A M4V MKV MOV MP3 MP4 MPA MPE MPEG MPG MSI MSU OGG OGV PDF PLJ PPS PPT QT R0* R1* RA RAR RM RMVB SEA SIT SITX TAR TIF TIFF WAV WMA WMV Z ZIP",
	"3GP 7Z AAC ACE AIF APK ARJ ASF AVI BIN BZ2 EXE GZ GZIP IMG ISO LZH M4A M4V MKV MOV MP3 MP4 MPA MPE MPEG MPG MSI MSU OGG OGV PDF PLJ PPS PPT QT R0* R1* RA RAR RM RMVB SEA SIT SITX TAR TIF TIFF TORRENT WAV WMA WMV Z ZIP",
	"3GP 7Z AAC ACE AIF APK APPIMAGE APPX APPXBUNDLE ARJ ASF AVI BIN BZ2 CAB DEB DMG EXE FLAC GZ GZIP IMG ISO JAR LZH M4A M4V MKV MOV MP3 MP4 MPA MPE MPEG MPG MSI MSIX MSU OGG OGV OPUS PDF PKG PLJ PPS PPT QT R0* R1* RA RAR RM RMVB RPM SEA SIT SITX TAR TGZ TIF TIFF TORRENT TXZ WAV WEBM WMA WMV XZ Z ZIP ZST",
}

// DefaultExcludedSites are sites whose downloads stay with the browser.
const DefaultExcludedSites = "*.update.microsoft.com download.windowsupdate.com *.download.windowsupdate.com siteseal.thawte.com ecom.cimetz.com *.voice2page.com"

// UpgradedTypes returns today's default list when saved is an earlier
// version's default, untouched (same types, any order or case).
func UpgradedTypes(saved string) (string, bool) {
	set := func(s string) []string {
		var v []string
		for _, f := range strings.Fields(s) {
			v = append(v, strings.ToUpper(f))
		}
		slices.Sort(v)
		return slices.Compact(v)
	}
	got := set(saved)
	for _, old := range earlierAutoTypes {
		if slices.Equal(set(old), got) {
			return DefaultAutoTypes, true
		}
	}
	return "", false
}

// BrowserCapture says whether downloads from a browser (by the names
// Options lists) are taken.
type BrowserCapture struct {
	Name    string `json:"name"`
	Capture bool   `json:"capture"`
}

// CaptureRules are the capture settings (Options → General and File
// types), saved in downloads.json under SettingsKey.
type CaptureRules struct {
	AdvancedIntegration bool             `json:"advancedIntegration"`
	Browsers            []BrowserCapture `json:"browsers"`
	AutoTypes           string           `json:"autoTypes"`
	ExcludedSites       string           `json:"excludedSites"`
	ExcludedAddresses   []string         `json:"excludedAddresses"`
	// VideoPanel is the download button over videos in the browsers;
	// VideoPanelSites are the sites it stays off.
	VideoPanel      bool   `json:"videoPanel"`
	VideoPanelSites string `json:"videoPanelSites"`
	// CaptureOpened also takes listed types the browser would open itself
	// (a PDF in its viewer), not only what it downloads.
	CaptureOpened bool `json:"captureOpened"`
	// CaptureMinKB leaves files smaller than this (KB) to the browser; 0
	// takes any size. Only when the browser knows the size.
	CaptureMinKB int64 `json:"captureMinKB"`
}

// SettingsKey is where the app saves the rules (a JSON value in
// downloads.json's settings).
const SettingsKey = "browser.capture"

// DefaultRules are the rules before the user changes any.
func DefaultRules() CaptureRules {
	return CaptureRules{AdvancedIntegration: true, AutoTypes: DefaultAutoTypes, ExcludedSites: DefaultExcludedSites,
		VideoPanel: true, CaptureOpened: true}
}

// ParseRules reads saved rules over the defaults; an earlier default
// File types list becomes today's.
func ParseRules(raw string) CaptureRules {
	r := DefaultRules()
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &r)
	}
	return r.Upgraded()
}

// LoadRules reads the rules as the app last saved them in dataDir's
// downloads.json (its newest backup when the list does not read); the
// defaults when there are none. The native host reads them at every
// hand-off, so a change applies at once.
func LoadRules(dataDir string) CaptureRules {
	path := filepath.Join(dataDir, "downloads.json")
	for _, p := range []string{path, path + ".bak"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var list struct {
			Settings map[string]string `json:"settings"`
		}
		if json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &list) == nil {
			return ParseRules(list.Settings[SettingsKey])
		}
	}
	return DefaultRules()
}

// Upgraded returns the rules with an untouched earlier default type list
// replaced by today's.
func (r CaptureRules) Upgraded() CaptureRules {
	if t, ok := UpgradedTypes(r.AutoTypes); ok {
		r.AutoTypes = t
	}
	return r
}

// Declined says why a download is left to the browser.
type Declined string

// Reasons, by their names between the native host and the extension.
const (
	IntegrationOff Declined = "integration-off"
	BrowserOff     Declined = "browser"
	FileType       Declined = "file-type"
	Site           Declined = "site"
	Address        Declined = "address"
	Small          Declined = "small"
)

// AllDeclined lists every reason.
var AllDeclined = []Declined{IntegrationOff, BrowserOff, FileType, Site, Address, Small}

// ParseDeclined returns the reason named code, as the extension sends it.
func ParseDeclined(code string) (Declined, bool) {
	for _, d := range AllDeclined {
		if string(d) == code {
			return d, true
		}
	}
	return "", false
}

// Reason names the setting that decides, for the log.
func (d Declined) Reason() string {
	switch d {
	case IntegrationOff:
		return "Options > General > Use advanced browser integration is off"
	case BrowserOff:
		return "the browser is unticked in Options > General > Capture downloads from the following browsers"
	case FileType:
		return "its type isn't in Options > File types"
	case Site:
		return "its site is in Options > File types > Don't start downloading automatically from the following sites"
	case Address:
		return "its address is in Options > File types > Edit list (addresses left to the browser)"
	case Small:
		return "it is smaller than Options > File types > Don't take files smaller than"
	}
	return string(d)
}

// TooSmall reports a file of size bytes (the browser's figure; 0 or less:
// unknown) too small to take.
func (r CaptureRules) TooSmall(size int64) bool {
	return size > 0 && size < r.CaptureMinKB*1024
}

// AutoType reports whether name's extension is in File types.
func (r CaptureRules) AutoType(name string) bool { return TypeListed(r.AutoTypes, name) }

// CaptureOff says why no download from browser is taken, if so.
func (r CaptureRules) CaptureOff(browser string) (Declined, bool) {
	if !r.AdvancedIntegration {
		return IntegrationOff, true
	}
	for _, b := range r.Browsers {
		if b.Name == browser && !b.Capture {
			return BrowserOff, true
		}
	}
	return "", false
}

// Captures reports whether downloads from browser are taken at all.
func (r CaptureRules) Captures(browser string) bool {
	_, off := r.CaptureOff(browser)
	return !off
}

// Accepts says whether browser may hand url (saved as fileName) over, and
// if not, why.
func (r CaptureRules) Accepts(url, fileName, browser string) (Declined, bool) {
	if d, off := r.CaptureOff(browser); off {
		return d, false
	}
	if !r.AutoType(fileName) {
		return FileType, false
	}
	host := HostOf(url)
	for _, p := range strings.Fields(r.ExcludedSites) {
		if HostMatches(host, p) {
			return Site, false
		}
	}
	for _, p := range r.ExcludedAddresses {
		if AddressMatches(p, url) {
			return Address, false
		}
	}
	return "", true
}

// PanelOn reports whether the download button may show over videos on page
// in browser.
func (r CaptureRules) PanelOn(page, browser string) bool {
	return r.Captures(browser) && r.VideoPanel && !r.PanelHiddenOn(page)
}

// PanelHiddenOn reports a page on a site the button stays off.
func (r CaptureRules) PanelHiddenOn(page string) bool {
	host := HostOf(page)
	for _, p := range strings.Fields(r.VideoPanelSites) {
		if HostMatches(host, p) {
			return true
		}
	}
	return false
}

// AddSite returns sites (space-separated) with host added, unless a
// pattern covers it or it is not one host name.
func AddSite(sites, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	one := host != ""
	for _, c := range host {
		one = one && (unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune(".-_:[]", c))
	}
	if !one {
		return sites
	}
	for _, p := range strings.Fields(sites) {
		if HostMatches(host, p) {
			return sites
		}
	}
	out := strings.TrimSpace(sites)
	if out != "" {
		out += " "
	}
	return out + host
}

// TypeListed reports whether name's extension appears in types ("EXE ZIP
// R0*").
func TypeListed(types, name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	ext := strings.ToUpper(name[i+1:])
	for _, t := range strings.Fields(types) {
		t = strings.ToUpper(t)
		if prefix, ok := strings.CutSuffix(t, "*"); ok {
			if strings.HasPrefix(ext, prefix) {
				return true
			}
		} else if ext == t {
			return true
		}
	}
	return false
}

// ClipboardType reports whether a copied link named name is a file to
// offer: the clipboard keeps to the list as it stood before the big-file
// types were added, plus what the user typed in; a lone "*" does not count.
func ClipboardType(types, name string) bool {
	var own []string
	for _, t := range strings.Fields(types) {
		if t != "*" {
			own = append(own, t)
		}
	}
	browserOnly := TypeListed(DefaultAutoTypes, name) && !TypeListed(earlierAutoTypes[2], name)
	return !browserOnly && TypeListed(strings.Join(own, " "), name)
}

// BrowserNamed returns the browser by the names Options lists, from its
// User-Agent and, for those that say who they are only there, its brands
// (navigator.userAgentData's; for Firefox's forks runtime.getBrowserInfo's
// name).
func BrowserNamed(userAgent string, brands []string) string {
	has := func(b string) bool { return slices.Contains(brands, b) }
	switch {
	case has("Zen"):
		return "Zen Browser"
	case has("LibreWolf"):
		return "LibreWolf"
	case has("Floorp"):
		return "Floorp"
	case has("Waterfox"):
		return "Waterfox"
	case has("Brave"):
		return "Brave Browser"
	case has("Opera") || has("Opera GX"):
		return "Opera"
	case has("Microsoft Edge"):
		return "Microsoft Edge"
	}
	return BrowserOf(userAgent)
}

// BrowserOf returns the browser a User-Agent belongs to.
func BrowserOf(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"):
		return "Microsoft Edge"
	case strings.Contains(ua, "OPR/") || strings.Contains(ua, "Opera"):
		return "Opera"
	case strings.Contains(ua, "Firefox/"):
		return "Mozilla Firefox"
	case strings.Contains(ua, "Chrome/"):
		return "Google Chrome"
	case strings.Contains(ua, "Safari/"):
		return "Apple Safari"
	}
	return ""
}

// HostOf returns the host of an http(s) address, lower case.
func HostOf(url string) string {
	rest := url
	if _, r, ok := strings.Cut(url, "://"); ok {
		rest = r
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	if strings.HasPrefix(rest, "[") {
		if end := strings.IndexByte(rest, ']'); end > 0 {
			rest = rest[1:end]
		}
	} else if i := strings.IndexByte(rest, ':'); i >= 0 {
		rest = rest[:i]
	}
	return strings.ToLower(rest)
}

// HostMatches reports host is pattern (any case) or, for *.example.com,
// one of its subdomains or the domain itself.
func HostMatches(host, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if _, r, ok := strings.Cut(pattern, "://"); ok {
		pattern = r
	}
	pattern, _, _ = strings.Cut(pattern, "/")
	if base, ok := strings.CutPrefix(pattern, "*."); ok {
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return pattern != "" && host == pattern
}

// AddressMatches matches an address pattern against url, any case: with a
// scheme it is the whole address; without one, the address after its
// scheme://.
func AddressMatches(pattern, url string) bool {
	pattern, url = strings.ToLower(strings.TrimSpace(pattern)), strings.ToLower(url)
	if pattern == "" {
		return false
	}
	text := url
	if !strings.Contains(pattern, "://") {
		if _, r, ok := strings.Cut(url, "://"); ok {
			text = r
		}
	}
	return wildcard(pattern, text)
}

// wildcard: '*' matches any run of characters.
func wildcard(pattern, text string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == text
	}
	rest := text
	for i, part := range parts {
		switch {
		case i == 0:
			r, ok := strings.CutPrefix(rest, part)
			if !ok {
				return false
			}
			rest = r
		case i == len(parts)-1:
			return strings.HasSuffix(rest, part)
		default:
			at := strings.Index(rest, part)
			if at < 0 {
				return false
			}
			rest = rest[at+len(part):]
		}
	}
	return true
}
