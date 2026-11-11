// Ported from Mister Download Manager, crates/crash/src/report.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package crash is Yellow Flash's crash reports. MDM's monitor process
// writes a minidump; Go has none, so the runtime itself writes what a dump
// would hold: debug.SetCrashOutput sends an unrecovered panic's or a fatal
// error's report (every goroutine's stack) to <data>/crashes/pending.log
// as the process dies. At the next start that becomes a report (Collect),
// which the app offers to send on GitHub, as MDM's does.
package crash

// The text of a report, in the manner of MDM's (and Minecraft's): what
// happened, the stack of the goroutine that crashed, the system, the log's
// last lines, then every goroutine. English on purpose: it is read by
// whoever fixes the bug, whatever language the app speaks.
//
// Nothing personal on purpose: the home folder shows as "~". A panic's
// message and the log can hold text the user typed (an address, a file
// name); the app shows all of it before anything is sent.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Kind is what happened.
type Kind string

// The kinds.
const (
	// Panic: an unrecovered panic (an internal error).
	Panic Kind = "panic"
	// Crash: a fatal runtime error or a signal (a crash).
	Crash Kind = "crash"
)

// Frame is one call on a goroutine's stack.
type Frame struct {
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Goroutine is one goroutine of a crash's output.
type Goroutine struct {
	// Header is its first line ("goroutine 7 [running]").
	Header string  `json:"header"`
	Frames []Frame `json:"frames"`
}

// Traceback is a crash's output read: what went wrong and the goroutines,
// the one that crashed first.
type Traceback struct {
	Kind Kind
	// Message is the panic's or fatal error's words (several lines may follow).
	Message    string
	Goroutines []Goroutine
}

var (
	goroutineLine = regexp.MustCompile(`^goroutine \d+.*\[[^\]]*\]:?$`)
	fileLine      = regexp.MustCompile(`^\t(.+):(\d+)( \+0x[0-9a-f]+)?.*$`)
)

// Parse reads a Go runtime's crash output.
func Parse(raw string) Traceback {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	t := Traceback{Kind: Crash}
	var message []string
	i := 0
	for ; i < len(lines) && !goroutineLine.MatchString(lines[i]); i++ {
		if l := strings.TrimRight(lines[i], " "); l != "" && len(message) < 20 {
			message = append(message, l)
		}
	}
	if len(message) > 0 && strings.HasPrefix(message[0], "panic: ") {
		t.Kind = Panic
	}
	t.Message = strings.Join(message, "\n")
	var g *Goroutine
	for ; i < len(lines); i++ {
		l := lines[i]
		switch {
		case goroutineLine.MatchString(l):
			t.Goroutines = append(t.Goroutines, Goroutine{Header: strings.TrimSuffix(l, ":")})
			g = &t.Goroutines[len(t.Goroutines)-1]
		case g == nil || strings.TrimSpace(l) == "":
		case strings.HasPrefix(l, "\t"):
			if m := fileLine.FindStringSubmatch(l); m != nil && len(g.Frames) > 0 {
				f := &g.Frames[len(g.Frames)-1]
				f.File = m[1]
				f.Line, _ = strconv.Atoi(m[2])
			}
		default:
			g.Frames = append(g.Frames, Frame{Function: l})
		}
	}
	return t
}

// trimFunction is a function name without its arguments ("pkg.(*T).M(0x1,
// …)" → "pkg.(*T).M") and closure numbers, so the same place reads the
// same in every build.
func trimFunction(f string) string {
	f = strings.TrimPrefix(strings.TrimSpace(f), "created by ")
	if i := strings.Index(f, " in goroutine"); i > 0 {
		f = f[:i]
	}
	if strings.HasSuffix(f, ")") {
		if i := strings.LastIndex(f, "("); i > 0 && !strings.HasSuffix(f[:i], ".") {
			f = f[:i]
		}
	}
	// Closures: ".func1", ".func1.2", ".gowrap1".
	parts := strings.Split(f, ".")
	for len(parts) > 1 {
		last := parts[len(parts)-1]
		if strings.HasPrefix(last, "func") || strings.HasPrefix(last, "gowrap") || isDigits(last) {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	return strings.Join(parts, ".")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Module is the program's module, whose functions are its own.
const Module = "github.com/vaisah"

// ownCode reports a function of the program's own: its module's packages
// or main.
func ownCode(f string) bool {
	return strings.HasPrefix(f, Module+"/") || strings.HasPrefix(f, "main.")
}

// machinery is code that is only how a crash happens (the runtime, panics,
// the crash reporter).
func machinery(f string) bool {
	if f == "panic" || strings.HasPrefix(f, Module+"/internal/crash.") {
		return true
	}
	for _, m := range []string{"runtime.", "runtime/", "internal/", "sync.", "testing."} {
		if strings.HasPrefix(f, m) {
			return true
		}
	}
	return false
}

// Signature is a short, stable name for the problem, the same for every
// report of it (it finds the issue already filed for it): the kind and the
// first of the program's own functions on the crashed goroutine's stack,
// past the runtime's ("panic in github.com/vaisah/internal/engine.(*Manager).run").
func (t Traceback) Signature() string {
	place := ""
	if len(t.Goroutines) > 0 {
		other := ""
		for _, f := range t.Goroutines[0].Frames {
			name := trimFunction(f.Function)
			if machinery(name) {
				continue
			}
			if other == "" {
				other = name
			}
			if ownCode(name) {
				place = name
				break
			}
		}
		if place == "" {
			place = other
		}
	}
	if place == "" {
		place = "unknown"
		if first, _, _ := strings.Cut(t.Message, "\n"); first != "" {
			place = strings.TrimPrefix(strings.TrimPrefix(first, "panic: "), "fatal error: ")
			if len(place) > 80 {
				place = place[:80]
			}
		}
	}
	return string(t.Kind) + " in " + place
}

// Description is the kind in a sentence.
func (k Kind) Description() string {
	if k == Panic {
		return "Yellow Flash hit an internal error and closed"
	}
	return "Yellow Flash crashed"
}

// sorry is one line that changes with each report (Minecraft's has its
// jokes; ours stay kind).
var sorry = []string{
	"Sorry about that.",
	"That wasn't supposed to happen.",
	"Even download managers have bad days.",
	"This one is on us.",
	"Thank you for sending this; it helps.",
}

// System is what the report says of the system and the app.
type System struct {
	App, Version, OS, Arch, GoVersion string
	CPUs                              int
}

// ThisSystem is this system's, for the app at version.
func ThisSystem(app, version string) System {
	return System{App: app, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(),
		CPUs: runtime.NumCPU()}
}

// shortPath is a source path from where it means something: the module's
// own (internal/engine/worker.go), a dependency's under the module cache,
// the standard library's from src/.
func shortPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	for _, mark := range []string{"/pkg/mod/", "/internal/", "/cmd/", "/src/"} {
		if i := strings.Index(p, mark); i >= 0 {
			return p[i+1:]
		}
	}
	return filepath.Base(p)
}

// Text is a report's text, the home folder shown as "~" (home "" leaves
// paths as they are).
func Text(t Traceback, time string, sys System, logTail []string, home string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "---- %s crash report ----\n", sys.App)
	n := 0
	for _, b := range []byte(time) {
		n += int(b)
	}
	fmt.Fprintf(&s, "// %s\n\n", sorry[n%len(sorry)])
	fmt.Fprintf(&s, "Time: %s\nDescription: %s\nSignature: %s\n\n", time, t.Kind.Description(), t.Signature())
	switch {
	case t.Message != "":
		fmt.Fprintf(&s, "%s\n\n", t.Message)
	case t.Kind == Crash:
		// The runtime writes a fatal error's own line to stderr only.
		s.WriteString("A fatal runtime error: its message wasn't kept; the stack below says where.\n\n")
	}
	if len(t.Goroutines) > 0 {
		g := t.Goroutines[0]
		fmt.Fprintf(&s, "-- The goroutine that crashed: %s --\n", g.Header)
		writeFrames(&s, g.Frames, len(g.Frames))
		s.WriteString("\n")
	}
	fmt.Fprintf(&s, "-- System --\n%s %s\nSystem: %s %s, %d CPUs\nBuilt with: %s\n\n", sys.App, sys.Version, sys.OS, sys.Arch,
		sys.CPUs, sys.GoVersion)
	if len(logTail) > 0 {
		fmt.Fprintf(&s, "-- The log's last %d lines --\n%s\n\n", len(logTail), strings.Join(logTail, "\n"))
	}
	if len(t.Goroutines) > 1 {
		fmt.Fprintf(&s, "-- Every other goroutine (%d) --\n", len(t.Goroutines)-1)
		for _, g := range t.Goroutines[1:] {
			fmt.Fprintf(&s, "%s\n", g.Header)
			writeFrames(&s, g.Frames, 12)
		}
	}
	return Scrub(s.String(), home)
}

func writeFrames(s *strings.Builder, frames []Frame, max int) {
	for i, f := range frames {
		if i == max {
			fmt.Fprintf(s, "     … %d more\n", len(frames)-max)
			break
		}
		fmt.Fprintf(s, "%3d: %s\n", i, trimFunction(f.Function))
		if f.File != "" {
			fmt.Fprintf(s, "       at %s:%d\n", shortPath(f.File), f.Line)
		}
	}
}

// Scrub shows the home folder as "~" (in paths of either kind of separator).
func Scrub(text, home string) string {
	if len(home) < 3 {
		return text
	}
	out := strings.ReplaceAll(text, home, "~")
	if flipped := strings.ReplaceAll(home, `\`, "/"); flipped != home {
		out = strings.ReplaceAll(out, flipped, "~")
	}
	return out
}
