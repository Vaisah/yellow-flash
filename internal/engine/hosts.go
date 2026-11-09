// Ported from Mister Download Manager, crates/engine/src/hosts.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"sort"
	"sync"
	"time"

	domain "github.com/vaisah/internal/domain/download"
)

const (
	// hostLimitDecay is how long a learned cap lasts without being seen
	// again.
	hostLimitDecay = 10 * time.Minute
	// hostLimitNeeded refusals are needed before a cap applies: one can be
	// a hiccup.
	hostLimitNeeded = 2
)

// hostLimits learns per-host connection limits. When a host refuses an
// extra connection while others to it are receiving data, it allows at
// least those: the most ever seen working at a refusal is the cap. (Keeping
// the smallest count instead learned 1 for a host that allows 2, in MDM's
// benchmarks.) After hostLimitNeeded observations the cap applies, to the
// running download and to later ones, instead of every extra connection
// spending its retries (wasted time, and a risk of being banned).
type hostLimits struct {
	mu    sync.Mutex
	hosts map[string]domain.HostLimit
	now   func() time.Time
	decay time.Duration
}

func newHostLimits(decay time.Duration) *hostLimits {
	return &hostLimits{hosts: map[string]domain.HostLimit{}, now: time.Now, decay: decay}
}

// load adopts persisted limits that have not decayed.
func (h *hostLimits) load(saved []domain.HostLimit) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range saved {
		if l.Host != "" && l.MaxConnections > 0 && h.now().Sub(l.UpdatedAt) < h.decay {
			h.hosts[l.Host] = l
		}
	}
}

// limit returns the connections host is known to allow.
func (h *hostLimits) limit(host string) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.hosts[host]
	switch {
	case !ok:
		return 0, false
	case h.now().Sub(l.UpdatedAt) >= h.decay:
		delete(h.hosts, host)
		return 0, false
	case l.Seen >= hostLimitNeeded:
		return l.MaxConnections, true
	}
	return 0, false
}

// refused records that an extra connection to host was refused while
// working others were receiving data. It returns the updated record, and
// whether the cap now applies.
func (h *hostLimits) refused(host string, working int) (domain.HostLimit, bool) {
	working = max(working, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	l, ok := h.hosts[host]
	if !ok || now.Sub(l.UpdatedAt) >= h.decay {
		l = domain.HostLimit{Host: host, MaxConnections: working}
	}
	l.MaxConnections = max(l.MaxConnections, working)
	l.Seen++
	l.UpdatedAt = now
	h.hosts[host] = l
	return l, l.Seen >= hostLimitNeeded
}

// working records that n connections to host are receiving data at once:
// a learned cap below that was too low (a refusal can arrive before an
// accepted connection's first byte), so it is raised. It returns the
// updated record when it changed.
func (h *hostLimits) working(host string, n int) (domain.HostLimit, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.hosts[host]
	if !ok || n <= l.MaxConnections {
		return domain.HostLimit{}, false
	}
	l.MaxConnections = n
	h.hosts[host] = l
	return l, true
}

// learned lists every cap that applies, sorted by host.
func (h *hostLimits) learned() []domain.HostLimit {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []domain.HostLimit
	for _, l := range h.hosts {
		if l.Seen >= hostLimitNeeded && h.now().Sub(l.UpdatedAt) < h.decay {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}
