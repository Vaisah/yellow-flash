// Package network provides the hardened HTTP client, metadata probing and
// HTTP range helpers used by the download engine.
package network

import (
	"context"
	"crypto/x509"
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
	// DNS is the resolver names are looked up with; nil is the system's.
	DNS *DNSChoice
	// PACScript is a proxy auto-config script's text: its
	// FindProxyForURL picks each request's proxy. Empty: the environment's
	// proxy settings. Without AllowPrivate a proxy still applies, and each
	// target is checked before it is handed to the proxy (guardedProxy).
	PACScript string
	// RootCAs verifies FTPS servers; nil uses the system's.
	RootCAs *x509.CertPool
	// Proxy is "" for the system's proxy settings, "direct" for none, or a
	// fixed proxy ("http://host:port", "https://…", "socks5://…"). A
	// PACScript wins over it.
	Proxy string
	// FTPActive makes FTP data connections active (PORT/EPRT) instead of
	// passive.
	FTPActive bool
}

// ParseProxy checks a fixed proxy address.
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: not a proxy address: %q", ErrUnsafeURL, raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	}
	return nil, fmt.Errorf("%w: proxy scheme %q (use http, https or socks5)", ErrUnsafeURL, u.Scheme)
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
	switch p := strings.TrimSpace(opts.Proxy); {
	case opts.PACScript != "":
		proxy = pacProxy(opts.PACScript)
	case strings.EqualFold(p, "direct"):
		proxy = nil
	case p != "":
		if u, err := ParseProxy(p); err == nil {
			proxy = http.ProxyURL(u)
		}
	}
	// Untrusted addresses through a proxy: the proxy is the user's own
	// setting (often on this computer), so connecting to it is allowed; the
	// target is checked before the request is handed to it (guardedProxy).
	var guarded func(*http.Request) (*url.URL, error)
	if !opts.AllowPrivate {
		dialer.ControlContext = func(ctx context.Context, _, address string, _ syscall.RawConn) error {
			if _, viaProxy := ctx.Value(proxiedKey{}).(*url.URL); viaProxy {
				return nil
			}
			return checkDialAddress(address)
		}
		if proxy != nil {
			guarded, proxy = proxy, proxyChosen
		}
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	var resolver *Resolver
	if !opts.DNS.IsSystem() {
		resolver = NewResolver(*opts.DNS)
	}
	// Names through the chosen DNS; a host's several addresses race.
	hosts := &hostDialer{base: dialer, dns: resolver}
	transport := &http.Transport{
		Proxy:                 proxy,
		DialContext:           hosts.DialContext,
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
	// FTP, FTPS and FTPES through the same dialer: the chosen DNS, the
	// address race and the dial policy apply to them too.
	ftp := newFTPTransport(hosts.DialContext, opts.RootCAs)
	ftp.active = opts.FTPActive
	for _, scheme := range []string{"ftp", "ftps", "ftpes"} {
		transport.RegisterProtocol(scheme, ftp)
	}
	var rt http.RoundTripper = transport
	if guarded != nil {
		rt = &guardedProxy{base: transport, proxy: guarded, dns: resolver}
	}
	maxRedirects := opts.MaxRedirects
	return &http.Client{
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("%w (limit %d)", ErrTooManyRedirects, maxRedirects)
			}
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect rejected: %w", err)
			}
			if IsFTP(req.URL) {
				return fmt.Errorf("redirect rejected: %w: a web server may not send a download to FTP", ErrUnsafeURL)
			}
			keepCredentialsWithinParty(req, via)
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
	_, hasPassword := u.User.Password()
	switch {
	case scheme != "http" && scheme != "https" && !IsFTP(u):
		return nil, fmt.Errorf("%w: scheme %q is not http, https or ftp", ErrUnsafeURL, u.Scheme)
	case u.Opaque != "" || u.Hostname() == "":
		return nil, fmt.Errorf("%w: missing host", ErrUnsafeURL)
	// An FTP address may name its user; a password never stays in an
	// address (Create moves it to a secret header).
	case u.User != nil && (!IsFTP(u) || hasPassword):
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

// proxiedKey marks a request's context with the proxy guardedProxy chose
// for it: the transport sends it there, and connecting to it is allowed.
type proxiedKey struct{}

// proxyChosen is the transport's proxy when guardedProxy decides: the one
// it recorded, or none.
func proxyChosen(req *http.Request) (*url.URL, error) {
	u, _ := req.Context().Value(proxiedKey{}).(*url.URL)
	return u, nil
}

// guardedProxy picks each untrusted request's proxy once and, when there is
// one, checks the target before handing the request over: the dial policy
// sees only the proxy's address then. Other schemes (FTP) never use the
// proxy and stay under the dial policy.
type guardedProxy struct {
	base  http.RoundTripper
	proxy func(*http.Request) (*url.URL, error)
	dns   *Resolver
}

func (g *guardedProxy) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return g.base.RoundTrip(req)
	}
	p, err := g.proxy(req)
	if err == nil && p != nil {
		err = checkProxiedTarget(req.Context(), req.URL.Hostname(), p, g.dns)
	}
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if p == nil {
		return g.base.RoundTrip(req)
	}
	return g.base.RoundTrip(req.WithContext(context.WithValue(req.Context(), proxiedKey{}, p)))
}

// checkProxiedTarget refuses a target that is, or is named as, a loopback,
// private or link-local address. Names are looked up here too, except
// through a socks5h proxy, which is chosen so the proxy alone resolves them;
// a name that doesn't resolve here is left to the proxy.
func checkProxiedTarget(ctx context.Context, host string, proxy *url.URL, dns *Resolver) error {
	if host == "" {
		return fmt.Errorf("%w: no host", ErrBlockedAddress)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublicAddress(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
		}
		return nil
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
	}
	if strings.EqualFold(proxy.Scheme, "socks5h") {
		return nil
	}
	ips, err := dns.LookupIP(ctx, host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if !IsPublicAddress(ip) {
			return fmt.Errorf("%w: %s is %s", ErrBlockedAddress, host, ip)
		}
	}
	return nil
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
