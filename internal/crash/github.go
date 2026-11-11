// Ported from Mister Download Manager, crates/crash/src/github.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package crash

// Reports on GitHub: a new issue on the Crash report form
// (.github/ISSUE_TEMPLATE/crash.yml) filled from the report, and a search
// for one already filed for the same problem. Nothing is sent by itself:
// the user opens the form in the browser, reads it and submits it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Repo is where reports go.
const Repo = "Vaisah/yellow-flash"

// URLBudget keeps the form's address under GitHub's limit: the report is
// cut to fit (the whole one goes in as the attached file).
const URLBudget = 7000

// Field is one of the form's fields the app fills: its id in crash.yml
// and its value.
type Field struct{ ID, Value string }

func enc(s string) string { return url.QueryEscape(s) }

// NewIssueURL is a new Crash report issue: fields (crash.yml's ids) and as
// much of report as fits, cut at a line, with a note that the rest is in
// the file to attach.
func NewIssueURL(title string, fields []Field, report, cutNote string) string {
	var u strings.Builder
	fmt.Fprintf(&u, "https://github.com/%s/issues/new?template=crash.yml&title=%s", Repo, enc(title))
	for _, f := range fields {
		fmt.Fprintf(&u, "&%s=%s", f.ID, enc(f.Value))
	}
	head := u.String() + "&report="
	room := URLBudget - len(head)
	if whole := enc(report); len(whole) <= room {
		return head + whole
	}
	note := "\n\n" + cutNote + "\n"
	room -= len(enc(note))
	var kept strings.Builder
	used := 0
	for _, line := range strings.Split(report, "\n") {
		l := len(enc(line + "\n"))
		if used+l > room {
			break
		}
		used += l
		kept.WriteString(line + "\n")
	}
	return head + enc(kept.String()+note)
}

// Found is an issue already filed.
type Found struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

// SearchURL is where GitHub's issue search answers (tests: a fake).
var SearchURL = "https://api.github.com/search/issues"

// FindIssue is the issue already filed for signature (its body holds it:
// the form's Signature field), open ones first; nil when there is none.
// Only the signature is sent.
func FindIssue(ctx context.Context, client *http.Client, signature string) (*Found, error) {
	q := fmt.Sprintf(`repo:%s is:issue in:body "%s"`, Repo, strings.ReplaceAll(signature, `"`, ""))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SearchURL+"?q="+enc(q)+"&sort=updated&per_page=5", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Yellow Flash crash reporter")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub's search answered %s", res.Status)
	}
	var s struct {
		Items []Found `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&s); err != nil {
		return nil, err
	}
	sort.SliceStable(s.Items, func(i, j int) bool { return s.Items[i].State == "open" && s.Items[j].State != "open" })
	if len(s.Items) == 0 {
		return nil, nil
	}
	return &s.Items[0], nil
}
