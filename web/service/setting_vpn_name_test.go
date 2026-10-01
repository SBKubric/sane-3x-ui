package service

import (
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/entity"
)

// TestVPNNameSettings: the VPN name settings (#225) are empty — with a
// five-minute TTL — on a fresh panel, go through the settings form stored
// tidy, and the DNSExit API key never comes back out: the form and the API
// see the mask, a save of the mask keeps the key, a new key replaces it and
// "" removes it.
func TestVPNNameSettings(t *testing.T) {
	s := newMonitoringSettingService(t)

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	if all.DnsExitApiKey != "" || all.VpnName != "" || all.VpnNameTtl != 5 || all.DomainExpiry != "" {
		t.Errorf("defaults: key %q, name %q, ttl %d, expiry %q", all.DnsExitApiKey, all.VpnName, all.VpnNameTtl, all.DomainExpiry)
	}

	all.DnsExitApiKey = " secret-key-1 "
	all.VpnName = "VPN.example.com."
	all.VpnNameTtl = 10
	all.DomainExpiry = "2027-03-01"
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	key, _ := s.GetDnsExitApiKey()
	name, _ := s.GetVPNName()
	ttl, _ := s.GetVPNNameTTL()
	expiry, _ := s.GetDomainExpiry()
	if key != "secret-key-1" || name != "vpn.example.com" || ttl != 10 || expiry != "2027-03-01" {
		t.Errorf("stored: key %q, name %q, ttl %d, expiry %q", key, name, ttl, expiry)
	}

	// The form sees the mask, never the key.
	shown, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	if shown.DnsExitApiKey != entity.DnsExitApiKeyMask || strings.Contains(shown.DnsExitApiKey, "secret") {
		t.Errorf("the form sees %q", shown.DnsExitApiKey)
	}
	// Saved back as loaded, the key stays.
	if err := s.UpdateAllSetting(shown); err != nil {
		t.Fatal(err)
	}
	if key, _ := s.GetDnsExitApiKey(); key != "secret-key-1" {
		t.Errorf("after saving the mask: %q", key)
	}
	// A new key replaces it, "" removes it.
	shown.DnsExitApiKey = "secret-key-2"
	if err := s.UpdateAllSetting(shown); err != nil {
		t.Fatal(err)
	}
	if key, _ := s.GetDnsExitApiKey(); key != "secret-key-2" {
		t.Errorf("a new key: %q", key)
	}
	shown.DnsExitApiKey = ""
	if err := s.UpdateAllSetting(shown); err != nil {
		t.Fatal(err)
	}
	if key, _ := s.GetDnsExitApiKey(); key != "" {
		t.Errorf("a removed key: %q", key)
	}
	if again, _ := s.GetAllSetting(); again.DnsExitApiKey != "" {
		t.Errorf("no key shows as %q", again.DnsExitApiKey)
	}

	// Refused values touch nothing.
	for _, bad := range []func(a *entity.AllSetting){
		func(a *entity.AllSetting) { a.VpnName = "203.0.113.7" },
		func(a *entity.AllSetting) { a.VpnNameTtl = 2000 },
		func(a *entity.AllSetting) { a.DomainExpiry = "01.03.2027" },
	} {
		a, _ := s.GetAllSetting()
		bad(a)
		if err := s.UpdateAllSetting(a); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
	name, _ = s.GetVPNName()
	ttl, _ = s.GetVPNNameTTL()
	expiry, _ = s.GetDomainExpiry()
	if name != "vpn.example.com" || ttl != 10 || expiry != "2027-03-01" {
		t.Errorf("after refusals: name %q, ttl %d, expiry %q", name, ttl, expiry)
	}
}

// TestVPNNameSetters: the CLI's setters check what they store as the form
// does.
func TestVPNNameSetters(t *testing.T) {
	s := newMonitoringSettingService(t)
	if err := s.SetVPNName(" Vpn.Example.NET "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVPNNameTTL(3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDomainExpiry("2027-12-31"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDnsExitApiKey(" k "); err != nil {
		t.Fatal(err)
	}
	name, _ := s.GetVPNName()
	ttl, _ := s.GetVPNNameTTL()
	expiry, _ := s.GetDomainExpiry()
	key, _ := s.GetDnsExitApiKey()
	if name != "vpn.example.net" || ttl != 3 || expiry != "2027-12-31" || key != "k" {
		t.Errorf("stored: %q %d %q %q", name, ttl, expiry, key)
	}
	if s.SetVPNName("vpn") == nil || s.SetVPNNameTTL(0) == nil || s.SetVPNNameTTL(1441) == nil || s.SetDomainExpiry("never") == nil {
		t.Error("a setter accepted a bad value")
	}
}

// TestLinkOverride: with the host override on, the VLESS links name the VPN
// name when one is set, the active edge's host otherwise; with it off,
// nothing — the VPN name alone overrides nothing.
func TestLinkOverride(t *testing.T) {
	s := newMonitoringSettingService(t)
	if host, on := s.GetLinkOverride(); on || host != "" {
		t.Errorf("no override: %q %v", host, on)
	}
	if err := s.SetVPNName("vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	if host, on := s.GetLinkOverride(); on || host != "" {
		t.Errorf("a VPN name without an override: %q %v", host, on)
	}
	setLegacyOverride(t, s, "203.0.113.10")
	if host, on := s.GetLinkOverride(); !on || host != "vpn.example.com" {
		t.Errorf("override with a VPN name: %q %v", host, on)
	}
	if host, _ := s.GetProxyOverride(); host != "203.0.113.10" {
		t.Errorf("the host override itself: %q", host)
	}
	if err := s.SetVPNName(""); err != nil {
		t.Fatal(err)
	}
	if host, on := s.GetLinkOverride(); !on || host != "203.0.113.10" {
		t.Errorf("override without a VPN name: %q %v", host, on)
	}
}

func setLegacyOverride(t *testing.T, s *SettingService, host string) {
	t.Helper()
	if err := s.SetProxyOverrideHost(host); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProxyOverrideEnable(true); err != nil {
		t.Fatal(err)
	}
}

func TestVPNNameZone(t *testing.T) {
	for name, want := range map[string]string{
		"vpn.example.com":     "example.com",
		"a.b.vpn.example.net": "example.net",
		"vpn.example.co.uk":   "example.co.uk",
		"example.com":         "example.com",
	} {
		if got := vpnNameZone(name); got != want {
			t.Errorf("vpnNameZone(%q) = %q, want %q", name, got, want)
		}
	}
}
