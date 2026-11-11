// Ported from Mister Download Manager, crates/geo/src/lib.rs (tests)
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package geo

import (
	_ "embed"
	"encoding/binary"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

//go:embed data/countries.txt
var countriesTxt string

func code(ip string) string {
	c, ok := Lookup(netip.MustParseAddr(ip))
	if !ok {
		return ""
	}
	return c.Code
}

func TestAddressesHaveTheirCountries(t *testing.T) {
	// Well-known, long-held blocks: DB-IP's sample row (8.8.8.0/24 US), APNIC's
	// 1.0.0.0/24 (AU) and 1.0.1.0/24 (CN).
	for ip, want := range map[string]string{"8.8.8.8": "US", "1.0.0.1": "AU", "1.0.2.3": "CN", "::ffff:8.8.8.8": "US"} {
		if got := code(ip); got != want {
			t.Errorf("%s: %q, want %s", ip, got, want)
		}
	}
	// An IPv6 address is placed (which country moves with the monthly data).
	if code("2001:4860:4860::8888") == "" {
		t.Error("IPv6 unplaced")
	}
	if !Ready() {
		t.Error("ready after a lookup")
	}
}

func TestThisComputerAndLocalNetworksHaveNone(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.1.1", "::1", "fe80::1", "fd00::1", "0.0.0.0"} {
		if c := code(ip); c != "" {
			t.Errorf("%s: %s", ip, c)
		}
	}
}

func TestTheFlagsFollowTheTablesOrder(t *testing.T) {
	order := strings.Fields(countriesTxt)
	if len(order) != Flags {
		t.Fatal(len(order))
	}
	table := Countries()
	if len(table) < len(order) || !slices.Equal(table[:len(order)], order) {
		t.Fatal("the table's countries start with countries.txt's")
	}
	rows := (Flags + Columns - 1) / Columns
	x, y, ok := Country{Index: Flags, Code: "ZW"}.FlagCell()
	if !ok || x+FlagW > Columns*FlagW || y+FlagH > rows*FlagH {
		t.Fatal(x, y, ok)
	}
	if _, _, ok := (Country{Index: Flags + 1, Code: "XX"}).FlagCell(); ok {
		t.Fatal("one newer than the picture has no flag")
	}
	// The picture is a PNG of that size.
	if string(FlagsPNG[1:4]) != "PNG" || binary.BigEndian.Uint32(FlagsPNG[16:20]) != Columns*FlagW ||
		binary.BigEndian.Uint32(FlagsPNG[20:24]) != uint32(rows*FlagH) {
		t.Fatal("flags.png")
	}
}

// Every range's lookup at its own first address gives its own country (the
// sparse index and the walk agree).
func TestTheWholeSpaceIsWalkedInOrder(t *testing.T) {
	tb := get()
	if tb == nil {
		t.Fatal("the table loads")
	}
	for _, p := range []part{tb.v4, tb.v6} {
		if len(p.sparse) == 0 {
			continue
		}
		at := p.sparse[0].at
		var first uint64
		for i := 0; i < min(p.count, 5000); i++ {
			d, _ := leb128(tb.data, &at)
			first += d
			idx := int(tb.data[at])
			at++
			if got := tb.find(p, first); got != idx {
				t.Fatalf("entry %d: %d, want %d", i, got, idx)
			}
		}
	}
}
