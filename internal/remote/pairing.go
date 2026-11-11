// Ported from Mister Download Manager, crates/remote/src/pairing.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package remote

// Pairing codes. Options → Remote → Pair a device shows an 8-digit code,
// valid for ten minutes and for one device; closing that window ends it.
// Each device (by its address) gets five wrong tries, one a second at most;
// after twenty wrong tries in all the code is void (a new one must be
// shown). A guesser gets at most twenty chances in a hundred million per
// code shown, and a device that keeps guessing can't use up the tries of
// the phone being paired.

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	// CodeValid is how long a code shown stays valid.
	CodeValid = 10 * time.Minute
	// Tries are the wrong tries one device (address) gets for a code.
	Tries = 5
	// TotalTries are the wrong tries in all, from every device, before the
	// code is void.
	TotalTries = 20
	// Pace is the least time between two tries from one device.
	Pace = time.Second
	// peers are the addresses remembered for pacing (more are forgotten
	// once they're past their pace).
	peers = 256
)

// CheckResult is a try's outcome.
type CheckResult int

const (
	Right CheckResult = iota
	// Wrong: Check's left says the tries left for this device (0: it can't
	// try this code again).
	Wrong
	// NoCode: no code is shown (none asked for, timed out, or used up).
	NoCode
	// TooSoon: too soon after this device's last try.
	TooSoon
)

type activeCode struct {
	code    string
	until   time.Time
	wrong   int
	wrongBy map[netip.Addr]int
}

// Pairing is the code shown, if any, and the tries made.
type Pairing struct {
	mu      sync.Mutex
	active  *activeCode
	lastTry map[netip.Addr]time.Time
	// paired is the device the code shown paired (the window says so and
	// closes).
	paired string
	now    func() time.Time
}

func (p *Pairing) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// Start shows a new code (the one before is void).
func (p *Pairing) Start() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		// The system's generator doesn't fail on a working system.
		panic(err)
	}
	code := fmt.Sprintf("%08d", n.Int64())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = &activeCode{code: code, until: p.clock().Add(CodeValid), wrongBy: map[netip.Addr]int{}}
	p.paired = ""
	return code
}

// Stop: the pairing window closed, its code is void.
func (p *Pairing) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active, p.paired = nil, ""
}

// Shown is the code shown now and the time it has left ("": none).
func (p *Pairing) Shown() (string, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active == nil {
		return "", 0
	}
	left := p.active.until.Sub(p.clock())
	if left <= 0 {
		return "", 0
	}
	return p.active.code, left
}

// Paired is the device the code shown paired, once it has ("": not yet).
func (p *Pairing) Paired() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paired
}

// Check: the device at peer tries given (spaces and dashes ignored); left
// is the tries it has left after a wrong one.
func (p *Pairing) Check(given string, peer netip.Addr) (result CheckResult, left int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	peer = peer.Unmap()
	if t, ok := p.lastTry[peer]; ok && now.Sub(t) < Pace {
		return TooSoon, 0
	}
	if len(p.lastTry) >= peers {
		for a, t := range p.lastTry {
			if now.Sub(t) >= Pace {
				delete(p.lastTry, a)
			}
		}
		if len(p.lastTry) >= peers {
			// Hundreds of addresses trying within one second: none more
			// until they slow.
			return TooSoon, 0
		}
	}
	if p.lastTry == nil {
		p.lastTry = map[netip.Addr]time.Time{}
	}
	p.lastTry[peer] = now
	a := p.active
	if a == nil || !a.until.After(now) || a.wrong >= TotalTries {
		p.active = nil
		return NoCode, 0
	}
	spent := a.wrongBy[peer]
	if spent >= Tries {
		return Wrong, 0
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, given)
	if subtle.ConstantTimeCompare([]byte(digits), []byte(a.code)) == 1 {
		// One device per code.
		p.active = nil
		return Right, 0
	}
	a.wrong++
	spent++
	a.wrongBy[peer] = spent
	if a.wrong >= TotalTries {
		p.active = nil
	}
	return Wrong, Tries - spent
}

// Done: the code shown paired name.
func (p *Pairing) Done(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paired = name
}
