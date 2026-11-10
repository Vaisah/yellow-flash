// Ported from Mister Download Manager, crates/torrent/src/blocklist.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package torrent

// IP blocklists for torrents. The usual lists come as PeerGuardian/P2P text
// (name:1.2.3.4-1.2.3.255) or eMule's .dat (001.002.003.004 -
// 001.002.003.255 , 000 , name), often gzipped. A list is converted here to
// plain P2P lines that have been checked, and its ranges.

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

// maxUnpacked is the most a gzipped list may unpack to (the largest public
// lists are a few tens of MB).
const maxUnpacked = 256 << 20

// IPRange is a blocked range, both ends included.
type IPRange struct{ Start, End netip.Addr }

// Blocklist is a list, converted.
type Blocklist struct {
	// Text is P2P lines (yf:start-end), one per blocked range.
	Text   string
	Ranges []IPRange
	// Skipped are lines that weren't a range, and eMule ranges marked
	// allowed (level > 127).
	Skipped int
}

// ConvertBlocklist reads b (a list, gzipped or not); false when no line is
// a range.
func ConvertBlocklist(b []byte) (Blocklist, bool) {
	raw := b
	if bytes.HasPrefix(b, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return Blocklist{}, false
		}
		raw, err = io.ReadAll(io.LimitReader(zr, maxUnpacked))
		if err != nil && len(raw) == 0 {
			return Blocklist{}, false
		}
	}
	var out Blocklist
	var text strings.Builder
	for _, line := range strings.Split(strings.ToValidUTF8(string(raw), "�"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		r, blocked, ok := blockRange(line)
		if !ok || !blocked {
			out.Skipped++
			continue
		}
		text.WriteString("yf:" + r.Start.String() + "-" + r.End.String() + "\n")
		out.Ranges = append(out.Ranges, r)
	}
	out.Text = text.String()
	return out, len(out.Ranges) > 0
}

// blockRange is one line's range: blocked, or an eMule range marked allowed
// (blocked false); ok false when it isn't a range.
func blockRange(line string) (IPRange, bool, bool) {
	// eMule: "start - end , level , description" (a level above 127 is
	// allowed), told apart from a P2P line whose name has a comma by the
	// range before the first comma.
	var span string
	if s, rest, found := strings.Cut(line, ","); found {
		if a, b, dash := strings.Cut(s, "-"); dash {
			if _, ok := parseIP(a); ok {
				if _, ok := parseIP(b); ok {
					level, _, _ := strings.Cut(rest, ",")
					if n, err := strconv.Atoi(strings.TrimSpace(level)); err == nil && n > 127 {
						return IPRange{}, false, true
					}
					span = s
				}
			}
		}
	}
	if span == "" {
		if strings.Count(line, ":") > 2 {
			// P2P with IPv6 addresses: the name ends at the first colon.
			_, rest, ok := strings.Cut(line, ":")
			if !ok {
				return IPRange{}, false, false
			}
			span = rest
		} else {
			i := strings.LastIndexByte(line, ':')
			if i < 0 {
				return IPRange{}, false, false
			}
			span = line[i+1:]
		}
	}
	a, b, ok := strings.Cut(span, "-")
	if !ok {
		return IPRange{}, false, false
	}
	start, ok1 := parseIP(a)
	end, ok2 := parseIP(b)
	if !ok1 || !ok2 || start.Is4() != end.Is4() || end.Less(start) {
		return IPRange{}, false, false
	}
	return IPRange{start, end}, true, true
}

// parseIP reads an address, IPv4's parts possibly zero-padded
// (001.002.003.004).
func parseIP(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, ":") {
		a, err := netip.ParseAddr(s)
		return a, err == nil && a.Is6()
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return netip.Addr{}, false
	}
	var b [4]byte
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return netip.Addr{}, false
		}
		b[i] = byte(n)
	}
	return netip.AddrFrom4(b), true
}

// Blocks reports whether ip is in one of the list's ranges.
func (l Blocklist) Blocks(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, r := range l.Ranges {
		if ip.Is4() == r.Start.Is4() && !ip.Less(r.Start) && !r.End.Less(ip) {
			return true
		}
	}
	return false
}
