package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vaisah/internal/infrastructure/network"
)

// NetworkSettings are the connection preferences: speed limits, DNS,
// proxy, FTP and the download-limits data cap. The DNS and proxy choices
// apply when the app next starts (the engine's client is built once); speed
// limits and the data cap apply at once.
type NetworkSettings struct {
	// SpeedLimitKBps caps all downloads together, in KiB/s; 0: none.
	SpeedLimitKBps int64 `json:"speedLimitKBps"`

	// DNSMode is "system", "preset", "plain", "tls" or "https".
	DNSMode string `json:"dnsMode"`
	// DNSPreset is a preset's id ("cloudflare", …) asked in DNSPresetMode
	// ("plain", "tls", "https"); DNSProfile is a NextDNS configuration ID.
	DNSPreset     string `json:"dnsPreset"`
	DNSPresetMode string `json:"dnsPresetMode"`
	DNSProfile    string `json:"dnsProfile"`
	// DNSServers are typed server addresses (plain, tls); DNSTLSName is
	// what a DoT server's certificate shows; DNSURL is a DoH address.
	DNSServers  string `json:"dnsServers"`
	DNSTLSName  string `json:"dnsTlsName"`
	DNSURL      string `json:"dnsUrl"`
	DNSFallback bool   `json:"dnsFallback"`

	// ProxyMode is "system", "none", "fixed" or "pac".
	ProxyMode string `json:"proxyMode"`
	ProxyURL  string `json:"proxyUrl"`
	// PACURL is where the auto-config script is fetched at startup;
	// PACScript is its text when given directly.
	PACURL    string `json:"pacUrl"`
	PACScript string `json:"pacScript"`

	FTPActive bool `json:"ftpActive"`

	// "Download no more than DataCapMB MBytes every DataCapHours hours."
	DataCapEnabled bool  `json:"dataCapEnabled"`
	DataCapMB      int64 `json:"dataCapMB"`
	DataCapHours   int   `json:"dataCapHours"`
}

const keyNetwork = "net.settings"

func defaultNetworkSettings() NetworkSettings {
	return NetworkSettings{DNSMode: "system", DNSPresetMode: "https", ProxyMode: "system", DataCapMB: 200, DataCapHours: 5}
}

// GetNetworkSettings returns the connection preferences.
func (a *App) GetNetworkSettings() NetworkSettings {
	s := defaultNetworkSettings()
	if a.store == nil {
		return s
	}
	if raw, ok, _ := a.store.Settings().Get(context.Background(), keyNetwork); ok {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

// SaveNetworkSettings validates and saves the connection preferences.
func (a *App) SaveNetworkSettings(s NetworkSettings) (NetworkSettings, error) {
	if s.SpeedLimitKBps < 0 {
		return NetworkSettings{}, errors.New("the speed limit must not be negative")
	}
	if _, err := s.dnsChoice(); err != nil {
		return NetworkSettings{}, err
	}
	switch s.ProxyMode {
	case "system", "none":
	case "fixed":
		if _, err := network.ParseProxy(s.ProxyURL); err != nil {
			return NetworkSettings{}, err
		}
	case "pac":
		if strings.TrimSpace(s.PACScript) == "" && strings.TrimSpace(s.PACURL) == "" {
			return NetworkSettings{}, errors.New("give the auto-config script's address or text")
		}
	default:
		return NetworkSettings{}, fmt.Errorf("unknown proxy mode %q", s.ProxyMode)
	}
	if s.DataCapEnabled && (s.DataCapMB < 1 || s.DataCapHours < 1) {
		return NetworkSettings{}, errors.New("the download limit needs a size and a period of at least 1")
	}
	raw, _ := json.Marshal(s)
	if err := a.store.Settings().Set(context.Background(), keyNetwork, string(raw)); err != nil {
		return NetworkSettings{}, err
	}
	a.applyLiveSettings(s)
	return s, nil
}

// applyLiveSettings applies what can change while downloads run.
func (a *App) applyLiveSettings(s NetworkSettings) {
	if a.manager != nil {
		a.manager.SetSpeedLimit(s.SpeedLimitKBps << 10)
	}
	if w := a.dataCap.Load(); w != nil {
		if s.DataCapEnabled {
			w.Set(s.DataCapMB<<20, time.Duration(s.DataCapHours)*time.Hour)
		} else {
			w.Set(0, 0)
		}
	}
}

// DNSPresets lists the public DNS services offered by name.
func (a *App) DNSPresets() []network.DNSPreset { return network.DNSPresets }

// SetDownloadSpeedLimit caps one download in KiB/s (0: unlimited).
func (a *App) SetDownloadSpeedLimit(id string, kbps int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return a.manager.SetDownloadSpeedLimit(a.ctx, id, kbps<<10)
}

// dnsChoice turns the DNS settings into a resolver choice (nil: the
// system's).
func (s NetworkSettings) dnsChoice() (*network.DNSChoice, error) {
	switch s.DNSMode {
	case "", "system":
		return nil, nil
	case "preset":
		p, ok := network.DNSPresetByID(s.DNSPreset)
		if !ok {
			return nil, fmt.Errorf("unknown DNS service %q", s.DNSPreset)
		}
		modes := map[string]network.DNSPresetMode{"plain": network.PresetPlain, "tls": network.PresetTLS, "https": network.PresetHTTPS}
		names := map[string]string{"plain": "DNS", "tls": "DNS over TLS", "https": "DNS over HTTPS"}
		mode, ok := modes[s.DNSPresetMode]
		if !ok || !p.Offers(mode) {
			return nil, fmt.Errorf("%s is not offered as %q", p.Name, s.DNSPresetMode)
		}
		if !network.ValidNextDNSProfile(s.DNSProfile) {
			return nil, errors.New("a NextDNS configuration ID is six hex digits")
		}
		c := p.Choice(mode, s.DNSProfile, s.DNSFallback, p.Name+" ("+names[s.DNSPresetMode]+")")
		return &c, nil
	case "plain", "tls":
		port := uint16(53)
		if s.DNSMode == "tls" {
			port = 853
		}
		servers, err := network.ParseDNSServers(s.DNSServers, port)
		if err != nil {
			return nil, err
		}
		if len(servers) == 0 {
			return nil, errors.New("give at least one DNS server")
		}
		c := network.DNSChoice{Mode: network.DNSPlain, Servers: servers, Fallback: s.DNSFallback, Label: "your DNS"}
		if s.DNSMode == "tls" {
			if strings.TrimSpace(s.DNSTLSName) == "" {
				return nil, errors.New("DNS over TLS needs the name its certificate shows")
			}
			c.Mode, c.TLSName, c.Label = network.DNSTLS, strings.TrimSpace(s.DNSTLSName), "your DNS over TLS"
		}
		return &c, nil
	case "https":
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.DNSURL)), "https://") {
			return nil, errors.New("DNS over HTTPS needs an https:// address")
		}
		return &network.DNSChoice{Mode: network.DNSHTTPS, URL: strings.TrimSpace(s.DNSURL), Fallback: s.DNSFallback, Label: "your DNS over HTTPS"}, nil
	}
	return nil, fmt.Errorf("unknown DNS mode %q", s.DNSMode)
}

// engineNetwork returns the engine's DNS and network options from the
// saved settings. A PAC script given by address is fetched now.
func (a *App) engineNetwork(ctx context.Context) (*network.DNSChoice, network.Options, error) {
	s := a.GetNetworkSettings()
	var opts network.Options
	dns, err := s.dnsChoice()
	if err != nil {
		return nil, opts, err
	}
	opts.FTPActive = s.FTPActive
	switch s.ProxyMode {
	case "none":
		opts.Proxy = "direct"
	case "fixed":
		opts.Proxy = s.ProxyURL
	case "pac":
		opts.PACScript = s.PACScript
		if strings.TrimSpace(s.PACURL) != "" {
			script, err := fetchPAC(ctx, s.PACURL)
			if err != nil {
				return dns, opts, fmt.Errorf("proxy auto-config script: %w", err)
			}
			opts.PACScript = script
		}
	}
	return dns, opts, nil
}

// fetchPAC downloads a proxy auto-config script (at most 1 MiB, 10 s).
func fetchPAC(ctx context.Context, raw string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(raw), nil)
	if err != nil {
		return "", err
	}
	res, err := network.NewClient(network.Options{AllowPrivate: true, Proxy: "direct"}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", raw, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return string(b), err
}
