// Ported from Mister Download Manager, crates/debrid/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package debrid is debrid services: the user's own account at a service
// such as Real-Debrid turns a file host's link into a direct link, which
// the engine then downloads as any file (every connection, resume). Only
// the services' official, documented APIs, with the user's own account;
// Yellow Flash ships no host lists or link sources, and never shows or
// copies the direct links (the services forbid sharing them).
//
// The engine knows nothing of accounts: *Debrid is its engine.Unlockers.
// Secrets (tokens) are the app's to keep; here they are only in memory, and
// never written to a log, an error or a dump.
//
// The services: Real-Debrid, AllDebrid and Premiumize, each from its own
// public API documentation, on one interface (Kind).
package debrid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vaisah/internal/engine"
)

// Kind is a service.
type Kind string

// The services Yellow Flash knows.
const (
	RealDebrid Kind = "realdebrid"
	AllDebrid  Kind = "alldebrid"
	Premiumize Kind = "premiumize"
)

// Kinds is every service, in the order Options lists them.
var Kinds = []Kind{RealDebrid, AllDebrid, Premiumize}

// Name is the service's name, as it writes it (not translated).
func (k Kind) Name() string {
	switch k {
	case RealDebrid:
		return "Real-Debrid"
	case AllDebrid:
		return "AllDebrid"
	case Premiumize:
		return "Premiumize"
	}
	return string(k)
}

// TokenPage is the service's page where a user finds their API token (key).
func (k Kind) TokenPage() string {
	switch k {
	case RealDebrid:
		return "https://real-debrid.com/apitoken"
	case AllDebrid:
		return "https://alldebrid.com/apikeys"
	case Premiumize:
		return "https://www.premiumize.me/account"
	}
	return ""
}

// SignsInByCode reports whether it signs in by a code typed in the browser
// (Real-Debrid's device flow, AllDebrid's PIN). Premiumize's needs a client
// registered by the app's maintainer: its API key is pasted.
func (k Kind) SignsInByCode() bool { return k == RealDebrid || k == AllDebrid }

// Valid reports whether k is a service Yellow Flash knows.
func (k Kind) Valid() bool { return k == RealDebrid || k == AllDebrid || k == Premiumize }

// perMinute is the requests a minute allowed to the service (each one's
// published limit, halved or more: Real-Debrid 250 a minute, AllDebrid 600;
// Premiumize publishes none).
func (k Kind) perMinute() int {
	switch k {
	case RealDebrid:
		return 120
	case AllDebrid:
		return 300
	}
	return 60
}

// Services is where the services answer (tests: debridtest's fakes,
// ServicesAt).
type Services struct {
	RDRest, RDOAuth, AD, PM string
}

// DefaultServices is the real services.
func DefaultServices() Services {
	return Services{
		RDRest:  "https://api.real-debrid.com/rest/1.0/",
		RDOAuth: "https://api.real-debrid.com/oauth/v2/",
		AD:      "https://api.alldebrid.com/",
		PM:      "https://www.premiumize.me/api/",
	}
}

// ServicesAt is debridtest's fakes at base ("http://127.0.0.1:PORT/").
func ServicesAt(base string) Services {
	base = strings.TrimSuffix(base, "/") + "/"
	return Services{RDRest: base + "rd/rest/1.0/", RDOAuth: base + "rd/oauth/v2/", AD: base + "ad/", PM: base + "pm/api/"}
}

// Credential is how an account signs in: what the app keeps sealed.
type Credential struct {
	// Kind is "token" (a pasted token, or AllDebrid's PIN's key) or "oauth"
	// (Real-Debrid's device flow).
	Kind         string `json:"kind"`
	Token        string `json:"token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// Token is a pasted token's credential.
func Token(t string) Credential { return Credential{Kind: "token", Token: t} }

// String never prints a secret, nor does any dump.
func (c Credential) String() string {
	if c.Kind == "oauth" {
		return "Oauth(…)"
	}
	return "Token(…)"
}

// GoString is %#v's: no secret.
func (c Credential) GoString() string { return c.String() }

// LogValue is slog's: no secret.
func (c Credential) LogValue() slog.Value { return slog.StringValue(c.String()) }

// Account is an account as the app gives it: its id (stable), service and
// credential.
type Account struct {
	ID         string
	Service    Kind
	Credential Credential
}

// AccountInfo is what the service says about the account.
type AccountInfo struct {
	User    string `json:"user"`
	Premium bool   `json:"premium"`
	// PremiumLeft is the premium time left (0: none, or not said).
	PremiumLeft time.Duration `json:"premiumLeft"`
}

// errUnsupported: the service doesn't support this file host; the link is
// downloaded directly.
var errUnsupported = errors.New("this file host isn't supported")

func fault(f engine.Fault) error { return &engine.ServiceError{Fault: f} }

func other(words string) error { return &engine.ServiceError{Fault: engine.FaultOther, Detail: words} }

var errUnreadable = other("an answer Yellow Flash can't read")

// Doer sends requests: the app's client (proxy, DNS), or a test's.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client sends requests to the services: through the app's client, each
// service at most its perMinute budget, and none for a while after it said
// "too many requests" (refused requests count, and abuse gets a block).
type Client struct {
	doer     Doer
	services Services
	// UserAgent names the app to the services.
	UserAgent string
	// minPoll is the shortest wait between AllDebrid's delayed-link polls
	// (its documentation: 5 s or more); tests shorten it.
	minPoll time.Duration

	mu      sync.Mutex
	budgets map[Kind]*budget
}

type budget struct {
	sent   []time.Time
	paused time.Time
}

// NewClient sends through doer to services.
func NewClient(doer Doer, services Services) *Client {
	return &Client{doer: doer, services: services, UserAgent: "YellowFlash", minPoll: 5 * time.Second,
		budgets: map[Kind]*budget{}}
}

// fresh is a client on the same doer and services with its own budgets.
func (c *Client) fresh() *Client {
	n := NewClient(c.doer, c.services)
	n.UserAgent, n.minPoll = c.UserAgent, c.minPoll
	return n
}

// slot waits for kind's budget to allow one more request.
func (c *Client) slot(ctx context.Context, kind Kind) error {
	for {
		c.mu.Lock()
		b := c.budgets[kind]
		if b == nil {
			b = &budget{}
			c.budgets[kind] = b
		}
		now := time.Now()
		if b.paused.After(now) {
			wait := b.paused.Sub(now)
			c.mu.Unlock()
			// Asked to slow down: no request until then.
			return &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: wait}
		}
		for len(b.sent) > 0 && now.Sub(b.sent[0]) >= time.Minute {
			b.sent = b.sent[1:]
		}
		if len(b.sent) < kind.perMinute() {
			b.sent = append(b.sent, now)
			c.mu.Unlock()
			return nil
		}
		wait := time.Minute - now.Sub(b.sent[0])
		c.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// pause: kind said "too many requests"; nothing more to it for that long (a
// minute if unsaid).
func (c *Client) pause(kind Kind, err error) {
	var se *engine.ServiceError
	if !errors.As(err, &se) || se.Fault != engine.FaultTooManyRequests {
		return
	}
	wait := se.Wait
	if wait <= 0 {
		wait = time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budgets[kind] == nil {
		c.budgets[kind] = &budget{}
	}
	c.budgets[kind].paused = time.Now().Add(wait)
}

// answer is a service's reply.
type answer struct {
	status     int
	body       []byte
	retryAfter time.Duration
}

// maxAnswer bounds an answer's body (AllDebrid's host list is the largest).
const maxAnswer = 8 << 20

// send sends a request to kind within its budget (GET, or POST with form)
// as token (none when ""), and reads its answer. The token travels only in
// the Authorization header, never in an address (addresses end up in logs).
func (c *Client) send(ctx context.Context, kind Kind, method, rawURL string, form url.Values, token string) (answer, error) {
	if err := c.slot(ctx, kind); err != nil {
		return answer{}, err
	}
	var body io.Reader
	if form != nil && method != http.MethodGet {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return answer{}, fault(engine.FaultServiceDown)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.doer.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return answer{}, ctx.Err()
		}
		return answer{}, fault(engine.FaultServiceDown) // the error names the address; it stays here
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer))
	if err != nil {
		return answer{}, fault(engine.FaultServiceDown)
	}
	a := answer{status: res.StatusCode, body: b}
	if s, err := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After"))); err == nil && s > 0 {
		a.retryAfter = time.Duration(s) * time.Second
	}
	if a.status == http.StatusTooManyRequests {
		c.pause(kind, &engine.ServiceError{Fault: engine.FaultTooManyRequests, Wait: a.retryAfter})
	}
	return a, nil
}

/* ---------- one account, signed in ---------- */

// session is one account: its access token in memory (Real-Debrid's device
// flow gives tokens that expire; a pasted token doesn't).
type session struct {
	account Account
	client  *Client

	mu     sync.Mutex
	access string
	until  time.Time
}

// token is a token to call with: the pasted one, or the device flow's
// access token, refreshed five minutes before it expires (or when stale,
// after a sign-in error).
func (s *session) token(ctx context.Context, stale bool) (string, error) {
	cr := s.account.Credential
	if cr.Kind != "oauth" {
		return cr.Token, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !stale && s.access != "" && time.Now().Before(s.until) {
		return s.access, nil
	}
	t, err := rdToken(ctx, s.client, cr.ClientID, cr.ClientSecret, cr.RefreshToken)
	if err != nil {
		return "", err
	}
	life := max(time.Duration(t.ExpiresIn)*time.Second-5*time.Minute, time.Minute)
	s.access, s.until = t.AccessToken, time.Now().Add(life)
	return s.access, nil
}

// withToken calls call with a token; once more with a fresh one after a
// sign-in error (an expired access token).
func (s *session) withToken(ctx context.Context, call func(token string) error) error {
	t, err := s.token(ctx, false)
	if err != nil {
		return err
	}
	err = call(t)
	var se *engine.ServiceError
	if s.account.Credential.Kind == "oauth" && errors.As(err, &se) && se.Fault == engine.FaultSignIn {
		if t, err = s.token(ctx, true); err != nil {
			return err
		}
		return call(t)
	}
	return err
}

// usable reports a direct link the engine can ask.
func usable(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

var errNotAFile = other("the service gave no file for this link")

// unlock is today's direct link for link, or nil (not a host the service
// takes).
func (s *session) unlock(ctx context.Context, link string, note func(string)) (*engine.Unlocked, error) {
	switch s.account.Service {
	case AllDebrid:
		key, _ := s.token(ctx, false)
		u, err := adUnlock(ctx, s.client, key, link)
		if errors.Is(err, errUnsupported) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		direct := u.Link
		if u.Delayed != 0 {
			if direct, err = s.adWait(ctx, key, u.Delayed, note); err != nil {
				return nil, err
			}
		}
		if !usable(direct) {
			return nil, errNotAFile
		}
		return &engine.Unlocked{URL: direct, FileName: strings.TrimSpace(u.Filename), Size: max(u.Filesize, 0)}, nil
	case Premiumize:
		key, _ := s.token(ctx, false)
		d, err := pmDirect(ctx, s.client, key, link)
		if errors.Is(err, errUnsupported) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		// One file per host link; several are a folder (not a file).
		if len(d.Content) != 1 || !usable(d.Content[0].Link) {
			return nil, errNotAFile
		}
		e := d.Content[0]
		name := e.Path[strings.LastIndex(e.Path, "/")+1:]
		return &engine.Unlocked{URL: e.Link, FileName: strings.TrimSpace(name), Size: max(e.Size, 0)}, nil
	default: // Real-Debrid
		var u rdUnrestricted
		err := s.withToken(ctx, func(t string) error {
			var err error
			u, err = rdUnrestrict(ctx, s.client, t, link)
			return err
		})
		if errors.Is(err, errUnsupported) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		// A multi-link answer (a video site) has no single download: not a file.
		if !usable(u.Download) {
			return nil, errNotAFile
		}
		return &engine.Unlocked{URL: u.Download, FileName: strings.TrimSpace(u.Filename), Size: max(u.Filesize, 0),
			MaxConnections: max(u.Chunks, 0)}, nil
	}
}

// adWait waits for an AllDebrid link that takes time to generate: asked
// every 5 s or more, up to 10 minutes, said so in the log, stopped with the
// download.
func (s *session) adWait(ctx context.Context, key string, id int64, note func(string)) (string, error) {
	until := time.Now().Add(10 * time.Minute)
	for {
		d, err := adDelayed(ctx, s.client, key, id)
		if err != nil {
			return "", err
		}
		switch d.Status {
		case 2:
			return d.Link, nil
		case 3:
			return "", fault(engine.FaultFileUnavailable)
		}
		if time.Now().After(until) {
			return "", fault(engine.FaultServiceDown)
		}
		left := max(d.TimeLeft, 0)
		note(fmt.Sprintf("AllDebrid is preparing the link (about %d s)", left))
		t := time.NewTimer(max(time.Duration(left)*time.Second/4, s.client.minPoll))
		select {
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		case <-t.C:
		}
	}
}

// info asks the service about the account.
func (s *session) info(ctx context.Context) (AccountInfo, error) {
	switch s.account.Service {
	case AllDebrid:
		key, _ := s.token(ctx, false)
		u, err := adUser(ctx, s.client, key)
		if err != nil {
			return AccountInfo{}, err
		}
		info := AccountInfo{User: u.Username, Premium: u.IsPremium}
		if left := time.Until(time.Unix(u.PremiumUntil, 0)); u.IsPremium && left > 0 {
			info.PremiumLeft = left
		}
		return info, nil
	case Premiumize:
		key, _ := s.token(ctx, false)
		a, err := pmAccount(ctx, s.client, key)
		if err != nil {
			return AccountInfo{}, err
		}
		// Premiumize gives no user name: its customer ID stands for it.
		info := AccountInfo{User: a.customerID()}
		if a.PremiumUntil != nil {
			if left := time.Until(time.Unix(*a.PremiumUntil, 0)); left > 0 {
				info.Premium, info.PremiumLeft = true, left
			}
		}
		return info, nil
	default:
		var u rdUser
		err := s.withToken(ctx, func(t string) error {
			var err error
			u, err = rdGetUser(ctx, s.client, t)
			return err
		})
		if err != nil {
			return AccountInfo{}, err
		}
		info := AccountInfo{User: u.Username, Premium: u.Type == "premium" && u.Premium > 0}
		if u.Premium > 0 {
			info.PremiumLeft = time.Duration(u.Premium) * time.Second
		}
		return info, nil
	}
}

// named gives err the service's name, for the app.
func named(kind Kind, err error) error {
	var se *engine.ServiceError
	if errors.As(err, &se) && se.Service == "" {
		return &engine.ServiceError{Service: kind.Name(), Fault: se.Fault, Wait: se.Wait, Detail: se.Detail}
	}
	return err
}

// Check checks a credential before it is kept: the account it signs in to.
func Check(ctx context.Context, c *Client, kind Kind, cr Credential) (AccountInfo, error) {
	s := &session{account: Account{Service: kind, Credential: cr}, client: c.fresh()}
	info, err := s.info(ctx)
	return info, named(kind, err)
}

// plainID is a service's id, checked before it goes into an address:
// letters, digits, '-' and '_' only.
func plainID(id string) (string, error) {
	bad := other("the service gave an id Yellow Flash can't use")
	if id == "" {
		return "", bad
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", bad
		}
	}
	return id, nil
}

// HostMatches reports whether host belongs to one of domains: the domain
// itself or a subdomain of it ("www." and case ignored). notexample.com is
// not example.com's.
func HostMatches(host string, domains []string) bool {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(host, ".")), "www.")
	if host == "" {
		return false
	}
	for _, d := range domains {
		d = strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), ".")), "www.")
		if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
			return true
		}
	}
	return false
}

/* ---------- the app's handle ---------- */

// Debrid is the accounts in priority order, each with its service's
// supported domains. It is the engine's Unlockers.
type Debrid struct {
	mu       sync.RWMutex
	sessions []*session
	domains  map[string][]string // account id → its service's domains
	// pinned are links that go through an account whatever their host (a
	// torrent's files while they download; torrents.go).
	pinned map[string]*session
}

// String says how many accounts, never what they hold.
func (d *Debrid) String() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return fmt.Sprintf("Debrid(%d accounts)", len(d.sessions))
}

// SetAccounts uses these accounts, in this order, through c. Their host
// lists are kept for the accounts still there; RefreshHosts reads them
// again. The same account (id and credential) keeps its access token.
func (d *Debrid) SetAccounts(accounts []Account, c *Client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	kept := make([]*session, 0, len(accounts))
	ids := map[string]bool{}
	for _, a := range accounts {
		var s *session
		for _, old := range d.sessions {
			if old.account == a && old.client == c {
				s = old
				break
			}
		}
		if s == nil {
			s = &session{account: a, client: c}
		}
		kept = append(kept, s)
		ids[a.ID] = true
	}
	for id := range d.domains {
		if !ids[id] {
			delete(d.domains, id)
		}
	}
	d.sessions = kept
}

func (d *Debrid) all() []*session {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]*session(nil), d.sessions...)
}

// RefreshHosts reads each account's service's supported domains. A service
// that doesn't answer leaves the others' lists read: the first failure is
// returned, with its service's name, once every account was asked.
func (d *Debrid) RefreshHosts(ctx context.Context) error {
	var first error
	for _, s := range d.all() {
		var list []string
		var err error
		switch s.account.Service {
		case AllDebrid:
			list, err = adDomains(ctx, s.client)
		case Premiumize:
			// Premiumize lists its services to signed-in accounts.
			key, _ := s.token(ctx, false)
			list, err = pmDomains(ctx, s.client, key)
		default:
			list, err = rdDomains(ctx, s.client)
		}
		if err != nil {
			if first == nil {
				first = named(s.account.Service, err)
			}
			continue
		}
		d.mu.Lock()
		if d.domains == nil {
			d.domains = map[string][]string{}
		}
		d.domains[s.account.ID] = list
		d.mu.Unlock()
	}
	return first
}

// HostsKnown reports whether the host lists are there for every account.
func (d *Debrid) HostsKnown() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, s := range d.sessions {
		if _, ok := d.domains[s.account.ID]; !ok {
			return false
		}
	}
	return true
}

// AccountFor is the account that would take rawURL: the first whose
// service supports its host.
func (d *Debrid) AccountFor(rawURL string) (id string, kind Kind, ok bool) {
	s := d.sessionFor(rawURL)
	if s == nil {
		return "", "", false
	}
	return s.account.ID, s.account.Service, true
}

func (d *Debrid) sessionFor(rawURL string) *session {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if s := d.pinned[rawURL]; s != nil {
		return s
	}
	for _, s := range d.sessions {
		if HostMatches(u.Hostname(), d.domains[s.account.ID]) {
			return s
		}
	}
	return nil
}

// UnlockerFor is the engine's unlocker for rawURL, when an account takes
// its host (engine.Unlockers).
func (d *Debrid) UnlockerFor(rawURL string) engine.Unlocker {
	if s := d.sessionFor(rawURL); s != nil {
		return &unlocker{s}
	}
	return nil
}

// AccountInfo asks the service about account id (Options, after signing in).
func (d *Debrid) AccountInfo(ctx context.Context, id string) (AccountInfo, error) {
	for _, s := range d.all() {
		if s.account.ID == id {
			info, err := s.info(ctx)
			return info, named(s.account.Service, err)
		}
	}
	return AccountInfo{}, fault(engine.FaultSignIn)
}

// unlocker is one account's unlocker for the engine.
type unlocker struct{ s *session }

func (u *unlocker) Name() string { return u.s.account.Service.Name() }

func (u *unlocker) String() string { return "unlocker(" + u.Name() + ")" }

func (u *unlocker) Unlock(ctx context.Context, rawURL string, note func(string)) (*engine.Unlocked, error) {
	return u.s.unlock(ctx, rawURL, note)
}

// decode reads a JSON answer into out.
func decode(body []byte, out any) error {
	if err := json.Unmarshal(body, out); err != nil {
		return errUnreadable
	}
	return nil
}
