// Ported from Mister Download Manager, crates/debrid/src/premiumize.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// Premiumize.me's API, as documented at https://www.premiumize.me/api (read
// 2026-11-05 for MDM). Only what is used: the account, the supported
// services, and direct links (transfer/directdl).
//
//   - Every call carries "Authorization: Bearer <API key>" (the docs'
//     recommended way; never the legacy ?apikey=, which ends up in logs).
//   - Every answer has status: "success" with its fields, or "error" with
//     message and code, with HTTP 200 for business errors too; 500 is a
//     server failure.
//   - Sign-in by code (an OAuth device flow) needs a client registered by
//     the app's maintainer: until then, the user pastes the API key from
//     the Account page.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/vaisah/internal/engine"
)

// pmFault is what an error code means (the documentation's list of codes).
func pmFault(code, message string) error {
	switch code {
	case "authentication_failed":
		return fault(engine.FaultSignIn)
	case "permission_denied":
		return fault(engine.FaultAccountBlocked)
	case "service_unsupported":
		return errUnsupported
	case "service_down":
		return fault(engine.FaultHostDown)
	case "service_limit_reached", "account_limit_reached":
		return fault(engine.FaultLimitReached)
	case "rate_limit_reached":
		return fault(engine.FaultTooManyRequests)
	case "not_found":
		return fault(engine.FaultFileUnavailable)
	case "link_generation_failed", "transient_error", "unknown_error":
		return fault(engine.FaultServiceDown)
	}
	if message != "" {
		return other(message)
	}
	return other(code)
}

// pmRead reads an answer into out, or what its error means.
func pmRead(a answer, out any) error {
	if a.status == http.StatusTooManyRequests {
		return &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: a.retryAfter}
	}
	var s struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(a.body, &s); err != nil {
		if a.status >= 500 {
			return fault(engine.FaultServiceDown)
		}
		return errUnreadable
	}
	if s.Status != "success" {
		return pmFault(s.Code, s.Message)
	}
	return decode(a.body, out)
}

func pmCall(ctx context.Context, c *Client, method, path string, form url.Values, key string, out any) error {
	a, err := c.send(ctx, Premiumize, method, c.services.PM+path, form, key)
	if err != nil {
		return err
	}
	err = pmRead(a, out)
	c.pause(Premiumize, err)
	return err
}

// pmAccountAnswer is GET account/info.
type pmAccountAnswer struct {
	CustomerID json.RawMessage `json:"customer_id"`
	// PremiumUntil is Unix time; null for a free account.
	PremiumUntil *int64 `json:"premium_until"`
}

// customerID is the customer id, a string or a number.
func (a pmAccountAnswer) customerID() string {
	var s string
	if json.Unmarshal(a.CustomerID, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(a.CustomerID, &n) == nil {
		return n.String()
	}
	return ""
}

func pmAccount(ctx context.Context, c *Client, key string) (pmAccountAnswer, error) {
	var a pmAccountAnswer
	err := pmCall(ctx, c, http.MethodGet, "account/info", nil, key, &a)
	return a, err
}

// pmServices is the part of GET services/list used.
type pmServices struct {
	DirectDL []string            `json:"directdl"`
	Aliases  map[string][]string `json:"aliases"`
}

// domains is the domains of the services transfer/directdl takes: each
// service's aliases, and its own name when that is a domain.
func (s pmServices) domains() []string {
	var out []string
	for _, svc := range s.DirectDL {
		if strings.Contains(svc, ".") {
			out = append(out, svc)
		}
		out = append(out, s.Aliases[svc]...)
	}
	return out
}

func pmDomains(ctx context.Context, c *Client, key string) ([]string, error) {
	var s pmServices
	if err := pmCall(ctx, c, http.MethodGet, "services/list", nil, key, &s); err != nil {
		return nil, err
	}
	return s.domains(), nil
}

// pmDirectAnswer is POST transfer/directdl: one entry per file of the source.
type pmDirectAnswer struct {
	Content []pmEntry `json:"content"`
}

type pmEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Link string `json:"link"`
}

func pmDirect(ctx context.Context, c *Client, key, link string) (pmDirectAnswer, error) {
	var d pmDirectAnswer
	err := pmCall(ctx, c, http.MethodPost, "transfer/directdl", url.Values{"src": {link}}, key, &d)
	return d, err
}
