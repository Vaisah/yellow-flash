package filesystem

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":                      "report.pdf",
		"../../etc/passwd":                "passwd",
		`C:\Windows\evil.exe`:             "evil.exe",
		"a<b>:c|d?.txt":                   "a_b__c_d_.txt",
		"CON.txt":                         "_CON.txt",
		"lpt1":                            "_lpt1",
		"trailing dots... ":               "trailing dots",
		"tab\there.bin":                   "tab_here.bin",
		"résumé.pdf":                      "résumé.pdf",
		strings.Repeat("a", 300) + ".tar": strings.Repeat("a", 196) + ".tar",
	} {
		got, err := SanitizeFilename(in)
		if err != nil || got != want {
			t.Errorf("SanitizeFilename(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "..", "/", " . ", "dir/.."} {
		if got, err := SanitizeFilename(in); !errors.Is(err, ErrInvalidFilename) {
			t.Errorf("SanitizeFilename(%q) = %q, want error", in, got)
		}
	}
	long, _ := SanitizeFilename(strings.Repeat("é", 150))
	if len(long) > maxNameBytes || !strings.HasPrefix(long, "é") {
		t.Errorf("multibyte truncation broke the name: %d bytes", len(long))
	}
}

func TestFilenameHelpers(t *testing.T) {
	u, _ := url.Parse("https://example.com/a/b/movie%20one.mp4?x=1")
	if got := FilenameFromURL(u); got != "movie one.mp4" {
		t.Errorf("FilenameFromURL = %q", got)
	}
	u, _ = url.Parse("https://example.com/")
	if got := FilenameFromURL(u); got != "download" {
		t.Errorf("root URL = %q", got)
	}
	if Candidate("movie.mp4", 2) != "movie (2).mp4" || Candidate("README", 1) != "README (1)" || Candidate(".bashrc", 1) != ".bashrc (1)" {
		t.Error("candidate naming")
	}
	if filepath.Base(TempPath("dir", "movie.mp4")) != "movie.mp4.part" {
		t.Error("temp path naming")
	}
}

func TestExtensionHelpers(t *testing.T) {
	for ct, want := range map[string]string{
		"video/mp4": ".mp4", "Video/WebM; codecs=vp9": ".webm", "text/html; charset=utf-8": ".html",
		"application/octet-stream": "", "": "", "x/unknown": "",
	} {
		if got := ExtensionForMIME(ct); got != want {
			t.Errorf("ExtensionForMIME(%q) = %q, want %q", ct, got, want)
		}
	}
	if WithExtension("watch", ".mp4") != "watch.mp4" || WithExtension("clip.mkv", ".mp4") != "clip.mkv" || WithExtension("x", "") != "x" {
		t.Error("WithExtension")
	}
	if !IsHTMLName("index.HTML") || IsHTMLName("watch") || IsHTMLName("video.mp4") {
		t.Error("IsHTMLName")
	}
}

func writeTemp(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "file.bin.part")
	if err := os.WriteFile(p, []byte(content), TempFileMode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"file.bin", "file (1).bin"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tmp := writeTemp(t, dir, "new data")
	got, err := NewPublisher().Publish(tmp, dir, "file.bin")
	if err != nil || got != "file (2).bin" {
		t.Fatalf("publish = %q, %v", got, err)
	}
	for name, want := range map[string]string{"file.bin": "user data", "file (1).bin": "user data", "file (2).bin": "new data"} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != want {
			t.Errorf("%s = %q", name, b)
		}
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temporary file left behind")
	}
}

func TestPublishFallbackWithoutHardLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.bin"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := writeTemp(t, dir, "payload")
	p := &Publisher{link: func(string, string) error { return errors.New("operation not supported") }, rename: os.Rename}
	got, err := p.Publish(tmp, dir, "file.bin")
	if err != nil || got != "file (1).bin" {
		t.Fatalf("fallback publish = %q, %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "file (1).bin")); string(b) != "payload" {
		t.Errorf("published content %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "file.bin")); string(b) != "keep" {
		t.Error("existing file overwritten")
	}
}

func TestPublishRecoversInterruptedLink(t *testing.T) {
	dir := t.TempDir()
	tmp := writeTemp(t, dir, "payload")
	if err := os.Link(tmp, filepath.Join(dir, "file.bin")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	got, err := NewPublisher().Publish(tmp, dir, "file.bin")
	if err != nil || got != "file.bin" {
		t.Fatalf("recovery publish = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "file (1).bin")); !os.IsNotExist(err) {
		t.Error("duplicate created for an already-linked file")
	}
}

func TestPreallocate(t *testing.T) {
	f, err := OpenTemp(filepath.Join(t.TempDir(), "x.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = Preallocate(f, 1<<20); err != nil {
		t.Fatal(err)
	}
	if info, _ := f.Stat(); info.Size() != 1<<20 {
		t.Fatalf("size %d", info.Size())
	}
}
