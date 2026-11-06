// Package network provides the hardened HTTP client, metadata probing and
// HTTP range helpers used by the download engine.
package network

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrUnsafeURL is returned for URLs the engine refuses to fetch.
	ErrUnsafeURL = errors.New("unsafe URL")
	// ErrBlockedAddress is returned when a connection would reach a loopback,
	// private, link-local or otherwise non-public address while that is
	// disallowed.
	ErrBlockedAddress = errors.New("destination address is not allowed")
	// ErrTooManyRedirects is returned when the redirect limit is exceeded.
	ErrTooManyRedirects = errors.New("too many redirects")
)

const maxURLLength = 8192

// Options configures NewClient. Zero values select safe defaults.
type Options struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxRedirects          int
	MaxConnsPerHost       int
	// AllowPrivate permits loopback, private and link-local destinations.
	// Leave it false for untrusted (for example browser-originated) URLs.
	AllowPrivate bool
}

func (o Options) withDefaults() Options {
	if o.DialTimeout <= 0 {
		o.DialTimeout = 15 * time.Second
	}
	if o.TLSHandshakeTimeout <= 0 {
		o.TLSHandshakeTimeout = 15 * time.Second
	}
	if o.ResponseHeaderTimeout <= 0 {
		o.ResponseHeaderTimeout = 30 * time.Second
	}
	if o.IdleConnTimeout <= 0 {
		o.IdleConnTimeout = 90 * time.Second
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = 10
	}
	if o.MaxConnsPerHost <= 0 {
		o.MaxConnsPerHost = 32
	}
	return o
}

// NewClient returns an HTTP client with bounded timeouts, validated redirects
// and, unless AllowPrivate is set, a dial-time address policy. The policy is
// checked against the resolved IP for every connection, so it also covers
// every redirect hop and DNS rebinding.
//
// HTTP/2 is disabled on purpose: segmented downloads need independent TCP
// connections rather than streams multiplexed over a single connection.
// Transparent decompression is disabled so byte offsets always refer to the
// stored representation.
func NewClient(opts Options) *http.Client {
	opts = opts.withDefaults()
	dialer := &net.Dialer{Timeout: opts.DialTimeout, KeepAlive: 30 * time.Second}
	proxy := http.ProxyFromEnvironment
	if !opts.AllowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			return checkDialAddress(address)
		}
		// A proxy would make the dialed address the proxy's, defeating the
		// policy. Proxy support for untrusted URLs arrives with Phase 5.
		proxy = nil
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport := &http.Transport{
		Proxy:                 proxy,
		DialContext:           dialer.DialContext,
		Protocols:             protocols,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   opts.MaxConnsPerHost,
		MaxConnsPerHost:       opts.MaxConnsPerHost,
		IdleConnTimeout:       opts.IdleConnTimeout,
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
	maxRedirects := opts.MaxRedirects
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w (limit %d)", ErrTooManyRedirects, maxRedirects)
			}
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect rejected: %w", err)
			}
			return nil
		},
	}
}

// ValidateURL accepts only absolute http(s) URLs with a host and without
// embedded credentials.
func ValidateURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLength {
		return nil, fmt.Errorf("%w: empty or too long", ErrUnsafeURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsafeURL, err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme != "http" && scheme != "https":
		return nil, fmt.Errorf("%w: scheme %q is not http or https", ErrUnsafeURL, u.Scheme)
	case u.Opaque != "" || u.Hostname() == "":
		return nil, fmt.Errorf("%w: missing host", ErrUnsafeURL)
	case u.User != nil:
		return nil, fmt.Errorf("%w: embedded credentials are not allowed", ErrUnsafeURL)
	}
	return u, nil
}

// blockedPrefixes are special-purpose ranges not covered by netip helpers.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64 can embed private IPv4
	netip.MustParsePrefix("2002::/16"),    // 6to4 can embed private IPv4
}

// IsPublicAddress reports whether ip is a globally routable unicast address.
func IsPublicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return ip != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func checkDialAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: unresolved host %q", ErrBlockedAddress, host)
	}
	if !IsPublicAddress(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
	}
	return nil
}
