package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vaisah/internal/crash"
)

func TestTheLastRunsCrashIsOfferedOnceAndFillsTheForm(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(crash.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	out := "panic: boom\n\ngoroutine 7 [running]:\ngithub.com/vaisah/internal/engine.(*Manager).run(0x1)\n\t/w/internal/engine/executor.go:40 +0x1\n"
	if err := os.WriteFile(filepath.Join(crash.Dir(dir), "pending.log"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := crash.Collect(dir, crash.ThisSystem(AppName, AppVersion))
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
	a := &App{dataDir: dir, lastCrash: r}
	if got := a.LastCrashReport(); got == nil || got.Signature != "panic in github.com/vaisah/internal/engine.(*Manager).run" {
		t.Fatal(got)
	}
	if a.LastCrashReport() != nil {
		t.Fatal("offered once")
	}
	if l := a.ListCrashReports(); len(l) != 1 || l[0].ID != r.ID {
		t.Fatal(l)
	}
	u, err := a.CrashIssueURL(r.ID, "  adding a magnet  ")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(strings.SplitN(u, "?", 2)[1])
	if !strings.HasPrefix(u, "https://github.com/"+crash.Repo+"/issues/new?template=crash.yml") ||
		q.Get("signature") != r.Signature || q.Get("version") != AppVersion || q.Get("what") != "adding a magnet" ||
		!strings.Contains(q.Get("report"), "panic: boom") {
		t.Fatal(u)
	}
	if _, err := a.CrashIssueURL("nope", ""); err == nil {
		t.Fatal("no such report")
	}
	if err := a.DeleteCrashReport(r.ID); err != nil || len(a.ListCrashReports()) != 0 {
		t.Fatal(err)
	}
}
