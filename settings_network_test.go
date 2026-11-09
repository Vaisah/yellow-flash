package main

import (
	"path/filepath"
	"testing"

	"github.com/vaisah/internal/infrastructure/network"
	"github.com/vaisah/internal/infrastructure/persistence/jsonstore"
)

func TestNetworkSettingsBecomeEngineOptions(t *testing.T) {
	store, err := jsonstore.Open(filepath.Join(t.TempDir(), "downloads.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := &App{store: store}
	if s := a.GetNetworkSettings(); s.DNSMode != "system" || s.ProxyMode != "system" {
		t.Fatalf("defaults %+v", s)
	}
	for _, bad := range []NetworkSettings{
		{DNSMode: "plain", DNSServers: "dns.google", ProxyMode: "system"},
		{DNSMode: "tls", DNSServers: "9.9.9.9", ProxyMode: "system"},
		{DNSMode: "https", DNSURL: "http://x/q", ProxyMode: "system"},
		{DNSMode: "preset", DNSPreset: "nextdns", DNSPresetMode: "plain", ProxyMode: "system"},
		{DNSMode: "system", ProxyMode: "fixed", ProxyURL: "ftp://p:1"},
		{DNSMode: "system", ProxyMode: "pac"},
		{DNSMode: "system", ProxyMode: "system", DataCapEnabled: true},
		{DNSMode: "system", ProxyMode: "system", SpeedLimitKBps: -1},
	} {
		if _, err := a.SaveNetworkSettings(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	good := NetworkSettings{DNSMode: "preset", DNSPreset: "quad9", DNSPresetMode: "tls", DNSFallback: true,
		ProxyMode: "fixed", ProxyURL: "socks5://127.0.0.1:1080", FTPActive: true, SpeedLimitKBps: 512,
		DataCapEnabled: true, DataCapMB: 100, DataCapHours: 2}
	if _, err := a.SaveNetworkSettings(good); err != nil {
		t.Fatal(err)
	}
	dns, opts, err := a.engineNetwork(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if dns == nil || dns.Mode != network.DNSTLS || dns.TLSName != "dns.quad9.net" || !dns.Fallback || dns.Label != "Quad9 (DNS over TLS)" {
		t.Fatalf("dns %+v", dns)
	}
	if opts.Proxy != "socks5://127.0.0.1:1080" || !opts.FTPActive || opts.PACScript != "" {
		t.Fatalf("options %+v", opts)
	}
	if got := a.GetNetworkSettings(); got != good {
		t.Fatalf("read back %+v", got)
	}
}
