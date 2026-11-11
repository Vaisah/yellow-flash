package browser

import "strings"

// Installed is a browser on this computer the extension can run in.
type Installed struct {
	// Name is its name in Browsers ("Google Chrome").
	Name string
	// Program starts it; given an address, it opens that address.
	Program string
	Family  Family
}

// InstalledBrowsers lists the browsers on this computer the extension can
// run in, one per name, Chromium's first as in Browsers.
func InstalledBrowsers() []Installed {
	found := installedBrowsers()
	var out []Installed
	for _, b := range Browsers {
		for _, f := range found {
			if f.Name == b.Name {
				f.Family = b.Family
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// SameBrowser reports whether a name the extension reported (BrowserNamed:
// "Brave Browser") is the browser named name in Browsers ("Brave").
func SameBrowser(reported, name string) bool {
	norm := func(s string) string {
		return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), " browser")
	}
	return reported != "" && norm(reported) == norm(name)
}

// extensionsPages are the pages where each Chromium browser lists its
// extensions, with Developer mode and Load unpacked.
var extensionsPages = map[string]string{
	"Microsoft Edge": "edge://extensions/",
	"Brave":          "brave://extensions/",
	"Vivaldi":        "vivaldi://extensions/",
	"Opera":          "opera://extensions/",
}

// ExtensionsPage is where the browser named lists its extensions:
// chrome://extensions/ for Chrome and the browsers that keep its name for
// the page, about:addons for Firefox's family.
func ExtensionsPage(name string) string {
	if b, ok := Named(name); ok && b.Family == Gecko {
		return "about:addons"
	}
	if p, ok := extensionsPages[name]; ok {
		return p
	}
	return "chrome://extensions/"
}

// EdgeAddonsUpdateURL is Edge's update service: an external-install entry
// naming it makes Edge offer the Edge Add-ons extension at its next start.
const EdgeAddonsUpdateURL = "https://edge.microsoft.com/extensionwebstorebase/v1/crx"

// EdgeIDValid returns the extension's ID in Microsoft Edge Add-ons, once it
// is listed there.
func EdgeIDValid() (string, bool) {
	return OtherStoreIDs[0], ValidID(OtherStoreIDs[0])
}

// StorePageFor is the page that adds the extension to the browser named,
// once it is listed: Edge Add-ons for Edge when listed there, the Chrome Web
// Store for the other Chromium browsers (Edge, Brave, Vivaldi and Opera all
// install from it), addons.mozilla.org for Firefox's family.
func StorePageFor(name string) (string, bool) {
	b, ok := Named(name)
	if !ok {
		return "", false
	}
	if b.Family == Gecko {
		return FirefoxPage, FirefoxPage != ""
	}
	if name == "Microsoft Edge" {
		if id, ok := EdgeIDValid(); ok {
			return "https://microsoftedge.microsoft.com/addons/detail/" + id, true
		}
	}
	return StorePage()
}
