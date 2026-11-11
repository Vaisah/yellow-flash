// Ported from Mister Download Manager, crates/app/src/whereis.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package main

// Where a download's server is, for Download Info and Properties: the
// country (internal/geo: DB-IP's offline table, looked up on this
// computer, never asked of anyone) and its flag, or this computer / the
// local network; the round trip the download's own connections measured,
// near / medium / far; a CDN or a proxy said as such; and DB-IP's credit,
// which its licence asks for wherever a country is shown.

import (
	"encoding/base64"
	"net/netip"

	"github.com/vaisah/internal/geo"
)

// ServerLocation is where a download's server is.
type ServerLocation struct {
	// Place is "country", "this-computer", "local-network" or "unknown"
	// (no address seen, or one the table doesn't place).
	Place string `json:"place"`
	IP    string `json:"ip,omitempty"`
	// Country is its ISO 3166-1 code ("DE"); the window names it in its
	// language.
	Country string `json:"country,omitempty"`
	// Flag is the flag's cell in FlagsPicture (nil: none).
	Flag *FlagCell `json:"flag,omitempty"`
	// RTTMillis is the round trip; Reach "near" (under 60 ms), "medium"
	// (under 150 ms), "far", or "" when it wasn't measured.
	RTTMillis int64  `json:"rttMs,omitempty"`
	Reach     string `json:"reach,omitempty"`
	// Proxied: the address and round trip are the proxy's. CDN: the address
	// is one of the CDN's servers near this computer.
	Proxied bool   `json:"proxied,omitempty"`
	CDN     string `json:"cdn,omitempty"`
	// Attribution and AttributionURL are DB-IP's credit.
	Attribution    string `json:"attribution"`
	AttributionURL string `json:"attributionUrl"`
}

// FlagCell is a flag's place in the flags' picture, in pixels.
type FlagCell struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// reach is how far a server is, by its round trip: under 60 ms near (the
// same country or next door), under 150 ms medium (the same continent),
// else far.
func reach(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 60:
		return "near"
	case ms < 150:
		return "medium"
	}
	return "far"
}

// locate is where a server address is.
func locate(ip string, rttMs int64, proxied bool, cdn string) ServerLocation {
	loc := ServerLocation{Place: "unknown", IP: ip, RTTMillis: rttMs, Reach: reach(rttMs), Proxied: proxied, CDN: cdn,
		Attribution: geo.Attribution, AttributionURL: geo.AttributionURL}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return loc
	}
	addr = addr.Unmap()
	switch c, ok := geo.Lookup(addr); {
	case ok:
		loc.Place, loc.Country = "country", c.Code
		if x, y, ok := c.FlagCell(); ok {
			loc.Flag = &FlagCell{X: x, Y: y, W: geo.FlagW, H: geo.FlagH}
		}
	case addr.IsLoopback():
		loc.Place = "this-computer"
	case geo.Local(addr):
		loc.Place = "local-network"
	}
	return loc
}

// ServerLocation is where a download's server is (as its last run saw it).
func (a *App) ServerLocation(id string) (ServerLocation, error) {
	d, err := a.GetDownload(id)
	if err != nil {
		return ServerLocation{}, err
	}
	if d.Server == nil {
		return locate("", 0, false, ""), nil
	}
	return locate(d.Server.IP, d.Server.RTTMillis, d.Server.Proxied, d.Server.CDN), nil
}

// FlagsPicture is the flags' picture, as a data: URL for the window.
func (a *App) FlagsPicture() string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(geo.FlagsPNG)
}
