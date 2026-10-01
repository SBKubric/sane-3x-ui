package entity

import (
	"strings"
	"testing"
)

// The VPN name settings (#225): the name, its TTL and the domain's expiry
// date, as the settings form sends them.

func TestNormalizeVPNName(t *testing.T) {
	cases := []struct {
		raw, want string
		bad       bool
	}{
		{raw: "", want: ""},
		{raw: "  ", want: ""},
		{raw: "vpn.example.com", want: "vpn.example.com"},
		{raw: " VPN.Example.COM. ", want: "vpn.example.com"},
		{raw: "a-1.b2.example.net", want: "a-1.b2.example.net"},
		{raw: "example", bad: true},                 // one label: not a name in a zone
		{raw: "203.0.113.7", bad: true},             // an address is what the name replaces
		{raw: "https://vpn.example.com", bad: true}, // a URL
		{raw: "vpn.example.com:443", bad: true},     // a port
		{raw: "vpn..example.com", bad: true},        // an empty label
		{raw: "-vpn.example.com", bad: true},        // a label starting with a hyphen
		{raw: "vpn_1.example.com", bad: true},       // an underscore
		{raw: "*.example.com", bad: true},           // a wildcard
		{raw: strings.Repeat("a", 64) + ".example.com", bad: true},
	}
	for _, c := range cases {
		got, err := NormalizeVPNName(c.raw)
		if c.bad {
			if err == nil || !strings.Contains(err.Error(), "VPN name must be") {
				t.Errorf("NormalizeVPNName(%q) = %q, %v; want a refusal", c.raw, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("NormalizeVPNName(%q) = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
}

func TestParseDomainExpiry(t *testing.T) {
	for _, raw := range []string{"", " "} {
		if d, ok, err := ParseDomainExpiry(raw); err != nil || ok || !d.IsZero() {
			t.Errorf("ParseDomainExpiry(%q) = %v, %v, %v; want none", raw, d, ok, err)
		}
	}
	d, ok, err := ParseDomainExpiry(" 2027-03-01 ")
	if err != nil || !ok || d.Format("2006-01-02") != "2027-03-01" {
		t.Errorf("ParseDomainExpiry = %v, %v, %v", d, ok, err)
	}
	for _, raw := range []string{"01.03.2027", "2027-02-30", "2027-3-1", "tomorrow"} {
		if _, _, err := ParseDomainExpiry(raw); err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
			t.Errorf("ParseDomainExpiry(%q): %v; want a refusal", raw, err)
		}
	}
}

// TestCheckVPNNameSettings: the form's values are stored tidy, and what is
// out of range is refused.
func TestCheckVPNNameSettings(t *testing.T) {
	s := &AllSetting{VpnName: " Vpn.Example.com. ", VpnNameTtl: 5, DomainExpiry: " 2027-03-01 "}
	if err := checkVPNName(s); err != nil {
		t.Fatal(err)
	}
	if s.VpnName != "vpn.example.com" || s.DomainExpiry != "2027-03-01" {
		t.Errorf("stored as %q, %q", s.VpnName, s.DomainExpiry)
	}
	// A form from before the setting sends no TTL: the default.
	old := &AllSetting{}
	if err := checkVPNName(old); err != nil || old.VpnNameTtl != VPNNameDefaultTTL {
		t.Errorf("no TTL: %d, %v", old.VpnNameTtl, err)
	}
	for _, ttl := range []int{-1, VPNNameMaxTTL + 1} {
		s := &AllSetting{VpnNameTtl: ttl}
		if err := checkVPNName(s); err == nil || !strings.Contains(err.Error(), "TTL") {
			t.Errorf("TTL %d: %v; want a refusal", ttl, err)
		}
	}
	if err := checkVPNName(&AllSetting{VpnNameTtl: 5, VpnName: "localhost"}); err == nil {
		t.Error("a one-label name was accepted")
	}
	if err := checkVPNName(&AllSetting{VpnNameTtl: 5, DomainExpiry: "soon"}); err == nil {
		t.Error("a date that is no date was accepted")
	}
}
