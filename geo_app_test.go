package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAServerIsLocated(t *testing.T) {
	us := locate("8.8.8.8", 20, false, "")
	if us.Place != "country" || us.Country != "US" || us.Flag == nil || us.Reach != "near" || us.Attribution == "" {
		t.Fatalf("%+v", us)
	}
	for ip, place := range map[string]string{"127.0.0.1": "this-computer", "::1": "this-computer",
		"192.168.1.5": "local-network", "fe80::1": "local-network", "": "unknown", "nonsense": "unknown"} {
		if got := locate(ip, 0, false, ""); got.Place != place || got.Flag != nil {
			t.Errorf("%q: %+v", ip, got)
		}
	}
	if reach(100) != "medium" || reach(400) != "far" || reach(0) != "" {
		t.Error(reach(100), reach(400))
	}
	if !strings.HasPrefix((&App{}).FlagsPicture(), "data:image/png;base64,iVBOR") {
		t.Error("the flags' picture")
	}
}

// A download keeps where its answers came from: this computer, here, and
// the CDN its headers named.
func TestADownloadKeepsItsServer(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 300_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8a1-FRA")
		http.ServeContent(w, r, "f.bin", time.Unix(1_700_000_000, 0), bytes.NewReader(body))
	}))
	defer srv.Close()
	a, _, dir := extractApp(t)
	d := fetch(t, a, srv.URL+"/f.bin", dir)
	if d.Server == nil || d.Server.IP != "127.0.0.1" || d.Server.CDN != "Cloudflare" || d.Server.Proxied {
		t.Fatalf("%+v", d.Server)
	}
	loc, err := a.ServerLocation(d.ID)
	if err != nil || loc.Place != "this-computer" || loc.CDN != "Cloudflare" {
		t.Fatalf("%+v %v", loc, err)
	}
}
