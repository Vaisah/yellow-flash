// Ported from Mister Download Manager, crates/engine/src/unlock.rs (ServiceFault)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServiceRefusalsSayWhyAndWhetherToWait(t *testing.T) {
	e := &ServiceError{Service: "Real-Debrid", Fault: FaultNotPremium}
	if e.Error() != "Real-Debrid: this needs a premium account" || e.RetryWait() != 0 {
		t.Error(e.Error(), e.RetryWait())
	}
	waits := map[Fault]time.Duration{FaultTooManyRequests: time.Minute, FaultHostDown: 5 * time.Minute,
		FaultServiceDown: 5 * time.Minute, FaultTooManyActive: 2 * time.Minute, FaultSignIn: 0, FaultFileUnavailable: 0}
	for f, want := range waits {
		if got := (&ServiceError{Fault: f}).RetryWait(); got != want {
			t.Errorf("%s: %v, want %v", f, got, want)
		}
	}
	if w := (&ServiceError{Fault: FaultTooManyRequests, Wait: 30 * time.Second}).RetryWait(); w != 30*time.Second {
		t.Error("the service's own wait", w)
	}
	if s := FaultOther.Describe("its words"); s != "its words" {
		t.Error(s)
	}
	if s := FaultUnsafeName.Describe("../x"); !strings.HasSuffix(s, "../x") {
		t.Error(s)
	}
	// Retried when a wait within the policy may help.
	wrapped := fmt.Errorf("metadata: %w", &ServiceError{Fault: FaultTooManyRequests})
	if c := Classify(wrapped); c.Kind != KindService || !c.Retryable || c.RetryAfter != time.Minute {
		t.Errorf("%+v", c)
	}
	if c := Classify(&ServiceError{Fault: FaultHostDown}); DefaultRetryPolicy().ShouldRetry(0, c) {
		t.Error("five minutes is beyond the policy's longest wait")
	}
	if c := Classify(&ServiceError{Fault: FaultSignIn}); c.Retryable {
		t.Error("signing in again is the user's")
	}
}

func TestADirectLinkLeavesNoTraceInErrors(t *testing.T) {
	err := fmt.Errorf("segment: %w", &url.Error{Op: "Get", URL: "https://cdn.test/d/SECRETSIG", Err: errors.New("EOF")})
	if s := hideLink(err, "https://cdn.test/d/SECRETSIG?x").Error(); strings.Contains(s, "SECRETSIG") || !strings.Contains(s, theLink) {
		t.Error(s)
	}
	plain := errors.New("x")
	if hideLink(plain, "https://cdn.test/") != plain {
		t.Error("other errors are untouched")
	}
}
