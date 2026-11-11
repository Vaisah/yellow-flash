// Ported from Mister Download Manager, crates/debrid/src/lib.rs (mod signin)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

// Sign-in by code: Real-Debrid's device flow, AllDebrid's PIN. The code to
// show and the page to type it at, then a poll until the user allowed
// Yellow Flash there.

import (
	"context"
	"time"

	"github.com/vaisah/internal/engine"
)

// Code is a sign-in code waiting for the user. Its inner codes are secrets
// while it waits: it is kept in memory only.
type Code struct {
	Service Kind `json:"service"`
	// Shown is what the user types.
	Shown string `json:"shown"`
	// Page is where (AllDebrid's has the PIN in it already).
	Page string `json:"page"`
	// Interval is how often to ask; Expires is when the code dies.
	Interval time.Duration `json:"interval"`
	Expires  time.Time     `json:"expires"`

	rd *rdDeviceCode
	ad *adPinAnswer
}

// String says the service and the code shown, nothing secret.
func (c *Code) String() string { return "Code(" + c.Service.Name() + ", " + c.Shown + ")" }

// StartSignIn is step 1: a code from kind (one that signs in by code).
func StartSignIn(ctx context.Context, c *Client, kind Kind) (*Code, error) {
	switch kind {
	case RealDebrid:
		d, err := rdStartDevice(ctx, c)
		if err != nil {
			return nil, named(kind, err)
		}
		return &Code{Service: kind, Shown: d.UserCode, Page: d.VerificationURL,
			Interval: time.Duration(max(d.Interval, 1)) * time.Second,
			Expires:  time.Now().Add(time.Duration(d.ExpiresIn) * time.Second), rd: &d}, nil
	case AllDebrid:
		p, err := adPin(ctx, c)
		if err != nil {
			return nil, named(kind, err)
		}
		// The docs: ask every 5 seconds or more.
		return &Code{Service: kind, Shown: p.Pin, Page: p.UserURL, Interval: 5 * time.Second,
			Expires: time.Now().Add(time.Duration(p.ExpiresIn) * time.Second), ad: &p}, nil
	}
	return nil, &engine.ServiceError{Service: kind.Name(), Fault: engine.FaultOther,
		Detail: kind.Name() + " signs in with its API key"}
}

// PollSignIn is step 2, every Interval until Expires: the credential once
// allowed, nil while waiting.
func PollSignIn(ctx context.Context, c *Client, code *Code) (*Credential, error) {
	switch {
	case code.rd != nil:
		cr, err := rdDeviceCredentials(ctx, c, code.rd.DeviceCode)
		if err != nil || cr == nil {
			return nil, named(code.Service, err)
		}
		t, err := rdToken(ctx, c, cr.ClientID, cr.ClientSecret, code.rd.DeviceCode)
		if err != nil {
			return nil, named(code.Service, err)
		}
		return &Credential{Kind: "oauth", ClientID: cr.ClientID, ClientSecret: cr.ClientSecret,
			RefreshToken: t.RefreshToken}, nil
	case code.ad != nil:
		key, err := adPinCheck(ctx, c, *code.ad)
		if err != nil || key == "" {
			return nil, named(code.Service, err)
		}
		cr := Token(key)
		return &cr, nil
	}
	return nil, other("not a sign-in code")
}
