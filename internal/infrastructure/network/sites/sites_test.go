// Ported from Mister Download Manager, crates/engine/src/sites/gdrive.rs and crates/engine/src/html.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package sites

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const id = "1s52ek_4YTDRt_EOkx1FS53u-vJa0c4nu"

func u(t *testing.T, s string) *url.URL {
	t.Helper()
	p, err := url.Parse(strings.ReplaceAll(s, "ID", id))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEveryLinkFormIsRecognised(t *testing.T) {
	type want struct {
		kind DriveKind
		doc  DocKind
	}
	file, folder := &want{kind: DriveFile}, &want{kind: DriveFolder}
	doc := func(d DocKind) *want { return &want{kind: DriveDoc, doc: d} }
	cases := []struct {
		link string
		want *want
	}{
		{"https://drive.google.com/file/d/ID/view?usp=sharing", file},
		{"https://drive.google.com/file/d/ID", file},
		{"https://drive.google.com/file/d/ID/", file},
		{"https://drive.google.com/file/d/ID/preview#t=1", file},
		{"https://drive.google.com/file/u/1/d/ID/view", file},
		{"https://docs.google.com/file/d/ID/edit", file},
		{"http://drive.google.com/file/d/ID/view", file},
		{"https://DRIVE.Google.com/file/d/ID/view", file},
		{"https://drive.google.com/uc?id=ID&export=download", file},
		{"https://docs.google.com/uc?export=download&id=ID", file},
		{"https://drive.google.com/open?id=ID", file},
		{"https://drive.usercontent.google.com/download?id=ID&export=download", file},
		{"https://drive.usercontent.google.com/uc?id=ID", file},
		{"https://docs.google.com/document/d/ID/edit", doc(Document)},
		{"https://docs.google.com/document/d/ID", doc(Document)},
		{"https://docs.google.com/spreadsheets/u/0/d/ID/htmlview", doc(Spreadsheet)},
		{"https://docs.google.com/presentation/d/ID/preview#slide=1", doc(Presentation)},
		{"https://docs.google.com/drawings/d/ID/edit", doc(Drawing)},
		{"https://drive.google.com/drive/folders/ID?usp=sharing", folder},
		{"https://drive.google.com/drive/u/0/folders/ID", folder},
		{"https://drive.google.com/drive/mobile/folders/ID/", folder},
		{"https://drive.google.com/folderview?id=ID", folder},
		{"https://drive.google.com/embeddedfolderview?id=ID", folder},
		// Already a file's address: downloaded as it is.
		{"https://docs.google.com/document/d/ID/export?format=pdf", nil},
		{"https://docs.google.com/spreadsheets/d/ID/pub?output=csv", nil},
		{"https://drive.usercontent.google.com/download?id=ID&export=download&confirm=t&uuid=x", nil},
		// Not Drive, or not a link to something.
		{"https://drive.google.com.evil.test/file/d/ID/view", nil},
		{"https://evil.test/drive.google.com/file/d/ID/view", nil},
		{"https://drive.google.com/file/d/short/view", nil},
		{"https://drive.google.com/file/d/ID/view/more", nil},
		{"https://drive.google.com/drive/my-drive", nil},
		{"https://drive.google.com/", nil},
		{"https://drive.google.com/uc?id=ID&id=ID", nil},
		{"https://drive.google.com/uc?export=download", nil},
		{"https://docs.google.com/forms/d/ID/viewform", nil},
		{"ftp://drive.google.com/file/d/ID/view", nil},
	}
	for _, c := range cases {
		l, ok := ParseDrive(u(t, c.link))
		switch {
		case c.want == nil && ok:
			t.Errorf("%s: recognised as %+v", c.link, l)
		case c.want != nil && (!ok || l.ID != id || l.Kind != c.want.kind || l.Doc != c.want.doc):
			t.Errorf("%s: got %+v %v, want %+v", c.link, l, ok, *c.want)
		}
	}
}

func TestKeysAndAccountsAreKept(t *testing.T) {
	l, _ := ParseDrive(u(t, "https://drive.google.com/file/u/2/d/ID/view?resourcekey=0-K&usp=sharing"))
	if l.ResourceKey != "0-K" || l.AuthUser != "2" {
		t.Fatalf("%+v", l)
	}
	l, _ = ParseDrive(u(t, "https://drive.google.com/uc?id=ID&authuser=1"))
	if l.AuthUser != "1" || l.ResourceKey != "" {
		t.Fatalf("%+v", l)
	}
}

func TestLinksLeadToTheFileKeepingSchemeAndPort(t *testing.T) {
	at := func(s string) string {
		next, ok := Rewrite(u(t, s))
		if !ok {
			return ""
		}
		return strings.ReplaceAll(next.String(), id, "ID")
	}
	for link, want := range map[string]string{
		"https://drive.google.com/file/d/ID/view?usp=sharing":                           "https://drive.usercontent.google.com/download?id=ID&export=download&confirm=t",
		"http://drive.google.com:8080/open?id=ID&resourcekey=0-K&authuser=1":            "http://drive.usercontent.google.com:8080/download?id=ID&export=download&confirm=t&resourcekey=0-K&authuser=1",
		"https://docs.google.com/document/d/ID/edit?usp=sharing":                        "https://docs.google.com/document/d/ID/export?format=docx",
		"https://docs.google.com/spreadsheets/d/ID/edit#gid=0":                          "https://docs.google.com/spreadsheets/d/ID/export?format=xlsx",
		"https://docs.google.com/presentation/d/ID/edit?resourcekey=0-K":                "https://docs.google.com/presentation/d/ID/export?format=pptx&resourcekey=0-K",
		"https://docs.google.com/drawings/d/ID/edit":                                    "https://docs.google.com/drawings/d/ID/export/png",
		"https://drive.google.com/drive/folders/ID":                                     "",
		"https://drive.usercontent.google.com/download?id=ID&export=download&confirm=t": "",
	} {
		if got := at(link); got != want {
			t.Errorf("%s\n got %s\nwant %s", link, got, want)
		}
	}
	// The file host's address without a confirmation gets one.
	if at("https://drive.usercontent.google.com/download?id=ID") == "" {
		t.Error("unconfirmed file host address not rewritten")
	}
	if r := NotAFile(u(t, "https://drive.google.com/drive/folders/ID")); r == nil || r.Kind != Folder {
		t.Errorf("folder: %v", r)
	}
	if r := NotAFile(u(t, "https://drive.google.com/file/d/ID/view")); r != nil {
		t.Errorf("file refused: %v", r)
	}
}

func TestADocAskedAsAFileIsLookedUpOnce(t *testing.T) {
	at := u(t, "http://drive.usercontent.google.com:81/download?id=ID&confirm=t&resourcekey=0-K")
	next, ok := SecondLook(at)
	if !ok || next.String() != "http://drive.google.com:81/open?id="+id+"&resourcekey=0-K" {
		t.Fatalf("%v %v", next, ok)
	}
	if !ExpectsFile(at) || !ExpectsFile(u(t, "https://docs.google.com/document/d/ID/edit")) {
		t.Fatal("expects a file")
	}
	if ExpectsFile(u(t, "https://drive.google.com/drive/folders/ID")) || ExpectsFile(u(t, "https://docs.google.com/forms/d/ID/viewform")) {
		t.Fatal("not a file")
	}
}

func page(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func next(t *testing.T, at string, html []byte) string {
	t.Helper()
	p := ReadPage(u(t, at), html)
	if p.Action != Next {
		t.Fatalf("%+v", p)
	}
	return strings.ReplaceAll(p.Next.String(), id, "ID")
}

func TestTheVirusScanPageIsConfirmed(t *testing.T) {
	at := "https://drive.usercontent.google.com/download?id=ID&export=download"
	if got := next(t, at, page(t, "drive-warning.html")); got != "https://drive.usercontent.google.com/download?id=ID&export=download&confirm=t&uuid=c45fcb99-500b-4ea1-8988-c8099948304d" {
		t.Errorf("warning: %s", got)
	}
	// A signed-in user's page carries its token, which goes too.
	if got := next(t, at, page(t, "drive-warning-at.html")); got != "https://drive.usercontent.google.com/download?id=ID&export=download&confirm=t&uuid=0f1e2d3c-0000-4000-8000-000000000001&at=AENtkX%3Atoken%261" {
		t.Errorf("with token: %s", got)
	}
	// The older page: its link, on docs.google.com.
	if got := next(t, "https://docs.google.com/uc?export=download&id=ID", page(t, "drive-old.html")); got != "https://docs.google.com/uc?export=download&confirm=AbCd&id=ID" {
		t.Errorf("old: %s", got)
	}
	// The address in the page's data.
	if got := next(t, at, page(t, "drive-json.html")); got != "https://drive.usercontent.google.com/download?id=ID&export=download&confirm=t" {
		t.Errorf("json: %s", got)
	}
}

func TestAFormOffDriveOrBackToItselfIsNotFollowed(t *testing.T) {
	at := u(t, "https://drive.usercontent.google.com/download?id=ID&export=download")
	if p := ReadPage(at, page(t, "drive-offsite.html")); p.Action != Nothing {
		t.Errorf("offsite: %+v", p)
	}
	itself := strings.ReplaceAll(`<form id="download-form" action="/download?export=download&amp;id=ID"></form>`, "ID", id)
	if p := ReadPage(at, []byte(itself)); p.Action != Nothing {
		t.Errorf("itself: %+v", p)
	}
	if p := ReadPage(at, []byte("<html><p>Drive</p></html>")); p.Action != Nothing {
		t.Errorf("plain: %+v", p)
	}
	if p := ReadPage(u(t, "https://example.com/"), page(t, "drive-warning.html")); p.Action != Nothing {
		t.Errorf("not Drive: %+v", p)
	}
}

func TestDrivesErrorPageIsARefusalInItsWords(t *testing.T) {
	at := u(t, "https://drive.usercontent.google.com/download?id=ID&export=download")
	p := ReadPage(at, page(t, "drive-quota.html"))
	if p.Action != Refused || !strings.HasPrefix(p.Refusal.Message, "Too many users have viewed or downloaded") ||
		!strings.Contains(p.Refusal.Message, "can't") {
		t.Fatalf("%+v %v", p, p.Refusal)
	}
	p = ReadPage(at, []byte(`<p class="uc-error-caption">Sorry, the file you have requested does not exist.</p>`))
	if p.Action != Refused || p.Refusal.Message != "Sorry, the file you have requested does not exist." {
		t.Fatalf("caption: %+v", p.Refusal)
	}
	p = ReadPage(at, []byte(`<p class="uc-error-subcaption">`+strings.Repeat("word ", 200)+`</p>`))
	if m := p.Refusal.Message; utf8.RuneCountInString(m) > messageMax+1 || !strings.HasSuffix(m, "…") {
		t.Fatalf("long: %d %q", utf8.RuneCountInString(m), m)
	}
}

func TestSignInAndNoAccessAnswers(t *testing.T) {
	link := u(t, "https://drive.google.com/file/d/ID/view")
	login := u(t, "https://accounts.google.com/ServiceLogin?continue=x")
	if r := RefusedBy(link, login, 200); r == nil || r.Kind != SignIn {
		t.Errorf("sign in: %v", r)
	}
	if r := RefusedBy(u(t, "https://site.test/f"), login, 200); r != nil {
		t.Errorf("another site's sign-in: %v", r)
	}
	file := u(t, "https://drive.usercontent.google.com/download?id=ID")
	for status, want := range map[int]bool{403: true, 401: true, 404: false} {
		if r := RefusedBy(link, file, status); (r != nil) != want || (r != nil && r.Kind != NoAccess) {
			t.Errorf("%d: %v", status, r)
		}
	}
	if r := RefusedBy(link, u(t, "https://cdn.test/f"), 403); r != nil {
		t.Errorf("cdn: %v", r)
	}
}

func TestDrivesHostsAreOneParty(t *testing.T) {
	drive := u(t, "https://drive.google.com/file/d/ID/view")
	for other, want := range map[string]bool{
		"https://drive.usercontent.google.com/download?id=ID": true,
		"https://docs.google.com/document/d/ID/export":        true,
		"https://drive.usercontent.google.com:8443/x":         false,
		"https://accounts.google.com/ServiceLogin":            false,
		"https://doc-0.docs.googleusercontent.com/export/x":   false,
		"https://drive.google.com.evil.test/x":                false,
	} {
		if got := SameParty(drive, u(t, other)); got != want {
			t.Errorf("%s: %v", other, got)
		}
	}
	if UserAgent(drive) != DriveUserAgent || UserAgent(u(t, "https://example.com/")) != "" {
		t.Error("user agent")
	}
}

/* ---------- html.rs ---------- */

func TestAttributesAreReadInOrder(t *testing.T) {
	tg := `<input type="hidden" name='id' value=abc data-value="x">`
	for name, want := range map[string]string{"type": "hidden", "NAME": "id", "value": "abc"} {
		if got, ok := attribute(tg, name); !ok || got != want {
			t.Errorf("%s: %q %v", name, got, ok)
		}
	}
	if _, ok := attribute(tg, "id"); ok {
		t.Error("a value isn't a name")
	}
	if got, _ := attribute("<form id=download-form>", "id"); got != "download-form" {
		t.Errorf("unquoted: %q", got)
	}
	if got, _ := attribute("<input disabled name=x>", "name"); got != "x" {
		t.Errorf("after a bare attribute: %q", got)
	}
	if _, ok := attribute("<br/>", "x"); ok {
		t.Error("<br/>")
	}
	if got, _ := attribute(`<a href="/uc?a=1&amp;b=2">`, "href"); got != "/uc?a=1&amp;b=2" {
		t.Errorf("entities stay for decode: %q", got)
	}
}

func TestTagsAreWholeNames(t *testing.T) {
	var got []string
	for _, tg := range tags("<META a=1><metadata><meta\nb=2/><p>", "meta") {
		got = append(got, tg.text)
	}
	if len(got) != 2 || got[0] != "<META a=1>" || got[1] != "<meta\nb=2/>" {
		t.Fatalf("%q", got)
	}
}

func TestReferencesDecode(t *testing.T) {
	if got := decode("a&amp;b &lt;c&gt; &quot;d&quot; &#39;e&#x27; &copy; & f&amp"); got != `a&b <c> "d" 'e' &copy; & f&amp` {
		t.Errorf("%q", got)
	}
	if got := decode("can&#39;t"); got != "can't" {
		t.Errorf("%q", got)
	}
}

func TestAnElementsText(t *testing.T) {
	p := `<p class="uc-error-caption">Sorry, you can&#39;t
            view this</p><p class="x uc-error-subcaption"><b>Too many</b> users</p>`
	if got, _ := textOfClass(p, "uc-error-caption"); got != "Sorry, you can't view this" {
		t.Errorf("%q", got)
	}
	if got, _ := textOfClass(p, "uc-error-subcaption"); got != "Too many users" {
		t.Errorf("%q", got)
	}
	if _, ok := textOfClass(p, "uc-error"); ok {
		t.Error("a part of a class name")
	}
	if _, ok := textOfClass("<p class=e>  </p>", "e"); ok {
		t.Error("empty text")
	}
}
