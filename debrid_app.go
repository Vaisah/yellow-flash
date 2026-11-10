// Ported from Mister Download Manager, crates/app (debrid accounts in Options, #44)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Debrid accounts in the app: the user's own Real-Debrid, AllDebrid or
// Premiumize account turns file-host links into direct links (and may take
// torrents, debrid_torrents_app.go). The list (service, user name, order,
// on or off) is kept with the settings in downloads.json; each sign-in is
// kept in the system's keychain (Windows Credential Manager, the Secret
// Service on Linux) under "Yellow Flash/debrid/<id>", never in Yellow
// Flash's files, never in a log. Without a keychain, sign-ins last until
// the app closes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vaisah/internal/debrid"
	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/platform"
	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

const keyDebrid = "debrid.settings"

// DebridAccount is an account in the list.
type DebridAccount struct {
	ID      string      `json:"id"`
	Service debrid.Kind `json:"service"`
	// User is the service's name for the account, as it said at sign-in.
	User    string `json:"user"`
	Enabled bool   `json:"enabled"`
	// SignedIn: its sign-in is kept (false: sign in again).
	SignedIn bool `json:"signedIn"`
}

// DebridSettings is Options → Debrid.
type DebridSettings struct {
	// Accounts in priority order: a link goes to the first whose service
	// takes its host.
	Accounts []DebridAccount `json:"accounts"`
	// Torrents: magnet links and .torrent files go to the first account,
	// which downloads them on its servers; no peer connects to this
	// computer.
	Torrents bool `json:"torrents"`
	// Keychain says sign-ins are kept in the system's keychain (false: only
	// until the app closes). Not saved.
	Keychain bool `json:"keychain"`
}

// DebridService describes a service for Options.
type DebridService struct {
	Service       debrid.Kind `json:"service"`
	Name          string      `json:"name"`
	TokenPage     string      `json:"tokenPage"`
	SignsInByCode bool        `json:"signsInByCode"`
}

// DebridSignIn is a sign-in by code: the code to type at the page, and how
// it went.
type DebridSignIn struct {
	Service debrid.Kind `json:"service"`
	Code    string      `json:"code"`
	Page    string      `json:"page"`
	Expires time.Time   `json:"expires"`
	// State is "waiting", "done", "failed", "expired" or "cancelled".
	State   string         `json:"state"`
	Error   string         `json:"error,omitempty"`
	Account *DebridAccount `json:"account,omitempty"`
}

// debridHost holds the accounts at work.
type debridHost struct {
	once sync.Once
	d    *debrid.Debrid

	mu       sync.Mutex
	client   *debrid.Client
	secrets  platform.SecretStore
	keychain bool
	signin   *DebridSignIn
	cancelIn context.CancelFunc
	stop     chan struct{}

	// Tests point these at fakes (zero: the real services, the app's own
	// client, every 5 s).
	services   *debrid.Services
	httpClient debrid.Doer
	poll       time.Duration

	torrents debridTorrents
}

// debridAccounts is the engine's Unlockers: the same object for the app's
// life, its accounts changed in place.
func (a *App) debridAccounts() *debrid.Debrid {
	a.debrid.once.Do(func() { a.debrid.d = &debrid.Debrid{} })
	return a.debrid.d
}

// debridSecrets is where sign-ins are kept: the system's keychain, or
// memory when there is none.
func (a *App) debridSecrets() (platform.SecretStore, bool) {
	h := &a.debrid
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.secrets == nil {
		if k, err := platform.NewKeychain(AppName); err == nil {
			h.secrets, h.keychain = k, true
		} else {
			h.secrets = platform.NewMemorySecretStore()
			a.logf("debrid: %v; sign-ins last until the app closes", err)
		}
	}
	return h.secrets, h.keychain
}

// debridClient is the client the services are asked through: the
// connection settings' proxy and DNS, public addresses only.
func (a *App) debridClient() *debrid.Client {
	h := &a.debrid
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client == nil {
		h.client = a.newDebridClientLocked()
	}
	return h.client
}

func (a *App) newDebridClientLocked() *debrid.Client {
	h := &a.debrid
	services := debrid.DefaultServices()
	if h.services != nil {
		services = *h.services
	}
	doer := h.httpClient
	if doer == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		dns, opts, err := a.engineNetwork(ctx)
		if err != nil {
			a.logf("debrid: network settings: %v", err)
		}
		opts.DNS, opts.AllowPrivate = dns, false
		doer = network.NewClient(opts)
	}
	c := debrid.NewClient(doer, services)
	c.UserAgent = "YellowFlash/" + AppVersion
	return c
}

func secretKey(id string) string { return "debrid/" + id }

func credentialMap(c debrid.Credential) map[string]string {
	return map[string]string{"kind": c.Kind, "token": c.Token, "client_id": c.ClientID,
		"client_secret": c.ClientSecret, "refresh_token": c.RefreshToken}
}

func credentialOf(m map[string]string) debrid.Credential {
	return debrid.Credential{Kind: m["kind"], Token: m["token"], ClientID: m["client_id"],
		ClientSecret: m["client_secret"], RefreshToken: m["refresh_token"]}
}

// savedDebrid is the list as saved (SignedIn and Keychain are worked out).
func (a *App) savedDebrid() DebridSettings {
	var s DebridSettings
	if a.store == nil {
		return s
	}
	if raw, ok, _ := a.store.Settings().Get(context.Background(), keyDebrid); ok {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (a *App) saveDebrid(s DebridSettings) error {
	for i := range s.Accounts {
		s.Accounts[i].SignedIn = false
	}
	s.Keychain = false
	raw, _ := json.Marshal(s)
	return a.store.Settings().Set(context.Background(), keyDebrid, string(raw))
}

// GetDebridSettings is the accounts, each said signed in or not.
func (a *App) GetDebridSettings() DebridSettings {
	s := a.savedDebrid()
	secrets, keychain := a.debridSecrets()
	s.Keychain = keychain
	for i := range s.Accounts {
		_, err := secrets.Get(context.Background(), secretKey(s.Accounts[i].ID))
		s.Accounts[i].SignedIn = err == nil
	}
	return s
}

// DebridServices lists the services for Options.
func (a *App) DebridServices() []DebridService {
	var out []DebridService
	for _, k := range debrid.Kinds {
		out = append(out, DebridService{Service: k, Name: k.Name(), TokenPage: k.TokenPage(), SignsInByCode: k.SignsInByCode()})
	}
	return out
}

// SaveDebridSettings saves the accounts' order and on/off, and the torrent
// choice; accounts are added by signing in and removed with
// RemoveDebridAccount.
func (a *App) SaveDebridSettings(s DebridSettings) (DebridSettings, error) {
	old := a.savedDebrid()
	var accounts []DebridAccount
	for _, in := range s.Accounts {
		i := slices.IndexFunc(old.Accounts, func(o DebridAccount) bool { return o.ID == in.ID })
		if i < 0 || slices.ContainsFunc(accounts, func(o DebridAccount) bool { return o.ID == in.ID }) {
			continue
		}
		acct := old.Accounts[i]
		acct.Enabled = in.Enabled
		accounts = append(accounts, acct)
	}
	for _, o := range old.Accounts { // one left out keeps its place at the end
		if !slices.ContainsFunc(accounts, func(x DebridAccount) bool { return x.ID == o.ID }) {
			accounts = append(accounts, o)
		}
	}
	if err := a.saveDebrid(DebridSettings{Accounts: accounts, Torrents: s.Torrents}); err != nil {
		return DebridSettings{}, err
	}
	a.applyDebrid()
	return a.GetDebridSettings(), nil
}

// addDebridAccount keeps a checked sign-in and adds its account (or
// replaces the sign-in of the same service's account with that user).
func (a *App) addDebridAccount(kind debrid.Kind, cr debrid.Credential, user string) (DebridAccount, error) {
	s := a.savedDebrid()
	acct := DebridAccount{ID: uuid.NewString(), Service: kind, User: user, Enabled: true}
	if i := slices.IndexFunc(s.Accounts, func(o DebridAccount) bool { return o.Service == kind && o.User == user && user != "" }); i >= 0 {
		acct.ID = s.Accounts[i].ID
		s.Accounts[i] = acct
	} else {
		s.Accounts = append(s.Accounts, acct)
	}
	secrets, _ := a.debridSecrets()
	if err := secrets.Put(context.Background(), secretKey(acct.ID), credentialMap(cr)); err != nil {
		return DebridAccount{}, fmt.Errorf("the sign-in couldn't be kept: %w", err)
	}
	if err := a.saveDebrid(s); err != nil {
		return DebridAccount{}, err
	}
	a.applyDebrid()
	acct.SignedIn = true
	return acct, nil
}

// AddDebridToken signs in with a pasted API token (key): checked with the
// service first.
func (a *App) AddDebridToken(service string, token string) (DebridAccount, error) {
	kind := debrid.Kind(service)
	token = strings.TrimSpace(token)
	if !kind.Valid() {
		return DebridAccount{}, fmt.Errorf("unknown service %q", service)
	}
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return DebridAccount{}, errors.New("paste the service's API token")
	}
	cr := debrid.Token(token)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := debrid.Check(ctx, a.debridClient(), kind, cr)
	if err != nil {
		return DebridAccount{}, err
	}
	return a.addDebridAccount(kind, cr, info.User)
}

// StartDebridSignIn starts a sign-in by code: the code and the page to
// type it at. It is asked about in the background until it is allowed, it
// expires or it is cancelled; "debrid:signin" says how it went.
func (a *App) StartDebridSignIn(service string) (DebridSignIn, error) {
	kind := debrid.Kind(service)
	if !kind.SignsInByCode() {
		return DebridSignIn{}, fmt.Errorf("%s signs in with its API token", kind.Name())
	}
	client := a.debridClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	code, err := debrid.StartSignIn(ctx, client, kind)
	cancel()
	if err != nil {
		return DebridSignIn{}, err
	}
	view := &DebridSignIn{Service: kind, Code: code.Shown, Page: code.Page, Expires: code.Expires, State: "waiting"}
	wait, stop := context.WithDeadline(context.Background(), code.Expires)
	h := &a.debrid
	h.mu.Lock()
	if h.cancelIn != nil {
		h.cancelIn()
	}
	h.signin, h.cancelIn = view, stop
	out := *view
	h.mu.Unlock()
	go a.waitSignIn(wait, client, code, view)
	return out, nil
}

func (a *App) waitSignIn(ctx context.Context, client *debrid.Client, code *debrid.Code, view *DebridSignIn) {
	finish := func(state string, err error, acct *DebridAccount) {
		h := &a.debrid
		h.mu.Lock()
		view.State, view.Account = state, acct
		if err != nil {
			view.Error = err.Error()
		}
		out := *view
		h.mu.Unlock()
		a.emit("debrid:signin", out)
	}
	t := time.NewTicker(code.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finish("expired", nil, nil)
			} else {
				finish("cancelled", nil, nil)
			}
			return
		case <-t.C:
		}
		cr, err := debrid.PollSignIn(ctx, client, code)
		if ctx.Err() != nil {
			continue
		}
		if err != nil {
			finish("failed", err, nil)
			return
		}
		if cr == nil {
			continue
		}
		info, err := debrid.Check(ctx, client, code.Service, *cr)
		if err != nil {
			finish("failed", err, nil)
			return
		}
		acct, err := a.addDebridAccount(code.Service, *cr, info.User)
		if err != nil {
			finish("failed", err, nil)
			return
		}
		finish("done", nil, &acct)
		return
	}
}

// GetDebridSignIn is the sign-in by code under way (or the last one).
func (a *App) GetDebridSignIn() DebridSignIn {
	h := &a.debrid
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.signin == nil {
		return DebridSignIn{}
	}
	return *h.signin
}

// CancelDebridSignIn stops waiting for the sign-in by code.
func (a *App) CancelDebridSignIn() {
	h := &a.debrid
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancelIn != nil {
		h.cancelIn()
	}
}

// RemoveDebridAccount forgets an account and its sign-in.
func (a *App) RemoveDebridAccount(id string) error {
	s := a.savedDebrid()
	i := slices.IndexFunc(s.Accounts, func(o DebridAccount) bool { return o.ID == id })
	if i < 0 {
		return errors.New("no such account")
	}
	secrets, _ := a.debridSecrets()
	if err := secrets.Delete(context.Background(), secretKey(id)); err != nil {
		return fmt.Errorf("the sign-in couldn't be removed from the keychain: %w", err)
	}
	s.Accounts = slices.Delete(s.Accounts, i, i+1)
	if err := a.saveDebrid(s); err != nil {
		return err
	}
	a.applyDebrid()
	return nil
}

// CheckDebridAccount asks the service about an account: its user, premium
// and premium time left.
func (a *App) CheckDebridAccount(id string) (debrid.AccountInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.debridAccounts().AccountInfo(ctx, id)
}

// RefreshDebridHosts reads the services' host lists again.
func (a *App) RefreshDebridHosts() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return a.debridAccounts().RefreshHosts(ctx)
}

// applyDebrid gives the engine the enabled, signed-in accounts and reads
// their host lists (in the background).
func (a *App) applyDebrid() {
	s := a.savedDebrid()
	secrets, _ := a.debridSecrets()
	var accounts []debrid.Account
	for _, acct := range s.Accounts {
		if !acct.Enabled {
			continue
		}
		m, err := secrets.Get(context.Background(), secretKey(acct.ID))
		if err != nil {
			continue // signed out: sign in again
		}
		accounts = append(accounts, debrid.Account{ID: acct.ID, Service: acct.Service, Credential: credentialOf(m)})
		clear(m)
	}
	a.debridAccounts().SetAccounts(accounts, a.debridClient())
	if len(accounts) > 0 {
		go func() {
			if err := a.RefreshDebridHosts(); err != nil {
				a.logf("debrid: %v", err)
			}
		}()
	}
}

// restartDebridClient asks the services through the new connection
// settings.
func (a *App) restartDebridClient() {
	h := &a.debrid
	h.mu.Lock()
	h.client = a.newDebridClientLocked()
	h.mu.Unlock()
	a.applyDebrid()
}

// startDebrid sets the accounts up, reads their host lists twice a day,
// and continues the torrents sent to them.
func (a *App) startDebrid() {
	a.applyDebrid()
	h := &a.debrid
	h.mu.Lock()
	if h.stop != nil {
		h.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	h.stop = stop
	h.mu.Unlock()
	go func() {
		t := time.NewTicker(12 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := a.RefreshDebridHosts(); err != nil {
					a.logf("debrid: %v", err)
				}
			}
		}
	}()
	a.resumeDebridTorrents()
}

// stopDebrid stops the sign-in under way and the torrents (they continue
// at the next start).
func (a *App) stopDebrid() {
	h := &a.debrid
	h.mu.Lock()
	if h.cancelIn != nil {
		h.cancelIn()
	}
	if h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
	h.mu.Unlock()
	a.stopDebridTorrents()
}

// emit sends an event to the window once it is there.
func (a *App) emit(name string, data any) {
	if a.ctx != nil {
		wails.EventsEmit(a.ctx, name, data)
	}
}
