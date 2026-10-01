package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// Tests for the `x-ui setting` VPN name flags (#225): -dnsExitApiKey,
// -vpnName, -vpnNameTtl, -domainExpiry — what the orchestrator sets.

func TestRunVPNNameSetting(t *testing.T) {
	newMonTestDB(t)
	var out bytes.Buffer
	err := runVPNNameSetting(&out, map[string]string{"dnsExitApiKey": "k-1", "vpnName": "VPN.example.com",
		"vpnNameTtl": "7", "domainExpiry": "2027-03-01"})
	if err != nil {
		t.Fatal(err)
	}
	want := "dnsExitApiKey: (set)\nvpnName: vpn.example.com\nvpnNameTtl: 7\ndomainExpiry: 2027-03-01\n"
	if out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	s := service.SettingService{}
	if key, _ := s.GetDnsExitApiKey(); key != "k-1" {
		t.Errorf("key %q", key)
	}

	// Only the flags given are touched; "" clears.
	out.Reset()
	if err := runVPNNameSetting(&out, map[string]string{"vpnName": "", "dnsExitApiKey": ""}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "dnsExitApiKey: (not set)\nvpnName: \n" {
		t.Errorf("output %q", out.String())
	}
	if ttl, _ := s.GetVPNNameTTL(); ttl != 7 {
		t.Errorf("ttl %d", ttl)
	}

	for flag, bad := range map[string]string{"vpnName": "203.0.113.7", "vpnNameTtl": "five", "domainExpiry": "01.03.2027"} {
		out.Reset()
		err := runVPNNameSetting(&out, map[string]string{flag: bad})
		if err == nil || !strings.Contains(err.Error(), flag) || out.Len() != 0 {
			t.Errorf("-%s %q: %v, output %q", flag, bad, err, out.String())
		}
	}
	if ttl, _ := s.GetVPNNameTTL(); ttl != 7 {
		t.Errorf("ttl after refusals %d", ttl)
	}
}
