// Ported from Mister Download Manager, crates/debrid/src/realdebrid.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// Real-Debrid's REST API 1.0 and its OAuth v2 device flow, as documented at
// https://api.real-debrid.com/ (read 2026-11-04 for MDM). Only what is
// used: the account, the supported hosts, unrestricting a link, and signing
// in.
//
//   - Every call carries "Authorization: Bearer <token>" (never
//     ?auth_token=, which ends up in logs).
//   - Errors are HTTP 4xx/5xx with {"error": "...", "error_code": N}.
//   - remote is always 0: Yellow Flash is a desktop program, and remote
//     traffic is for servers.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/vaisah/internal/engine"
)

// rdOpenSourceClientID is the client id Real-Debrid gives open-source
// programs for the device flow ("Opensource Apps").
const rdOpenSourceClientID = "X245A4XAIBGVM"

// rdDeviceGrant is the grant the device flow and refreshes use.
const rdDeviceGrant = "http://oauth.net/grant_type/device/1.0"

// rdUser is GET /user.
type rdUser struct {
	Username string `json:"username"`
	// Type is "premium" or "free".
	Type string `json:"type"`
	// Premium is the premium seconds left.
	Premium int64 `json:"premium"`
}

// rdUnrestricted is POST /unrestrict/link.
type rdUnrestricted struct {
	Filename string `json:"filename"`
	// Filesize is 0 when unknown.
	Filesize int64 `json:"filesize"`
	// Chunks is the "Max Chunks allowed".
	Chunks int `json:"chunks"`
	// Download is the direct link.
	Download string `json:"download"`
}

// rdDeviceCode is GET oauth/v2/device/code.
type rdDeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	Interval        int64  `json:"interval"`
	ExpiresIn       int64  `json:"expires_in"`
	VerificationURL string `json:"verification_url"`
}

// rdCredentials is GET oauth/v2/device/credentials: the user-bound client,
// once the user allowed Yellow Flash.
type rdCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// rdTokenAnswer is POST oauth/v2/token.
type rdTokenAnswer struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// rdFault is what an error answer means (the documentation's list of codes).
func rdFault(a answer) error {
	var e struct {
		Error     string `json:"error"`
		ErrorCode *int   `json:"error_code"`
	}
	_ = json.Unmarshal(a.body, &e)
	const none = -1000
	code := none
	if e.ErrorCode != nil {
		code = *e.ErrorCode
	}
	switch {
	case code == 8 || (code == none && a.status == 401):
		return fault(engine.FaultSignIn)
	case code == 9 || code == 14 || code == 15:
		return fault(engine.FaultAccountBlocked)
	case code == 20:
		return fault(engine.FaultNotPremium)
	case code == 16:
		return errUnsupported
	case code == 17 || code == 19:
		return fault(engine.FaultHostDown)
	case code == 18 || code == 23 || code == 36:
		return fault(engine.FaultLimitReached)
	case code == 21:
		return fault(engine.FaultTooManyActive)
	case code == 22:
		return fault(engine.FaultIPRefused)
	case code == 24 || (code == none && a.status == 503):
		return fault(engine.FaultFileUnavailable)
	case code == 28 || code == 35:
		return fault(engine.FaultFileRefused)
	case code == 5 || code == 34 || (code == none && a.status == 429):
		return &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: a.retryAfter}
	case code == 25 || code == -1:
		return fault(engine.FaultServiceDown)
	case code == none && a.status == 403:
		return fault(engine.FaultAccountBlocked)
	case code == none && a.status >= 500:
		return fault(engine.FaultServiceDown)
	case e.Error != "":
		return other(e.Error)
	}
	return other("HTTP " + http.StatusText(a.status))
}

// rdCall sends a request to Real-Debrid and reads its JSON answer into out
// (nil: no body, as 204); an error answer through rdFault.
func rdCall(ctx context.Context, c *Client, method, rawURL string, form url.Values, token string, out any) error {
	a, err := c.send(ctx, RealDebrid, method, rawURL, form, token)
	if err != nil {
		return err
	}
	if a.status >= 200 && a.status < 300 {
		if out == nil {
			return nil
		}
		return decode(a.body, out)
	}
	err = rdFault(a)
	c.pause(RealDebrid, err)
	return err
}

// rdGetUser is the account: name, premium or not.
func rdGetUser(ctx context.Context, c *Client, token string) (rdUser, error) {
	var u rdUser
	err := rdCall(ctx, c, http.MethodGet, c.services.RDRest+"user", nil, token, &u)
	return u, err
}

// rdDomains is the domains Real-Debrid supports (no sign-in needed).
func rdDomains(ctx context.Context, c *Client) ([]string, error) {
	var d []string
	err := rdCall(ctx, c, http.MethodGet, c.services.RDRest+"hosts/domains", nil, "", &d)
	return d, err
}

// rdUnrestrict is today's direct link for link.
func rdUnrestrict(ctx context.Context, c *Client, token, link string) (rdUnrestricted, error) {
	var u rdUnrestricted
	form := url.Values{"link": {link}, "remote": {"0"}}
	err := rdCall(ctx, c, http.MethodPost, c.services.RDRest+"unrestrict/link", form, token, &u)
	return u, err
}

// rdStartDevice is sign-in by code, step 1: the code the user types at
// Real-Debrid.
func rdStartDevice(ctx context.Context, c *Client) (rdDeviceCode, error) {
	q := url.Values{"client_id": {rdOpenSourceClientID}, "new_credentials": {"yes"}}
	var d rdDeviceCode
	err := rdCall(ctx, c, http.MethodGet, c.services.RDOAuth+"device/code?"+q.Encode(), nil, "", &d)
	return d, err
}

// rdDeviceCredentials is step 2, asked every interval: the user-bound
// client once the user allowed Yellow Flash, nil while waiting.
func rdDeviceCredentials(ctx context.Context, c *Client, deviceCode string) (*rdCredentials, error) {
	q := url.Values{"client_id": {rdOpenSourceClientID}, "code": {deviceCode}}
	var cr rdCredentials
	err := rdCall(ctx, c, http.MethodGet, c.services.RDOAuth+"device/credentials?"+q.Encode(), nil, "", &cr)
	if err == nil {
		return &cr, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Not allowed yet: Real-Debrid answers with an error until then (its
	// wording isn't documented); a service that is down or asks to slow
	// down is said as such.
	var se *engine.ServiceError
	if asService(err, &se) && (se.Fault == engine.FaultServiceDown || se.Fault == engine.FaultTooManyRequests) {
		return nil, fault(engine.FaultServiceDown)
	}
	return nil, nil
}

// rdToken is step 3, and every refresh: an access token for the user-bound
// client. code is the device code the first time, then the refresh token.
func rdToken(ctx context.Context, c *Client, clientID, clientSecret, code string) (rdTokenAnswer, error) {
	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret}, "code": {code},
		"grant_type": {rdDeviceGrant}}
	var t rdTokenAnswer
	err := rdCall(ctx, c, http.MethodPost, c.services.RDOAuth+"token", form, "", &t)
	return t, err
}
