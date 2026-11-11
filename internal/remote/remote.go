// Ported from Mister Download Manager, crates/remote/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package remote is Yellow Flash's remote control: a phone or another
// computer on the local network adds downloads and torrents, sees the list
// and controls it, from a small web page Yellow Flash serves over HTTPS.
// Off by default (Options → Remote).
//
//   - One command path: every request becomes an ipc.Command marked Remote,
//     handed to the app through its command channel (ipc.Sink) and answered
//     by the app as the automation API is.
//   - Paired devices only: a code shown in Yellow Flash pairs a device
//     (pairing.go), which gets a token of its own (devices.go), removable in
//     Options.
//   - Local network only: one listener per local network address
//     (addrs.go), never all at once.
//   - HTTPS with a certificate made once and kept (tls.go); plain http:// is
//     redirected.
package remote

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/vaisah/internal/ipc"
)

// Port is the default port.
const Port = 8463

// Config is where and whether to listen.
type Config struct {
	On bool `json:"on"`
	// Port: 0 is any free port (tests).
	Port int `json:"port"`
	// Listen is "" for every local network address, an address, or an
	// adapter's name (Chosen).
	Listen string `json:"listen"`
	// Hosts are names besides the addresses a request may be sent to
	// (Host): a server's name in DNS, a reverse proxy's.
	Hosts []string `json:"hosts"`
}

func (c Config) equal(o Config) bool {
	return c.On == o.On && c.Port == o.Port && c.Listen == o.Listen && slices.Equal(c.Hosts, o.Hosts)
}

// State is what the remote control is doing.
type State string

const (
	Off State = "off"
	// Listening at Status.Addrs.
	Listening State = "listening"
	// NoAddress: on, but no address to listen on (no local network now, or
	// the address chosen is gone); it starts when one comes.
	NoAddress State = "no-address"
	// PortInUse: the port is taken by another program.
	PortInUse State = "port-in-use"
	// Failed: anything else (the certificate could not be made…).
	Failed State = "failed"
)

// Status is the remote control's state.
type Status struct {
	State State            `json:"state"`
	Addrs []netip.AddrPort `json:"addrs,omitempty"`
	Port  int              `json:"port,omitempty"`
	Error string           `json:"error,omitempty"`
}

// PageTexts are the page's texts in one language: Texts maps each English
// text to its translation.
type PageTexts struct {
	Lang  string            `json:"lang"`
	RTL   bool              `json:"rtl"`
	Texts map[string]string `json:"texts"`
}

// Texts is the app's translations for the page: given the browser's
// languages (de-DE,de,en), the texts in the best one it has.
type Texts func(langs string) PageTexts

// Remote is the remote control: one per app.
type Remote struct {
	// dir is remote/ in the data folder: certificate, paired devices.
	dir      string
	sink     ipc.Sink
	texts    Texts
	devices  *Devices
	pairing  *Pairing
	onChange func()
	logger   func(format string, args ...any)
	adapters func() []Adapter

	mu          sync.Mutex
	config      Config
	status      Status
	started     bool
	ips         []netip.Addr
	servers     []*http.Server
	fingerprint string
	stopWatch   chan struct{}
	// generation counts the restarts: a connection still open from before
	// one is refused.
	generation uint64
}

// New is the remote control of the app whose commands go to sink, keeping
// its files in dir; off until Apply turns it on. onChange (may be nil) is
// told when a device paired or left; logf (may be nil) gets its log lines
// (tokens are never in them).
func New(dir string, sink ipc.Sink, texts Texts, onChange func(), logf func(string, ...any)) *Remote {
	r := &Remote{dir: dir, sink: sink, texts: texts, pairing: &Pairing{}, onChange: onChange, logger: logf,
		adapters: Adapters, devices: LoadDevices(filepath.Join(dir, "devices.json")), status: Status{State: Off}}
	r.devices.OnSaveError = func(err error) { r.logf("remote control: the paired devices could not be saved: %v", err) }
	return r
}

func (r *Remote) logf(format string, args ...any) {
	if r.logger != nil {
		r.logger(format, args...)
	}
}

func (r *Remote) changed() {
	if r.onChange != nil {
		r.onChange()
	}
}

func (r *Remote) generationNow() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generation
}

// Apply listens as config says (started, stopped or moved when it
// changed).
func (r *Remote) Apply(config Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started && r.config.equal(config) {
		return
	}
	r.config = config
	r.started = true
	r.restartLocked()
}

// Status is what it is doing.
func (r *Remote) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// URLs are the page's addresses (https://192.168.1.20:8463/) while it
// listens, the one a phone most likely reaches first.
func (r *Remote) URLs() []string {
	st := r.Status()
	if st.State != Listening {
		return nil
	}
	addrs := slices.Clone(st.Addrs)
	slices.SortStableFunc(addrs, func(a, b netip.AddrPort) int { return Rank(a.Addr()) - Rank(b.Addr()) })
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, URL(a.Addr(), int(a.Port())))
	}
	return out
}

// Fingerprint is the certificate's SHA-256, once it is made.
func (r *Remote) Fingerprint() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fingerprint
}

// Devices are the paired devices.
func (r *Remote) Devices() []Device { return r.devices.List() }

// RemoveDevice unpairs device id: refused from its next request on.
func (r *Remote) RemoveDevice(id string) bool { return r.devices.Remove(id) }

// StartPairing shows a new pairing code (Pair a device).
func (r *Remote) StartPairing() string { return r.pairing.Start() }

// StopPairing: the pairing window closed.
func (r *Remote) StopPairing() { r.pairing.Stop() }

// PairingState is the code shown and its time left ("": none), and the
// device it paired, once it has.
func (r *Remote) PairingState() (code string, left time.Duration, paired string) {
	code, left = r.pairing.Shown()
	return code, left, r.pairing.Paired()
}

// Stop stops listening (the app quits).
func (r *Remote) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config.On = false
	r.started = true
	r.restartLocked()
}

// restartLocked stops what listens, then listens again as r.config says.
func (r *Remote) restartLocked() {
	for _, s := range r.servers {
		_ = s.Close()
	}
	r.servers = nil
	if r.stopWatch != nil {
		close(r.stopWatch)
		r.stopWatch = nil
	}
	r.ips = nil
	r.generation++
	if !r.config.On {
		r.status = Status{State: Off}
		return
	}
	ips := Chosen(r.config.Listen, r.adapters())
	// Every local network address: watched, as they come and go (Wi-Fi
	// joined, DHCP).
	if r.config.Listen == "" {
		r.stopWatch = make(chan struct{})
		go r.watch(r.stopWatch)
	}
	if len(ips) == 0 {
		r.status = Status{State: NoAddress}
		return
	}
	// The certificate names the computer, localhost and the addresses found
	// the first time.
	names := []string{"localhost"}
	if h := HostName(); h != "" {
		names = append(names, h)
	}
	for _, ip := range ips {
		names = append(names, ip.String())
	}
	id, err := identity(r.dir, names)
	if err != nil {
		r.logf("remote control: no certificate: %v", err)
		r.status = Status{State: Failed, Error: err.Error()}
		return
	}
	r.fingerprint = id.Fingerprint
	var (
		addrs   []netip.AddrPort
		failure *Status
	)
	hosts := slices.Clone(r.config.Hosts)
	for _, ip := range ips {
		tcp, err := net.Listen("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(r.config.Port)))
		if err != nil {
			if addrInUse(err) {
				failure = &Status{State: PortInUse, Port: r.config.Port}
			} else if failure == nil {
				failure = &Status{State: Failed, Error: err.Error()}
			}
			continue
		}
		ap, _ := netip.ParseAddrPort(tcp.Addr().String())
		s := &server{r: r, hosts: hosts, generation: r.generation}
		srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: handshake, ReadTimeout: idle,
			WriteTimeout: idle, IdleTimeout: idle, ErrorLog: log.New(io.Discard, "", 0)}
		l := newListener(tcp, id.Config, func(h string) bool { return HostAllowed(h, hosts) })
		go func() { _ = srv.Serve(l) }()
		r.servers = append(r.servers, srv)
		addrs = append(addrs, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	r.ips = ips
	switch {
	case len(addrs) > 0:
		r.logf("remote control: listening on %v", addrs)
		r.status = Status{State: Listening, Addrs: addrs}
	case failure != nil:
		r.status = *failure
	default:
		r.status = Status{State: NoAddress}
	}
}

// watch listens again, every 10 s, when the local network addresses
// changed.
func (r *Remote) watch(stop chan struct{}) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		ips := Chosen("", r.adapters())
		r.mu.Lock()
		if r.stopWatch == stop && r.config.On && r.config.Listen == "" && !slices.Equal(ips, r.ips) {
			r.restartLocked()
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
	}
}
