// Ported from Mister Download Manager, crates/remote/src/addrs.rs
// Copyright (C) 2026 Ahmed Nefzaoui and contributors; Go port (C) 2026 the Yellow Flash contributors.
// Translated from Rust to Go and modified. SPDX-License-Identifier: GPL-3.0-or-later

package remote

// Where the remote control listens: the computer's local network
// addresses, one listener each, never every address at once (0.0.0.0 or
// ::), so an adapter with a public address (a modem plugged in directly, a
// VPN's) is never reached by surprise.

import (
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
)

// ThisComputer is Chosen's choice for this computer only: other devices
// can't reach it (tests, a reverse proxy on the same computer).
const ThisComputer = "127.0.0.1"

// Adapter is a network adapter and its addresses.
type Adapter struct {
	Name string
	IPs  []netip.Addr
}

// Adapters are the computer's network adapters that are up.
func Adapters() []Adapter {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Adapter
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		a := Adapter{Name: i.Name}
		for _, ad := range addrs {
			if p, err := netip.ParsePrefix(ad.String()); err == nil {
				a.IPs = append(a.IPs, p.Addr().Unmap())
			}
		}
		out = append(out, a)
	}
	return out
}

// IsLocalNetwork reports a private address the internet can't reach:
// IPv4's 10/8, 172.16/12 and 192.168/16, and IPv6's unique local fc00::/7.
func IsLocalNetwork(ip netip.Addr) bool {
	return ip.Unmap().IsPrivate()
}

// Chosen are the addresses to listen on for listen: "" every local network
// address of adapters; an address (this computer's 127.0.0.1 or ::1, or
// one of an adapter's, even a public one: the person chose it); an
// adapter's name, its addresses. Nothing when the choice isn't there now,
// and never an unspecified address.
func Chosen(listen string, adapters []Adapter) []netip.Addr {
	listen = strings.TrimSpace(listen)
	var out []netip.Addr
	switch ip, err := netip.ParseAddr(strings.Trim(listen, "[]")); {
	case listen == "":
		for _, a := range adapters {
			for _, ip := range a.IPs {
				if IsLocalNetwork(ip) {
					out = append(out, ip)
				}
			}
		}
	case err == nil:
		ip = ip.Unmap()
		if ip.IsUnspecified() {
			return nil
		}
		if ip.IsLoopback() || slices.ContainsFunc(adapters, func(a Adapter) bool { return slices.Contains(a.IPs, ip) }) {
			out = append(out, ip)
		}
	default:
		for _, a := range adapters {
			if strings.EqualFold(a.Name, listen) {
				out = append(out, a.IPs...)
			}
		}
	}
	// IPv6 link-local addresses need a zone to be listened on: left out.
	out = slices.DeleteFunc(out, func(ip netip.Addr) bool { return ip.IsLinkLocalUnicast() || ip.IsUnspecified() })
	slices.SortFunc(out, func(a, b netip.Addr) int { return a.Compare(b) })
	return slices.Compact(out)
}

// Rank is how likely a phone reaches ip, best first: a home network's
// 192.168/16, then 10/8, then 172.16/12 (often a virtual machine's or a
// container's own network), IPv6, this computer.
func Rank(ip netip.Addr) int {
	switch {
	case ip.Is4() && ip.As4()[0] == 192:
		return 0
	case ip.Is4() && ip.As4()[0] == 10:
		return 1
	case ip.Is4() && ip.IsPrivate():
		return 2
	case ip.Is4() && !ip.IsLoopback():
		return 3
	case ip.Is6() && !ip.IsLoopback():
		return 4
	}
	return 5
}

// URL is the page's address for a listener at ip:port.
func URL(ip netip.Addr, port int) string {
	return "https://" + net.JoinHostPort(ip.String(), strconv.Itoa(port)) + "/"
}

// HostName is the computer's name, for the certificate and the names a
// request may use (Host); "" when unknown.
func HostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > 63 {
		return ""
	}
	return name
}

// HostAllowed reports whether host (a request's Host without its port) is
// a name this server answers to: any address (the one a phone used may be
// the router's forwarding, or a container's host), localhost, the
// computer's name with a local suffix, and the names the person added. Any
// other name is a web page's (DNS rebinding) and is refused.
func HostAllowed(host string, extra []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return true
	}
	if host == "localhost" || slices.ContainsFunc(extra, func(e string) bool { return strings.EqualFold(e, host) }) {
		return true
	}
	own := HostName()
	if own == "" {
		return false
	}
	for _, s := range []string{"", ".local", ".lan", ".home", ".home.arpa", ".localdomain"} {
		if host == own+s {
			return true
		}
	}
	return false
}

// HostOf is a Host header's name, without its port.
func HostOf(header string) string {
	header = strings.TrimSpace(header)
	if strings.HasPrefix(header, "[") {
		if i := strings.IndexByte(header, ']'); i >= 0 {
			return header[:i+1]
		}
		return header
	}
	if i := strings.LastIndexByte(header, ':'); i >= 0 {
		if _, err := strconv.ParseUint(header[i+1:], 10, 16); err == nil {
			return header[:i]
		}
	}
	return header
}
