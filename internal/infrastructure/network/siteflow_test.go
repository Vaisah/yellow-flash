package network

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vaisah/internal/infrastructure/network/sites"
)

const (
	fileID  = "1s52ek_4YTDRt_EOkx1FS53u-vJa0c4nu"
	quotaID = "1QuotaQuotaQuotaQuotaQuotaQuota1"
	loginID = "1LoginLoginLoginLoginLoginLogin1"
)

// fakeDrive answers for Drive's hosts (routed by Host) the way Google does:
// a virus-scan form before a large file, a quota page for a Google Doc's ID
// at the file host, and a sign-in redirect for an unshared file.
type fakeDrive struct {
	mu       sync.Mutex
	seen     []string          // "host path?query" of each request
	cookies  map[string]string // by "host path"
	agents   map[string]string // by host
	fileBody string
}

func (f *fakeDrive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.Host)
	f.mu.Lock()
	f.seen = append(f.seen, host+" "+r.URL.RequestURI())
	f.cookies[host+" "+r.URL.Path] = r.Header.Get("Cookie")
	f.agents[host] = r.Header.Get("User-Agent")
	f.mu.Unlock()
	q := r.URL.Query()
	switch {
	case host == "drive.usercontent.google.com" && q.Get("id") == fileID && q.Get("uuid") == "":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><form id="download-form" action="/download" method="get">
<input type="hidden" name="id" value="`+fileID+`"><input type="hidden" name="export" value="download">
<input type="hidden" name="confirm" value="t"><input type="hidden" name="uuid" value="u-1"></form></html>`)
	case host == "drive.usercontent.google.com" && q.Get("id") == fileID:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="big.iso"`)
		_, _ = io.WriteString(w, f.fileBody)
	case host == "drive.usercontent.google.com" && q.Get("id") == quotaID:
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<p class="uc-error-subcaption">Too many users have viewed or downloaded this file recently.</p>`)
	case host == "drive.google.com" && r.URL.Path == "/open" && q.Get("id") == quotaID:
		// A Doc's ID: Drive sends it on to the editor, on Docs.
		http.Redirect(w, r, "http://docs.google.com:"+portOf(r)+"/document/d/"+quotaID+"/edit", http.StatusFound)
	case host == "docs.google.com" && strings.HasSuffix(r.URL.Path, "/edit"):
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>editor</html>")
	case host == "docs.google.com" && strings.HasSuffix(r.URL.Path, "/export"):
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		_, _ = io.WriteString(w, "DOCX")
	case host == "drive.usercontent.google.com" && q.Get("id") == loginID:
		http.Redirect(w, r, "http://accounts.google.com:"+portOf(r)+"/ServiceLogin", http.StatusFound)
	case host == "accounts.google.com":
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>sign in</html>")
	default:
		http.NotFound(w, r)
	}
}

func portOf(r *http.Request) string {
	_, port, _ := net.SplitHostPort(r.Host)
	return port
}

// driveProber returns a prober whose client sends every host to the fake
// server, and the server's port.
func driveProber(t *testing.T) (*Prober, *fakeDrive, string) {
	t.Helper()
	fd := &fakeDrive{cookies: map[string]string{}, agents: map[string]string{}, fileBody: strings.Repeat("x", 4096)}
	srv := httptest.NewServer(fd)
	t.Cleanup(srv.Close)
	client := NewClient(Options{AllowPrivate: true})
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return NewProber(client), fd, port
}

func TestDriveViewerLinkGoesThroughTheVirusScanPageToTheFile(t *testing.T) {
	p, fd, port := driveProber(t)
	link := "http://drive.google.com:" + port + "/file/d/" + fileID + "/view?usp=sharing"
	meta, res, err := p.Open(context.Background(), link, http.Header{"Cookie": {"SID=1"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != fd.fileBody || meta.Filename != "big.iso" || !strings.Contains(meta.URL, "uuid=u-1") {
		t.Fatalf("meta %+v, %d bytes", meta, len(body))
	}
	if fd.agents["drive.usercontent.google.com"] != sites.DriveUserAgent {
		t.Fatalf("agent %q", fd.agents["drive.usercontent.google.com"])
	}
	for _, s := range fd.seen {
		if strings.HasPrefix(s, "drive.google.com ") {
			t.Fatalf("the viewer was asked: %v", fd.seen)
		}
	}
}

func TestADocsIDAtTheFileHostIsLookedUpOnceAndExported(t *testing.T) {
	p, fd, port := driveProber(t)
	link := "http://drive.google.com:" + port + "/uc?id=" + quotaID + "&export=download"
	meta, res, err := p.Open(context.Background(), link, http.Header{"Cookie": {"SID=1"}})
	if err != nil {
		t.Fatalf("%v (seen %v)", err, fd.seen)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "DOCX" || !strings.Contains(meta.URL, "/export?format=docx") {
		t.Fatalf("meta %+v body %q", meta, body)
	}
	// The sign-in cookie went with Drive's redirect to Docs (Go alone drops it).
	if got := fd.cookies["docs.google.com /document/d/"+quotaID+"/edit"]; got != "SID=1" {
		t.Fatalf("cookie on the redirect to docs: %q (%v)", got, fd.cookies)
	}
}

func TestDriveRefusalsAreNotRetriedAndFoldersNotAsked(t *testing.T) {
	p, fd, port := driveProber(t)
	_, _, err := p.Open(context.Background(), "http://drive.google.com:"+port+"/uc?id="+loginID, nil)
	var r *sites.Refusal
	if !errors.As(err, &r) || r.Kind != sites.SignIn {
		t.Fatalf("sign-in: %v", err)
	}
	before := len(fd.seen)
	_, err = p.Probe(context.Background(), "http://drive.google.com:"+port+"/drive/folders/"+fileID, nil)
	if !errors.As(err, &r) || r.Kind != sites.Folder || len(fd.seen) != before {
		t.Fatalf("folder: %v (asked %d times)", err, len(fd.seen)-before)
	}
}

func TestCredentialsStayWithinOneSitesHosts(t *testing.T) {
	var gotCookie string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
	}))
	defer other.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	}))
	defer src.Close()
	c := NewClient(Options{AllowPrivate: true})
	req, _ := http.NewRequest(http.MethodGet, strings.Replace(src.URL, "127.0.0.1", "localhost", 1), nil)
	req.Header.Set("Cookie", "secret=1")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if gotCookie != "" {
		t.Fatalf("a cookie crossed to another host: %q", gotCookie)
	}
}
