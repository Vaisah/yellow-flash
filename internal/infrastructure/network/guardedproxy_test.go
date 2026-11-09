package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// A fake proxy on this computer: it answers every request it gets with the
// address asked for, or redirects to redirect when set.
func fakeProxy(t *testing.T, redirect string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if redirect != "" && !strings.Contains(r.URL.String(), "redirected") {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "proxied "+r.URL.String())
	}))
	t.Cleanup(p.Close)
	return p, &hits
}

// Untrusted addresses keep the user's proxy (on this computer here): a
// public target goes through it; a loopback, private or localhost target is
// refused before the proxy hears of it.
func TestUntrustedAddressesKeepTheProxyButNotPrivateTargets(t *testing.T) {
	proxy, hits := fakeProxy(t, "")
	c := NewClient(Options{Proxy: proxy.URL})
	res, err := c.Get("http://1.1.1.1/a.zip")
	if err != nil {
		t.Fatalf("a public target through the proxy: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "proxied http://1.1.1.1/a.zip" {
		t.Errorf("answer %q", body)
	}
	for _, target := range []string{"http://127.0.0.1:9/x", "http://10.0.0.5/x", "http://[::1]/x",
		"http://localhost:9/x", "http://router.localhost/x", "http://169.254.169.254/latest/meta-data"} {
		before := hits.Load()
		if _, err := c.Get(target); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: %v", target, err)
		}
		if hits.Load() != before {
			t.Errorf("%s reached the proxy", target)
		}
	}
}

// A redirect is a new request: its target is checked too.
func TestARedirectThroughTheProxyIsCheckedToo(t *testing.T) {
	proxy, _ := fakeProxy(t, "http://192.168.1.1/redirected")
	c := NewClient(Options{Proxy: proxy.URL})
	if _, err := c.Get("http://1.1.1.1/a.zip"); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("redirected to a private address: %v", err)
	}
}

// Where a PAC script says DIRECT, the dial policy applies as without a proxy.
func TestAPACsDirectAnswerKeepsTheDialPolicy(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer local.Close()
	c := NewClient(Options{PACScript: `function FindProxyForURL(u, h) { return "DIRECT"; }`})
	if _, err := c.Get(local.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("a local server, direct: %v", err)
	}
	if _, err := NewClient(Options{Proxy: "direct"}).Get(local.URL); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("a local server, no proxy: %v", err)
	}
}

func TestProxiedTargets(t *testing.T) {
	ctx := context.Background()
	socks, _ := url.Parse("socks5h://127.0.0.1:1080")
	http1, _ := url.Parse("http://127.0.0.1:3128")
	// socks5h resolves names at the proxy: none looked up here.
	if err := checkProxiedTarget(ctx, "intranet.invalid", socks, nil); err != nil {
		t.Errorf("socks5h name: %v", err)
	}
	for _, p := range []*url.URL{socks, http1} {
		if err := checkProxiedTarget(ctx, "192.168.0.1", p, nil); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s, a private literal: %v", p.Scheme, err)
		}
		if err := checkProxiedTarget(ctx, "LOCALHOST.", p, nil); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s, localhost: %v", p.Scheme, err)
		}
	}
	// A name that doesn't resolve here is the proxy's to resolve.
	if err := checkProxiedTarget(ctx, "no-such-host.invalid", http1, nil); err != nil {
		t.Errorf("an unresolvable name: %v", err)
	}
	if err := checkProxiedTarget(ctx, "", http1, nil); err == nil {
		t.Error("no host")
	}
}
