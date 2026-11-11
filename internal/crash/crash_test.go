// Ported from Mister Download Manager, crates/crash (tests: e2e.rs, github.rs, logs.rs, report.rs)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package crash

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const sample = `panic: runtime error: index out of range [3] with length 0

goroutine 18 [running]:
main.downloadWorker()
	C:/Users/someone/src/idm-clone/internal/crash/testdata/crashy/main.go:31 +0x1d
created by main.main in goroutine 1
	C:/Users/someone/src/idm-clone/internal/crash/testdata/crashy/main.go:26 +0x9c

goroutine 1 [sleep]:
time.Sleep(0x2540be400)
	C:/Program Files/Go/src/runtime/time.go:300 +0x11c
main.main()
	C:/Users/someone/src/idm-clone/internal/crash/testdata/crashy/main.go:27 +0xa8
`

func TestATracebackIsReadAndNamed(t *testing.T) {
	tb := Parse(strings.ReplaceAll(sample, "\n", "\r\n"))
	if tb.Kind != Panic || !strings.HasPrefix(tb.Message, "panic: runtime error: index out of range") {
		t.Fatalf("%+v", tb)
	}
	if len(tb.Goroutines) != 2 || tb.Goroutines[0].Header != "goroutine 18 [running]" || len(tb.Goroutines[0].Frames) != 2 {
		t.Fatalf("%+v", tb.Goroutines)
	}
	f := tb.Goroutines[0].Frames[0]
	if f.Function != "main.downloadWorker()" || f.Line != 31 || !strings.HasSuffix(f.File, "main.go") {
		t.Fatalf("%+v", f)
	}
	if s := tb.Signature(); s != "panic in main.downloadWorker" {
		t.Fatal(s)
	}
	fatal := Parse("fatal error: sync: unlock of unlocked mutex\n\ngoroutine 1 [running]:\nsync.fatal({0x1, 0x2})\n\t/go/src/runtime/panic.go:1 +0x1\n" +
		"sync.(*Mutex).Unlock(...)\n\t/go/src/sync/mutex.go:2\n" +
		"github.com/vaisah/internal/engine.(*Manager).run.func2(0xc000)\n\t/w/internal/engine/x.go:9 +0x2\n")
	if fatal.Kind != Crash || fatal.Signature() != "crash in github.com/vaisah/internal/engine.(*Manager).run" {
		t.Fatal(fatal.Kind, fatal.Signature())
	}
	if s := Parse("fatal error: out of memory\n").Signature(); s != "crash in out of memory" {
		t.Fatal(s)
	}
}

func TestFunctionNamesReadTheSameInEveryBuild(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/vaisah/internal/engine.(*Manager).run(0xc0001, {0x2, 0x3})": "github.com/vaisah/internal/engine.(*Manager).run",
		"main.main.func1.2()":                 "main.main",
		"created by main.main in goroutine 1": "main.main",
		"net/http.(*conn).serve.gowrap1()":    "net/http.(*conn).serve",
		"panic({0x7ff6, 0xc000})":             "panic",
	} {
		if got := trimFunction(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestAReportHidesTheHomeFolder(t *testing.T) {
	text := Text(Parse(sample), "2026-11-11T08:00:00Z", ThisSystem("Yellow Flash", "0.2.0"),
		[]string{`level=INFO msg="opened C:\Users\someone\Downloads\a.zip"`}, `C:\Users\someone`)
	for _, want := range []string{"---- Yellow Flash crash report ----", "Signature: panic in main.downloadWorker",
		"Description: Yellow Flash hit an internal error and closed", "at internal/crash/testdata/crashy/main.go:31",
		"-- The log's last 1 lines --", `~\Downloads\a.zip`, "Every other goroutine (1)", "Yellow Flash 0.2.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "someone") {
		t.Error("the home folder is in the report")
	}
}

func TestALongReportIsCutAtALineWithinTheBudget(t *testing.T) {
	var report strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&report, "line %d of the report\n", i)
	}
	u := NewIssueURL("Freeze in x", []Field{{"version", "0.2.0"}, {"kind", "panic"}}, report.String(), "(cut: see the file)")
	if len(u) > URLBudget || !strings.Contains(u, "template=crash.yml") || !strings.Contains(u, "&version=0.2.0") {
		t.Fatal(len(u))
	}
	q, _ := url.ParseQuery(strings.SplitN(u, "?", 2)[1])
	got := q.Get("report")
	if !strings.HasPrefix(got, "line 0 of the report\n") || !strings.HasSuffix(strings.TrimSpace(got), "(cut: see the file)") {
		t.Fatal(got[:40])
	}
	for _, l := range strings.Split(got, "\n") {
		if l != "" && !strings.HasPrefix(l, "line ") && !strings.HasPrefix(l, "(") {
			t.Fatalf("a cut line: %q", l)
		}
	}
	if short := NewIssueURL("t", nil, "short report", "cut"); !strings.HasSuffix(short, "&report=short+report") {
		t.Fatal(short)
	}
}

func TestTheIssueFiledAlreadyIsFound(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query().Get("q")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
			{"number": 3, "title": "old", "html_url": "https://github.com/x/3", "state": "closed"},
			{"number": 9, "title": "open one", "html_url": "https://github.com/x/9", "state": "open"},
		}})
	}))
	defer srv.Close()
	old := SearchURL
	SearchURL = srv.URL
	defer func() { SearchURL = old }()
	f, err := FindIssue(context.Background(), srv.Client(), `panic in main."x"`)
	if err != nil || f == nil || f.Number != 9 {
		t.Fatal(f, err)
	}
	if asked != `repo:`+Repo+` is:issue in:body "panic in main.x"` {
		t.Fatal(asked)
	}
}

func TestTheLogRollsOverAndItsTailSpansTheFiles(t *testing.T) {
	data := t.TempDir()
	r, err := NewRolling(LogDir(data))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 1023) + "\n"
	for i := 0; i < MaxLogBytes/1024+10; i++ {
		_, _ = r.Write([]byte(line))
	}
	_, _ = r.Write([]byte("last\n"))
	r.Close()
	if _, err := os.Stat(rolledLog(LogDir(data), 1)); err != nil {
		t.Fatal("rolled over", err)
	}
	tail := Tail(data, 20)
	if len(tail) != 20 || tail[19] != "last" {
		t.Fatal(len(tail))
	}
	if Tail(t.TempDir(), 5) != nil {
		t.Fatal("no log, no lines")
	}
}

// A real crash, end to end: a program that installed the crash output
// panics in a goroutine of its own (or dies of a fatal error); the next
// start's Collect makes it a report, named after the function that
// failed, with the log's lines and no home folder.
func TestARealCrashBecomesAReport(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	exe := filepath.Join(t.TempDir(), "crashy")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, "./testdata/crashy").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, mode := range []string{"panic", "fatal"} {
		data := t.TempDir()
		if err := exec.Command(exe, data, mode).Run(); err == nil {
			t.Fatalf("%s: crashy didn't crash", mode)
		}
		r, err := Collect(data, ThisSystem("Yellow Flash", "test"))
		if err != nil || r == nil {
			t.Fatal(mode, r, err)
		}
		switch mode {
		case "panic":
			if r.Kind != Panic || r.Signature != "panic in main.downloadWorker" || !strings.Contains(r.Text, "index out of range") {
				t.Fatalf("%+v\n%s", r, r.Text)
			}
		case "fatal":
			// The runtime keeps a fatal error's line on stderr only: the stack says it.
			if r.Kind != Crash || r.Signature != "crash in main.main" || !strings.Contains(r.Text, "sync.(*Mutex).Unlock") ||
				!strings.Contains(r.Text, "its message wasn't kept") {
				t.Fatalf("%+v\n%s", r, r.Text)
			}
		}
		if !strings.Contains(r.Text, "crashy is about to fail") {
			t.Error("the log's lines are in the report")
		}
		if home, _ := os.UserHomeDir(); len(home) > 3 && strings.Contains(r.Text, home) {
			t.Error("the home folder is in the report")
		}
		// Collected once; listed; removed.
		if again, _ := Collect(data, System{}); again != nil {
			t.Fatal("collected twice")
		}
		list := List(data)
		if len(list) != 1 || list[0].ID != r.ID || list[0].Signature != r.Signature || list[0].Kind != r.Kind {
			t.Fatalf("%+v", list)
		}
		if err := Remove(data, r.ID); err != nil || len(List(data)) != 0 {
			t.Fatal(err)
		}
		if Remove(data, "../x") == nil {
			t.Fatal("only a report's id")
		}
	}
}
