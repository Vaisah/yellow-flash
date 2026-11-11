// Ported from Mister Download Manager, crates/debrid/src/alldebrid.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// AllDebrid's API v4 / v4.1, as documented at https://docs.alldebrid.com/
// (read 2026-11-05 for MDM). Only what is used: the account, the supported
// domains, unlocking a link (and its delayed flow), and sign-in by PIN.
//
//   - Every call carries "Authorization: Bearer <apikey>"; no agent
//     parameter (the changelog of 15/01/2025 removed it). POST bodies are
//     form fields.
//   - Every answer is an envelope: {"status":"success","data":{…}} or
//     {"status":"error","error":{"code":"…","message":"…"}}, decided by
//     status, not by the HTTP code (errors may come with 200; a 429 or 503
//     is too many requests).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/vaisah/internal/engine"
)

// adFault is what an error code means (the documentation's list of codes).
func adFault(code, message string) error {
	switch code {
	case "AUTH_MISSING_APIKEY", "AUTH_BAD_APIKEY", "AUTH_MISSING_AGENT", "AUTH_BAD_AGENT":
		return fault(engine.FaultSignIn)
	case "AUTH_BLOCKED":
		return fault(engine.FaultNeedsApproval)
	case "AUTH_USER_BANNED", "ACCOUNT_INVALID":
		return fault(engine.FaultAccountBlocked)
	case "MUST_BE_PREMIUM", "FREE_TRIAL_LIMIT_REACHED", "MAGNET_MUST_BE_PREMIUM":
		return fault(engine.FaultNotPremium)
	case "LINK_HOST_NOT_SUPPORTED", "LINK_NOT_SUPPORTED":
		return errUnsupported
	case "LINK_HOST_UNAVAILABLE", "LINK_TEMPORARY_UNAVAILABLE", "LINK_HOST_FULL":
		return fault(engine.FaultHostDown)
	case "LINK_HOST_LIMIT_REACHED", "MAGNET_TOO_MANY":
		return fault(engine.FaultLimitReached)
	case "LINK_TOO_MANY_DOWNLOADS", "MAGNET_TOO_MANY_ACTIVE":
		return fault(engine.FaultTooManyActive)
	case "LINK_DOWN", "MAGNET_INVALID_ID":
		return fault(engine.FaultFileUnavailable)
	case "LINK_PASS_PROTECTED":
		return fault(engine.FaultPasswordNeeded)
	case "MAGNET_NO_SERVER", "NO_SERVER":
		return fault(engine.FaultIPRefused)
	case "MAINTENANCE", "GENERIC", "DELAYED_INVALID_ID", "LINK_ERROR":
		return fault(engine.FaultServiceDown)
	}
	if message != "" {
		return other(message)
	}
	return other(code)
}

type adErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// adRead reads an answer: the envelope's data into out, or what its error
// means.
func adRead(a answer, out any) error {
	if a.status == http.StatusTooManyRequests || a.status == http.StatusServiceUnavailable {
		return &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: a.retryAfter}
	}
	var e struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  *adErrorBody    `json:"error"`
	}
	if err := json.Unmarshal(a.body, &e); err != nil {
		if a.status >= 500 {
			return fault(engine.FaultServiceDown)
		}
		return errUnreadable
	}
	switch {
	case e.Status == "success" && len(e.Data) > 0 && string(e.Data) != "null":
		return decode(e.Data, out)
	case e.Error != nil:
		return adFault(e.Error.Code, e.Error.Message)
	}
	return fault(engine.FaultServiceDown)
}

func adCall(ctx context.Context, c *Client, method, path string, form url.Values, key string, out any) error {
	a, err := c.send(ctx, AllDebrid, method, c.services.AD+path, form, key)
	if err != nil {
		return err
	}
	return adRead(a, out)
}

// adUserAnswer is GET v4/user's user.
type adUserAnswer struct {
	Username  string `json:"username"`
	IsPremium bool   `json:"isPremium"`
	// PremiumUntil is Unix time; 0 when not premium.
	PremiumUntil int64 `json:"premiumUntil"`
}

func adUser(ctx context.Context, c *Client, key string) (adUserAnswer, error) {
	var d struct {
		User adUserAnswer `json:"user"`
	}
	err := adCall(ctx, c, http.MethodGet, "v4/user", nil, key, &d)
	return d.User, err
}

// adDomains is GET v4/hosts/domains (public): the file hosts' domains,
// their other names included.
func adDomains(ctx context.Context, c *Client) ([]string, error) {
	var d struct {
		Hosts []string `json:"hosts"`
	}
	err := adCall(ctx, c, http.MethodGet, "v4/hosts/domains", nil, "", &d)
	return d.Hosts, err
}

// adUnlockAnswer is POST v4/link/unlock.
type adUnlockAnswer struct {
	Link     string `json:"link"`
	Filename string `json:"filename"`
	Filesize int64  `json:"filesize"`
	// Delayed is set when the link takes time to generate: poll
	// link/delayed with it.
	Delayed int64 `json:"delayed"`
}

func adUnlock(ctx context.Context, c *Client, key, link string) (adUnlockAnswer, error) {
	var u adUnlockAnswer
	err := adCall(ctx, c, http.MethodPost, "v4/link/unlock", url.Values{"link": {link}}, key, &u)
	return u, err
}

// adDelayedAnswer is POST v4/link/delayed: 1 still processing, 2 ready
// (Link), 3 failed.
type adDelayedAnswer struct {
	Status   int    `json:"status"`
	TimeLeft int64  `json:"time_left"`
	Link     string `json:"link"`
}

func adDelayed(ctx context.Context, c *Client, key string, id int64) (adDelayedAnswer, error) {
	var d adDelayedAnswer
	err := adCall(ctx, c, http.MethodPost, "v4/link/delayed", url.Values{"id": {strconv.FormatInt(id, 10)}}, key, &d)
	return d, err
}

// adPinAnswer is GET v4.1/pin/get: the PIN to type at UserURL, and the
// check token.
type adPinAnswer struct {
	Pin       string `json:"pin"`
	Check     string `json:"check"`
	ExpiresIn int64  `json:"expires_in"`
	UserURL   string `json:"user_url"`
}

func adPin(ctx context.Context, c *Client) (adPinAnswer, error) {
	var p adPinAnswer
	err := adCall(ctx, c, http.MethodGet, "v4.1/pin/get", nil, "", &p)
	return p, err
}

// adPinCheck is POST v4/pin/check: the apikey once the user typed the PIN,
// "" while waiting.
func adPinCheck(ctx context.Context, c *Client, p adPinAnswer) (string, error) {
	var r struct {
		Activated bool   `json:"activated"`
		Apikey    string `json:"apikey"`
	}
	if err := adCall(ctx, c, http.MethodPost, "v4/pin/check", url.Values{"check": {p.Check}, "pin": {p.Pin}}, "", &r); err != nil {
		return "", err
	}
	if !r.Activated {
		return "", nil
	}
	return r.Apikey, nil
}

// asService is errors.As for a service's refusal.
func asService(err error, se **engine.ServiceError) bool { return errors.As(err, se) }
