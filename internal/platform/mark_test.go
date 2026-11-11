package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAFileIsMarkedAsFromTheInternet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.exe")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	MarkFromInternet(p, "https://example.com/a.zip", "https://example.com/\r\nZoneId=0")
	if runtime.GOOS != "windows" {
		return // xattrs depend on the file system (tmpfs may refuse user.*)
	}
	b, err := os.ReadFile(p + ":Zone.Identifier")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// A line break in an address can't add a line of its own (ZoneId=0 would trust the file).
	zones := 0
	for _, l := range strings.Split(s, "\r\n") {
		if strings.HasPrefix(l, "ZoneId=") {
			zones++
		}
	}
	if !strings.Contains(s, "ZoneId=3\r\n") || !strings.Contains(s, "HostUrl=https://example.com/a.zip") || zones != 1 {
		t.Fatal(s)
	}
}

func TestTrashGoesToTheTestFolder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(TrashDirEnv, filepath.Join(dir, "trash"))
	p := filepath.Join(dir, "a.zip")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	if err := Trash([]string{p}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "trash", "a.zip")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("still there")
	}
}

// The real Recycle Bin, once: a file made for it.
func TestTrashUsesTheRecycleBin(t *testing.T) {
	if runtime.GOOS != "windows" || testing.Short() {
		t.Skip("Windows only")
	}
	p := filepath.Join(t.TempDir(), "yellow-flash-trash-test.txt")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	if err := Trash([]string{p}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("still there")
	}
}
