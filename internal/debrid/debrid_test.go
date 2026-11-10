// Ported from Mister Download Manager, crates/debrid/src/{lib,realdebrid,alldebrid,premiumize}.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package debrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/vaisah/internal/engine"
)

func faultOf(t *testing.T, err error) engine.Fault {
	t.Helper()
	var se *engine.ServiceError
	if !errors.As(err, &se) {
		t.Fatalf("not a service's refusal: %v", err)
	}
	return se.Fault
}

func TestHostsMatchTheirDomainsAndSubdomainsOnly(t *testing.T) {
	d := []string{"rapidgator.net", "WWW.1fichier.com"}
	for _, yes := range []string{"rapidgator.net", "www.rapidgator.net", "RG.rapidgator.NET", "1fichier.com", "a.1fichier.com"} {
		if !HostMatches(yes, d) {
			t.Error(yes)
		}
	}
	for _, no := range []string{"notrapidgator.net", "rapidgator.net.evil.com", "fichier.com", ""} {
		if HostMatches(no, d) {
			t.Error(no)
		}
	}
}

func TestACredentialNeverPrintsItsSecret(t *testing.T) {
	c := Credential{Kind: "oauth", ClientID: "cid", ClientSecret: "SECRET", RefreshToken: "REFRESH"}
	tok := Token("TOKEN")
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("x", "c", c, "a", Account{ID: "1", Credential: tok})
	shown := fmt.Sprintf("%v %+v %#v %s %v %+v", c, c, c, tok, Account{Credential: tok}, Account{Credential: c}) + logged.String()
	for _, secret := range []string{"SECRET", "REFRESH", "TOKEN"} {
		if strings.Contains(shown, secret) {
			t.Errorf("%s in %s", secret, shown)
		}
	}
	b, _ := json.Marshal(c)
	var back Credential
	if json.Unmarshal(b, &back) != nil || back != c || !strings.Contains(string(b), `"kind":"oauth"`) {
		t.Errorf("%s", b)
	}
}

func TestRealDebridsDocumentedErrorsMeanWhatTheySay(t *testing.T) {
	f := func(status int, body string) error { return rdFault(answer{status: status, body: []byte(body)}) }
	cases := []struct {
		status int
		body   string
		want   engine.Fault
	}{
		{401, `{"error":"bad_token","error_code":8}`, engine.FaultSignIn},
		{403, `{"error":"x","error_code":20}`, engine.FaultNotPremium},
		{503, `{"error":"unavailable_file","error_code":24}`, engine.FaultFileUnavailable},
		{503, `{"error":"hoster_unavailable","error_code":19}`, engine.FaultHostDown},
		{403, `{"error":"traffic_exhausted","error_code":23}`, engine.FaultLimitReached},
		{403, `{"error":"ip_not_allowed","error_code":22}`, engine.FaultIPRefused},
		{500, "not json", engine.FaultServiceDown},
		{400, `{"error":"something new"}`, engine.FaultOther},
	}
	for _, c := range cases {
		if got := faultOf(t, f(c.status, c.body)); got != c.want {
			t.Errorf("%d %s: %s, want %s", c.status, c.body, got, c.want)
		}
	}
	if !errors.Is(f(503, `{"error":"hoster_unsupported","error_code":16}`), errUnsupported) {
		t.Error("16 is an unsupported host")
	}
	var se *engine.ServiceError
	err := rdFault(answer{status: 429, body: []byte(`{"error":"too_many_requests","error_code":34}`), retryAfter: 30 * time.Second})
	if !errors.As(err, &se) || se.Fault != engine.FaultTooManyRequests || se.Wait != 30*time.Second {
		t.Error(err)
	}
	if !errors.As(f(400, `{"error":"something new"}`), &se) || se.Detail != "something new" {
		t.Error("in the service's words")
	}
}

func TestTheDocumentedUnrestrictAnswerReads(t *testing.T) {
	body := `{"id":"ABC","filename":"x.rar","mimeType":"application/x-rar","filesize":1048576,
		"link":"https://host/file/x","host":"host.com","chunks":16,"crc":1,
		"download":"https://abc.download.real-debrid.com/d/ABC/x.rar","streamable":0}`
	var u rdUnrestricted
	if err := decode([]byte(body), &u); err != nil || u.Filename != "x.rar" || u.Filesize != 1048576 || u.Chunks != 16 ||
		!strings.HasPrefix(u.Download, "https://abc.download") {
		t.Error(u, err)
	}
}

func TestAllDebridsDocumentedEnvelopesRead(t *testing.T) {
	ok := `{"status":"success","data":{"link":"http://ombfyx.debrid.it/dl/abcdefgh12/somefile.txt",
		"host":"example","filename":"somefile.txt","paws":false,"filesize":699400192,"streams":[],
		"id":"abcdefgh12","hostDomain":"example.com"}}`
	var u adUnlockAnswer
	if err := adRead(answer{status: 200, body: []byte(ok)}, &u); err != nil || u.Filename != "somefile.txt" ||
		u.Filesize != 699400192 || u.Delayed != 0 {
		t.Error(u, err)
	}
	var d adDelayedAnswer
	if err := adRead(answer{status: 200, body: []byte(`{"status":"success","data":{"status":1,"time_left":45}}`)}, &d); err != nil ||
		d.Status != 1 || d.TimeLeft != 45 || d.Link != "" {
		t.Error(d, err)
	}
	bad := `{"status":"error","error":{"code":"LINK_DOWN","message":"This link is not available"}}`
	if got := faultOf(t, adRead(answer{status: 200, body: []byte(bad)}, &u)); got != engine.FaultFileUnavailable {
		t.Error(got)
	}
	if got := faultOf(t, adRead(answer{status: 429, retryAfter: 10 * time.Second}, &u)); got != engine.FaultTooManyRequests {
		t.Error(got)
	}
}

func TestAllDebridsDocumentedCodesMeanWhatTheySay(t *testing.T) {
	want := map[string]engine.Fault{
		"AUTH_BAD_APIKEY": engine.FaultSignIn, "AUTH_BLOCKED": engine.FaultNeedsApproval,
		"LINK_HOST_LIMIT_REACHED": engine.FaultLimitReached, "LINK_PASS_PROTECTED": engine.FaultPasswordNeeded,
		"NO_SERVER": engine.FaultIPRefused, "MUST_BE_PREMIUM": engine.FaultNotPremium, "SOMETHING_NEW": engine.FaultOther,
	}
	for code, f := range want {
		if got := faultOf(t, adFault(code, "")); got != f {
			t.Errorf("%s: %s, want %s", code, got, f)
		}
	}
	if !errors.Is(adFault("LINK_HOST_NOT_SUPPORTED", ""), errUnsupported) {
		t.Error("an unsupported host")
	}
	var se *engine.ServiceError
	if !errors.As(adFault("SOMETHING_NEW", "Its words"), &se) || se.Detail != "Its words" {
		t.Error("in the service's words")
	}
}

func TestPremiumizesDocumentedAnswersRead(t *testing.T) {
	var d pmDirectAnswer
	body := `{"status":"success","content":[{"path":"Folder/video1.mkv","size":123456789,"link":"https://x/1"}]}`
	if err := pmRead(answer{status: 200, body: []byte(body)}, &d); err != nil || d.Content[0].Path != "Folder/video1.mkv" ||
		d.Content[0].Size != 123456789 {
		t.Error(d, err)
	}
	var a pmAccountAnswer
	if err := pmRead(answer{status: 200, body: []byte(`{"status":"success","customer_id":"1234567","premium_until":null,"limit_used":0.42}`)}, &a); err != nil ||
		a.PremiumUntil != nil || a.customerID() != "1234567" {
		t.Error(a, err)
	}
	if err := pmRead(answer{status: 200, body: []byte(`{"status":"success","customer_id":7654321}`)}, &a); err != nil || a.customerID() != "7654321" {
		t.Error("a numeric customer id", err)
	}
	var s pmServices
	_ = json.Unmarshal([]byte(`{"status":"success","directdl":["exampleservice1","host.com"],
		"aliases":{"exampleservice1":["example1.alt"],"host.com":["host.net"]}}`), &s)
	if got := strings.Join(s.domains(), ","); got != "example1.alt,host.com,host.net" {
		t.Error(got)
	}
	if got := faultOf(t, pmRead(answer{status: 200, body: []byte(`{"status":"error","message":"Not found","code":"not_found"}`)}, &d)); got != engine.FaultFileUnavailable {
		t.Error(got)
	}
}

func TestPremiumizesDocumentedCodesMeanWhatTheySay(t *testing.T) {
	want := map[string]engine.Fault{
		"authentication_failed": engine.FaultSignIn, "service_down": engine.FaultHostDown,
		"account_limit_reached": engine.FaultLimitReached, "rate_limit_reached": engine.FaultTooManyRequests,
		"new_code": engine.FaultOther,
	}
	for code, f := range want {
		if got := faultOf(t, pmFault(code, "")); got != f {
			t.Errorf("%s: %s, want %s", code, got, f)
		}
	}
	if !errors.Is(pmFault("service_unsupported", ""), errUnsupported) {
		t.Error("an unsupported host")
	}
}

func TestIDsThatGoIntoAddressesArePlain(t *testing.T) {
	for _, ok := range []string{"ABC123", "a-b_c"} {
		if _, err := plainID(ok); err != nil {
			t.Error(ok, err)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "a?b=c", "a b"} {
		if _, err := plainID(bad); err == nil {
			t.Error(bad)
		}
	}
}

// A service that said "too many requests" gets nothing more until its wait
// is over; each service's budget counts what was sent to it.
func TestABudgetPausesAfterTooManyRequests(t *testing.T) {
	c := NewClient(nil, Services{})
	c.pause(AllDebrid, &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: time.Hour})
	err := c.slot(context.Background(), AllDebrid)
	var se *engine.ServiceError
	if !errors.As(err, &se) || se.Fault != engine.FaultTooManyRequests || se.Wait < 59*time.Minute {
		t.Error(err)
	}
	for range RealDebrid.perMinute() {
		if err := c.slot(context.Background(), RealDebrid); err != nil {
			t.Fatal("another service's budget is its own:", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.slot(ctx, RealDebrid); !errors.Is(err, context.DeadlineExceeded) {
		t.Error("over the budget, it waits:", err)
	}
}
