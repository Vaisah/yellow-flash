package browser

import "testing"

func TestBrowserSetupHelpers(t *testing.T) {
	// The names the extension reports (BrowserNamed) against the table's.
	for _, c := range []struct {
		reported, name string
		same           bool
	}{
		{"Brave Browser", "Brave", true},
		{"Zen Browser", "Zen Browser", true},
		{"Google Chrome", "Google Chrome", true},
		{"Microsoft Edge", "Google Chrome", false},
		{"", "Brave", false},
	} {
		if got := SameBrowser(c.reported, c.name); got != c.same {
			t.Errorf("SameBrowser(%q, %q) = %v", c.reported, c.name, got)
		}
	}

	for name, page := range map[string]string{
		"Google Chrome":   "chrome://extensions/",
		"Helium":          "chrome://extensions/",
		"Microsoft Edge":  "edge://extensions/",
		"Brave":           "brave://extensions/",
		"Mozilla Firefox": "about:addons",
	} {
		if got := ExtensionsPage(name); got != page {
			t.Errorf("ExtensionsPage(%q) = %q, want %q", name, got, page)
		}
	}

	// Until the extension is listed, no browser is sent to a store; a
	// placeholder ID never becomes an address.
	if _, listed := StoreIDValid(); !listed {
		for _, name := range []string{"Google Chrome", "Microsoft Edge", "Brave"} {
			if p, ok := StorePageFor(name); ok {
				t.Errorf("%s has a store page %q before the extension is listed", name, p)
			}
		}
	}
	if FirefoxPage == "" {
		if _, ok := StorePageFor("Mozilla Firefox"); ok {
			t.Error("Firefox has a store page before the add-on is listed")
		}
	}
	if _, ok := StorePageFor("Netscape"); ok {
		t.Error("an unknown browser has a store page")
	}

	// Whatever is installed here is named from the table, with a program.
	for _, b := range InstalledBrowsers() {
		if _, ok := Named(b.Name); !ok || b.Program == "" {
			t.Errorf("installed browser %+v", b)
		}
	}
}
