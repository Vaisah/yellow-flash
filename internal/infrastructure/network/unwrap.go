package network

import (
	"net/url"
	"strings"
)

// wrapperParams lists, per host and path, the query parameter that carries
// the real target of well-known redirect/preview pages.
var wrapperParams = []struct {
	host, path string
	params     []string
}{
	{"google.", "/imgres", []string{"imgurl"}},
	{"google.", "/url", []string{"url", "q"}},
	{"l.facebook.com", "/l.php", []string{"u"}},
	{"lm.facebook.com", "/l.php", []string{"u"}},
	{"youtube.com", "/redirect", []string{"q"}},
	{"l.instagram.com", "/", []string{"u"}},
}

// UnwrapLink returns the direct target hidden inside known redirect or
// preview links (Google image results, Google/Facebook/YouTube outbound
// redirects). Anything else, or a target that is not a safe http(s) URL,
// is returned unchanged. Only one level is unwrapped.
func UnwrapLink(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	for _, w := range wrapperParams {
		if !hostMatches(host, w.host) || u.Path != w.path {
			continue
		}
		q := u.Query()
		for _, p := range w.params {
			if target := q.Get(p); target != "" {
				if _, err := ValidateURL(target); err == nil {
					return target
				}
			}
		}
	}
	return raw
}

// hostMatches matches exact hosts, and "google."-style prefixes covering
// every country domain (google.com, google.co.uk, ...).
func hostMatches(host, pattern string) bool {
	if strings.HasSuffix(pattern, ".") {
		return strings.HasPrefix(host, pattern)
	}
	return host == pattern
}
