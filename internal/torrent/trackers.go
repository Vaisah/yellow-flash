// Ported from Mister Download Manager, crates/torrent/src/trackers.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

// Which of a torrent's trackers may be contacted. A torrent names its
// trackers, and a tracker on this computer or the local network (127.0.0.1,
// 192.168.1.1, localhost) lets a torrent make Yellow Flash send requests
// into the network it sits in: those are dropped. With a proxy, UDP
// trackers are dropped too: they would go around the proxy.
//
// The list is changed in the torrent itself (announce, announce-list,
// outside the info dictionary, so its hash stays the same) and in a magnet
// link (tr=).

import (
	"bytes"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// TrackerAllowed reports whether tracker may be contacted.
func TrackerAllowed(tracker string, proxied bool) bool {
	u, err := url.Parse(strings.TrimSpace(tracker))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "udp":
		if proxied {
			return false
		}
	default:
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return publicAddr(ip)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	// A name whose last label is a number (0x7f.1, 2130706433) is an
	// address some resolvers read as one: no real domain ends so.
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if _, err := strconv.ParseUint(last, 0, 64); err == nil || strings.HasPrefix(last, "0x") {
		return false
	}
	return true
}

// publicAddr reports an address on the internet (not this computer, the
// local network or a reserved range).
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip.Is4() {
		a := ip.As4()
		// 255.255.255.255, and 100.64.0.0/10, carrier-grade NAT.
		return a != [4]byte{255, 255, 255, 255} && !(a[0] == 100 && a[1]&0xc0 == 64)
	}
	return true
}

// magnetParams splits a magnet link's query into its parameters, in order.
func magnetParams(magnet string) (head string, params []string, ok bool) {
	head, query, ok := strings.Cut(magnet, "?")
	if !ok {
		return magnet, nil, false
	}
	return head, strings.Split(query, "&"), true
}

// TrackersInMagnet are a magnet link's trackers (tr=), each once, in its
// order.
func TrackersInMagnet(magnet string) []string {
	_, params, _ := magnetParams(magnet)
	var out []string
	for _, kv := range params {
		k, v, _ := strings.Cut(kv, "=")
		if !strings.EqualFold(k, "tr") {
			continue
		}
		v, err := url.QueryUnescape(v)
		v = strings.TrimSpace(v)
		if err == nil && v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// FilterMagnet is a magnet link with only the trackers that may be
// contacted.
func FilterMagnet(magnet string, proxied bool) string {
	head, params, ok := magnetParams(magnet)
	if !ok {
		return magnet
	}
	var kept []string
	for _, kv := range params {
		k, v, found := strings.Cut(kv, "=")
		if found && strings.EqualFold(k, "tr") {
			v, err := url.QueryUnescape(v)
			if err != nil || !TrackerAllowed(v, proxied) {
				continue
			}
		}
		kept = append(kept, kv)
	}
	return head + "?" + strings.Join(kept, "&")
}

// FilterTorrent is a .torrent with only the trackers that may be
// contacted; everything else byte for byte, so its info hash is the same.
// False when it isn't a bencoded dictionary.
func FilterTorrent(b []byte, proxied bool) ([]byte, bool) {
	entries, ok := topLevel(b)
	if !ok {
		return nil, false
	}
	out := []byte{'d'}
	for _, e := range entries {
		switch string(e.key) {
		case "announce":
			u, ok := stringAt(e.value)
			if !ok {
				return nil, false
			}
			if TrackerAllowed(string(u), proxied) {
				out = pushString(out, e.key)
				out = append(out, e.value...)
			}
		case "announce-list":
			tiers, ok := listOfLists(e.value)
			if !ok {
				return nil, false
			}
			var kept [][][]byte
			for _, tier := range tiers {
				var t [][]byte
				for _, u := range tier {
					if TrackerAllowed(string(u), proxied) {
						t = append(t, u)
					}
				}
				if len(t) > 0 {
					kept = append(kept, t)
				}
			}
			if len(kept) > 0 {
				out = pushString(out, e.key)
				out = appendTiers(out, kept)
			}
		default:
			out = pushString(out, e.key)
			out = append(out, e.value...)
		}
	}
	return append(out, 'e'), true
}

// TrackersListed are a .torrent's trackers, each once, in its order:
// announce-list's tiers, or announce when it has no list (BEP 12: a list
// replaces it).
func TrackersListed(b []byte) []string {
	entries, ok := topLevel(b)
	if !ok {
		return nil
	}
	value := func(k string) ([]byte, bool) {
		for _, e := range entries {
			if string(e.key) == k {
				return e.value, true
			}
		}
		return nil, false
	}
	var urls [][]byte
	if v, ok := value("announce-list"); ok {
		if tiers, ok := listOfLists(v); ok {
			for _, t := range tiers {
				urls = append(urls, t...)
			}
		}
	}
	if len(urls) == 0 {
		if v, ok := value("announce"); ok {
			if s, ok := stringAt(v); ok {
				urls = append(urls, s)
			}
		}
	}
	var out []string
	for _, u := range urls {
		s := strings.TrimSpace(string(u))
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// AddTrackers is a .torrent with more trackers (each a tier of its own,
// after the ones it has; those it has already are skipped); everything else
// byte for byte, so its info hash is the same. One with only announce gets
// a list holding it first; one with none gets announce too. False when it
// isn't a bencoded dictionary.
func AddTrackers(b []byte, more []string) ([]byte, bool) {
	entries, ok := topLevel(b)
	if !ok {
		return nil, false
	}
	have := TrackersListed(b)
	var fresh []string
	for _, u := range more {
		u = strings.TrimSpace(u)
		if u != "" && !slices.Contains(have, u) && !slices.Contains(fresh, u) {
			fresh = append(fresh, u)
		}
	}
	if len(fresh) == 0 {
		return bytes.Clone(b), true
	}
	var tiers [][][]byte
	listedTiers := false
	for _, e := range entries {
		if string(e.key) == "announce-list" {
			t, ok := listOfLists(e.value)
			if !ok {
				return nil, false
			}
			if len(t) > 0 {
				tiers, listedTiers = t, true
			}
		}
	}
	if !listedTiers {
		for _, u := range have {
			tiers = append(tiers, [][]byte{[]byte(u)})
		}
	}
	for _, u := range fresh {
		tiers = append(tiers, [][]byte{[]byte(u)})
	}
	type kv struct{ k, v []byte }
	var out []kv
	hasAnnounce := false
	for _, e := range entries {
		if string(e.key) == "announce-list" {
			continue
		}
		hasAnnounce = hasAnnounce || string(e.key) == "announce"
		out = append(out, kv{e.key, e.value})
	}
	out = append(out, kv{[]byte("announce-list"), appendTiers(nil, tiers)})
	if !hasAnnounce {
		out = append(out, kv{[]byte("announce"), pushString(nil, []byte(fresh[0]))})
	}
	// A dictionary's keys are sorted (bencode); the info dictionary's bytes
	// are untouched.
	slices.SortStableFunc(out, func(a, b kv) int { return bytes.Compare(a.k, b.k) })
	done := []byte{'d'}
	for _, e := range out {
		done = pushString(done, e.k)
		done = append(done, e.v...)
	}
	return append(done, 'e'), true
}

func appendTiers(out []byte, tiers [][][]byte) []byte {
	out = append(out, 'l')
	for _, tier := range tiers {
		out = append(out, 'l')
		for _, u := range tier {
			out = pushString(out, u)
		}
		out = append(out, 'e')
	}
	return append(out, 'e')
}

func pushString(out, s []byte) []byte {
	out = strconv.AppendInt(out, int64(len(s)), 10)
	out = append(out, ':')
	return append(out, s...)
}

type entry struct{ key, value []byte }

// topLevel is the top-level dictionary's entries: each key and its value's
// bytes.
func topLevel(b []byte) ([]entry, bool) {
	if len(b) == 0 || b[0] != 'd' {
		return nil, false
	}
	at := 1
	var out []entry
	for {
		if at >= len(b) {
			return nil, false
		}
		if b[at] == 'e' {
			break
		}
		key, next, ok := bstring(b, at)
		if !ok {
			return nil, false
		}
		end, ok := skip(b, next, 0)
		if !ok {
			return nil, false
		}
		out = append(out, entry{key, b[next:end]})
		at = end
	}
	return out, at+1 == len(b)
}

// bstring is a string at at: its bytes, and where it ends.
func bstring(b []byte, at int) ([]byte, int, bool) {
	if at >= len(b) {
		return nil, 0, false
	}
	colon := bytes.IndexByte(b[at:], ':')
	if colon < 0 {
		return nil, 0, false
	}
	colon += at
	n, err := strconv.ParseUint(string(b[at:colon]), 10, 32)
	if err != nil {
		return nil, 0, false
	}
	start := colon + 1
	end := start + int(n)
	if end > len(b) || end < start {
		return nil, 0, false
	}
	return b[start:end], end, true
}

func stringAt(v []byte) ([]byte, bool) {
	s, end, ok := bstring(v, 0)
	return s, ok && end == len(v)
}

func listOfLists(v []byte) ([][][]byte, bool) {
	if len(v) == 0 || v[0] != 'l' {
		return nil, false
	}
	at := 1
	var tiers [][][]byte
	for {
		if at >= len(v) {
			return nil, false
		}
		if v[at] == 'e' {
			return tiers, true
		}
		if v[at] != 'l' {
			return nil, false
		}
		at++
		var tier [][]byte
		for {
			if at >= len(v) {
				return nil, false
			}
			if v[at] == 'e' {
				break
			}
			s, next, ok := bstring(v, at)
			if !ok {
				return nil, false
			}
			tier = append(tier, s)
			at = next
		}
		at++
		tiers = append(tiers, tier)
	}
}

// skip is where the value at at ends; nesting is limited.
func skip(b []byte, at, depth int) (int, bool) {
	if depth > 64 || at >= len(b) {
		return 0, false
	}
	switch c := b[at]; {
	case c == 'i':
		e := bytes.IndexByte(b[at:], 'e')
		if e < 0 {
			return 0, false
		}
		return at + e + 1, true
	case c == 'l' || c == 'd':
		at++
		for {
			if at >= len(b) {
				return 0, false
			}
			if b[at] == 'e' {
				return at + 1, true
			}
			next, ok := skip(b, at, depth+1)
			if !ok {
				return 0, false
			}
			at = next
		}
	case c >= '0' && c <= '9':
		_, end, ok := bstring(b, at)
		return end, ok
	}
	return 0, false
}
