package crash

// Catching crashes: the runtime writes a dying process's crash output
// (debug.SetCrashOutput) to <data>/crashes/pending.log; the next start
// makes it a report, <data>/crashes/<kind>-<time>.txt, and the raw output
// is kept beside it as .log.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// Dir is the crash reports' folder in a data folder.
func Dir(data string) string { return filepath.Join(data, "crashes") }

func pendingPath(data string) string { return filepath.Join(Dir(data), "pending.log") }

// Install makes the runtime write a crash's output for the next start to
// report. Call it once, early, after Collect; the returned file stays open
// for the process's life.
func Install(data string) (*os.File, error) {
	if err := os.MkdirAll(Dir(data), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(pendingPath(data), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Report is a crash report made.
type Report struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Signature string    `json:"signature"`
	Time      time.Time `json:"time"`
	// Path is the report's file; Text its words.
	Path string `json:"path"`
	Text string `json:"text"`
}

// Collect turns the last run's crash output, when there is one, into a
// report (with the log's last lines and sys); nil when the last run ended
// without a crash.
func Collect(data string, sys System) (*Report, error) {
	pending := pendingPath(data)
	fi, err := os.Stat(pending)
	if errors.Is(err, os.ErrNotExist) || (err == nil && fi.Size() == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(pending)
	if err != nil {
		return nil, err
	}
	at := fi.ModTime().UTC()
	t := Parse(string(b))
	home, _ := os.UserHomeDir()
	text := Text(t, at.Format(time.RFC3339), sys, Tail(data, 150), home)
	id := string(t.Kind) + "-" + at.Format("20060102-150405")
	path := filepath.Join(Dir(data), id+".txt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return nil, err
	}
	// The raw output beside it, scrubbed too; the next run's starts empty.
	_ = os.WriteFile(filepath.Join(Dir(data), id+".log"), []byte(Scrub(string(b), home)), 0o600)
	if err := os.Remove(pending); err != nil {
		return nil, err
	}
	return &Report{ID: id, Kind: t.Kind, Signature: t.Signature(), Time: at, Path: path, Text: text}, nil
}

// List is the reports kept, newest first.
func List(data string) []Report {
	entries, _ := os.ReadDir(Dir(data))
	var out []Report
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".txt")
		if !ok || e.IsDir() {
			continue
		}
		path := filepath.Join(Dir(data), e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		r := Report{ID: id, Path: path, Text: string(b)}
		for _, l := range strings.SplitN(r.Text, "\n", 12) {
			switch {
			case strings.HasPrefix(l, "Time: "):
				r.Time, _ = time.Parse(time.RFC3339, strings.TrimPrefix(l, "Time: "))
			case strings.HasPrefix(l, "Signature: "):
				r.Signature = strings.TrimPrefix(l, "Signature: ")
				k, _, _ := strings.Cut(r.Signature, " ")
				r.Kind = Kind(k)
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// Remove deletes a report (and its raw output).
func Remove(data, id string) error {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return errors.New("no such report")
	}
	err := os.Remove(filepath.Join(Dir(data), id+".txt"))
	_ = os.Remove(filepath.Join(Dir(data), id+".log"))
	return err
}
