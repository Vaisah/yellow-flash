// Ported from Mister Download Manager, crates/geo/src/lib.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

// Package geo says where a download's server is: the country of an IP
// address, from DB-IP's IP to Country Lite database (CC BY 4.0: "IP
// Geolocation by DB-IP", https://db-ip.com), shipped as MDM's compressed
// range table (data/ipcountry.bin.xz, built by MDM's tools/geo/build.py).
// Addresses are looked up here, on this computer; none is ever sent
// anywhere. And the countries' flags, in one picture (data/flags.png,
// lipis/flag-icons, MIT).
//
// The table is unpacked once, on first use (about 3 MB, a few tens of
// milliseconds): call Prepare early, and Lookup answers at once once Ready.
package geo

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"io"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/ulikunitz/xz"
)

//go:embed data/ipcountry.bin.xz
var tableXZ []byte

// FlagsPNG is the flags' picture: Columns a row, each FlagW x FlagH pixels;
// cell i is country index i+1's (Country.Index), in data/countries.txt's
// order, which MDM's build keeps from month to month. Flags cells are
// drawn: a country the data gained since has none.
//
//go:embed data/flags.png
var FlagsPNG []byte

// The flags' picture's layout.
const (
	Flags   = 250
	FlagW   = 32
	FlagH   = 24
	Columns = 16
)

// What the data's licence asks to credit, and where it comes from.
const (
	Attribution    = "IP Geolocation by DB-IP"
	AttributionURL = "https://db-ip.com"
)

// Country is a country of the table.
type Country struct {
	// Index is its place in the table (1 and up): its flag's cell is
	// Index-1.
	Index int
	// Code is its ISO 3166-1 alpha-2 code, upper case ("DE").
	Code string
}

// FlagCell is its flag's place in FlagsPNG, in pixels; false for a country
// newer than the picture.
func (c Country) FlagCell() (x, y int, ok bool) {
	i := c.Index - 1
	if i < 0 || i >= Flags {
		return 0, 0, false
	}
	return (i % Columns) * FlagW, (i / Columns) * FlagH, true
}

// part is one family's ranges: count entries in the unpacked table, and
// every sparseEvery-th entry's first address, offset and number (the first
// one's included), for a binary search.
type part struct {
	count  int
	sparse []sparseEntry
}

type sparseEntry struct {
	first uint64
	at    int
	i     int
}

const sparseEvery = 256

type table struct {
	data   []byte
	codes  []string
	v4, v6 part
}

var (
	once   sync.Once
	loaded *table
	ready  atomic.Bool
)

func leb128(data []byte, at *int) (uint64, bool) {
	var v uint64
	for shift := uint(0); shift <= 63; shift += 7 {
		if *at >= len(data) {
			return 0, false
		}
		b := data[*at]
		*at++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, true
		}
	}
	return 0, false
}

// readPart reads a part's entries from at: the sparse index, and where the
// part ends.
func readPart(data []byte, at *int) (part, bool) {
	if *at+4 > len(data) {
		return part{}, false
	}
	count := int(binary.LittleEndian.Uint32(data[*at:]))
	*at += 4
	p := part{count: count, sparse: make([]sparseEntry, 0, count/sparseEvery+1)}
	var first uint64
	for i := 0; i < count; i++ {
		entry := *at
		d, ok := leb128(data, at)
		if !ok {
			return part{}, false
		}
		first += d
		*at++ // the country index
		if i%sparseEvery == 0 {
			p.sparse = append(p.sparse, sparseEntry{first, entry, i})
		}
	}
	return p, *at <= len(data)
}

func load() *table {
	r, err := xz.NewReader(bytes.NewReader(tableXZ))
	if err != nil {
		return nil
	}
	data, err := io.ReadAll(r)
	if err != nil || len(data) < 8 || string(data[:7]) != "MDMGEO\x01" {
		return nil
	}
	n := int(data[7])
	if len(data) < 8+2*n {
		return nil
	}
	t := &table{data: data}
	for i := 0; i < n; i++ {
		t.codes = append(t.codes, string(data[8+2*i:10+2*i]))
	}
	at := 8 + 2*n
	var ok bool
	if t.v4, ok = readPart(data, &at); !ok {
		return nil
	}
	if t.v6, ok = readPart(data, &at); !ok {
		return nil
	}
	return t
}

func get() *table {
	once.Do(func() {
		loaded = load()
		ready.Store(true)
	})
	return loaded
}

// Prepare unpacks the table now, in the background, if it isn't yet.
func Prepare() {
	if !ready.Load() {
		go get()
	}
}

// Ready reports whether Lookup answers at once.
func Ready() bool { return ready.Load() }

// find is the country index of key in p: the last range starting at or
// before it (0: none).
func (t *table) find(p part, key uint64) int {
	if p.count == 0 {
		return 0
	}
	s := sort.Search(len(p.sparse), func(i int) bool { return p.sparse[i].first > key })
	if s == 0 {
		return 0
	}
	e := p.sparse[s-1]
	first, at, i := e.first, e.at, e.i
	// The sparse entry's own first address is absolute; walk on from it.
	if _, ok := leb128(t.data, &at); !ok || at >= len(t.data) {
		return 0
	}
	idx := int(t.data[at])
	at++
	for i++; i < p.count; i++ {
		q := at
		d, ok := leb128(t.data, &q)
		if !ok || q >= len(t.data) {
			break
		}
		next := first + d
		if next > key {
			break
		}
		first, idx, at = next, int(t.data[q]), q+1
	}
	return idx
}

// Local reports an address of this computer or a local network (or a
// reserved one): none has a country.
func Local(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsLinkLocalMulticast() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

// Lookup is ip's country; false for an address of this computer or a local
// network, a reserved one, or one the table doesn't place. It unpacks the
// table on first use (see Prepare).
func Lookup(ip netip.Addr) (Country, bool) {
	ip = ip.Unmap()
	if !ip.IsValid() || Local(ip) {
		return Country{}, false
	}
	t := get()
	if t == nil {
		return Country{}, false
	}
	var idx int
	if ip.Is4() {
		b := ip.As4()
		idx = t.find(t.v4, uint64(binary.BigEndian.Uint32(b[:])))
	} else {
		b := ip.As16()
		idx = t.find(t.v6, binary.BigEndian.Uint64(b[:8]))
	}
	if idx < 1 || idx > len(t.codes) {
		return Country{}, false
	}
	return Country{Index: idx, Code: t.codes[idx-1]}, true
}

// Countries is the table's countries' codes, in its order (index 1 first).
func Countries() []string {
	if t := get(); t != nil {
		return append([]string(nil), t.codes...)
	}
	return nil
}
