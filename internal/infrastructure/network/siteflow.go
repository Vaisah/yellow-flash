// Ported from Mister Download Manager, crates/engine/src/download.rs (the sites hooks in `get`)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/vaisah/internal/infrastructure/network/sites"
)

// ErrSitePage means a site answered its file address with one of its own
// pages that leads nowhere known: the site talking, never the file.
var ErrSitePage = errors.New("the site answered with a web page, not the file")

// siteURL returns the address to ask for rawURL: a site's link to a file
// is rewritten to the file's own address (a Google Drive viewer link → its
// download address). A link that names no file (a Drive folder) is refused.
func siteURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, nil
	}
	if r := sites.NotAFile(u); r != nil {
		return "", r
	}
	if next, ok := sites.Rewrite(u); ok {
		return next.String(), nil
	}
	return rawURL, nil
}

// siteGet sends a GET for rawURL and follows a site's own pages on the way
// to the file: a confirmation form ("can't scan this file for viruses"), a
// confirmed link in the page, or the address in its data. A site's error
// page, sign-in redirect or 401/403 becomes a *sites.Refusal, which is not
// retried. Any other answer is returned as it is, its body intact.
func (p *Prober) siteGet(ctx context.Context, rawURL string, headers http.Header, rangeHeader string) (*http.Response, error) {
	target, err := siteURL(rawURL)
	if err != nil {
		return nil, err
	}
	looked := false
	for hop := 0; ; hop++ {
		asked, _ := url.Parse(target)
		res, err := p.Request(ctx, http.MethodGet, target, headers, rangeHeader)
		if err != nil {
			return nil, err
		}
		at := res.Request.URL
		if r := sites.RefusedBy(asked, at, res.StatusCode); r != nil {
			_ = res.Body.Close()
			return nil, r
		}
		if !sitePage(res) || hop >= sites.MaxPageHops {
			return res, nil
		}
		page, err := io.ReadAll(io.LimitReader(res.Body, sites.PagePeek))
		if err != nil {
			_ = res.Body.Close()
			return nil, err
		}
		says := sites.ReadPage(at, page)
		switch says.Action {
		case sites.Next:
			_ = res.Body.Close()
			target = says.Next.String()
			continue
		case sites.Refused:
			_ = res.Body.Close()
			// Once, before reporting it: a Google Doc's ID answered by
			// Drive's file host leads, from drive.google.com, to the Doc.
			if second, ok := sites.SecondLook(at); ok && !looked {
				looked = true
				if next, ok := p.secondLook(ctx, second.String(), headers, at); ok {
					target = next
					continue
				}
			}
			return nil, says.Refusal
		}
		if sites.ExpectsFile(at) && res.Header.Get("Content-Disposition") == "" {
			_ = res.Body.Close()
			return nil, fmt.Errorf("%w (%s)", ErrSitePage, at.Hostname())
		}
		// Not a page the site's rules know: hand it on whole.
		res.Body = readCloser{io.MultiReader(bytes.NewReader(page), res.Body), res.Body}
		return res, nil
	}
}

// secondLook asks second and returns the file address its answer leads to,
// when that differs from where the refusal came from.
func (p *Prober) secondLook(ctx context.Context, second string, headers http.Header, refusedAt *url.URL) (string, bool) {
	res, err := p.Request(ctx, http.MethodGet, second, headers, "")
	if err != nil {
		return "", false
	}
	_ = res.Body.Close()
	next, ok := sites.Rewrite(res.Request.URL)
	if !ok || next.String() == refusedAt.String() {
		return "", false
	}
	return next.String(), true
}

// sitePage reports a successful HTML answer from a host whose pages the
// sites package reads, small enough to read whole.
func sitePage(res *http.Response) bool {
	if res.StatusCode < 200 || res.StatusCode > 299 || !sites.IsSiteHost(res.Request.URL) {
		return false
	}
	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	mt = strings.ToLower(mt)
	return (mt == "text/html" || mt == "application/xhtml+xml") &&
		(res.ContentLength < 0 || res.ContentLength <= sites.PagePeek)
}

// keepCredentialsWithinParty restores the credential headers Go drops on a
// redirect to another host when both hosts are one site's (Google Drive's
// three: the browser sends its .google.com cookies to all of them). Never
// from https down to http, and never to any other host.
func keepCredentialsWithinParty(req *http.Request, via []*http.Request) {
	if len(via) == 0 {
		return
	}
	first, prev := via[0], via[len(via)-1]
	if !sites.SameParty(prev.URL, req.URL) || (prev.URL.Scheme == "https" && req.URL.Scheme != "https") {
		return
	}
	for _, name := range []string{"Cookie", "Authorization"} {
		if req.Header.Get(name) == "" {
			if v := first.Header.Values(name); len(v) > 0 {
				req.Header[name] = append([]string(nil), v...)
			}
		}
	}
}

type readCloser struct {
	io.Reader
	io.Closer
}
