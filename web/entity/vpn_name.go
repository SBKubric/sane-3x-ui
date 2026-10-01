package entity

import (
	"net"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The VPN name (#225, map SBKubric/sane-3x-ui-orchestrator#52 decisions 7
// and 8): a DNS name, such as vpn.example.com, whose A record the panel keeps
// on the active edge through the DNSExit API, and which the VLESS links name
// instead of the edge's address. Beside it: the DNSExit API key (a secret),
// the record's TTL in minutes and the domain's registration expiry date, for
// the renewal reminder.

// VPNNameDefaultTTL and VPNNameMaxTTL bound the record's TTL, in minutes as
// DNSExit takes it: five by default, at most a day.
const (
	VPNNameDefaultTTL = 5
	VPNNameMaxTTL     = 1440
)

// DnsExitApiKeyMask is what the settings form and API show in place of a set
// DNSExit API key; a save that sends it back keeps the stored key.
const DnsExitApiKeyMask = "********"

// domainExpiryLayout is how the expiry date is written: 2027-03-01.
const domainExpiryLayout = "2006-01-02"

// NormalizeVPNName checks a VPN name and returns it in lower case without the
// final dot; "" stays "". It must be a host name of two labels or more —
// letters, digits and inner hyphens, 63 characters a label — and not an
// address: the address is what it stands for.
func NormalizeVPNName(raw string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if name == "" {
		return "", nil
	}
	if len(name) > 253 || net.ParseIP(name) != nil {
		return "", vpnNameError(raw)
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return "", vpnNameError(raw)
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", vpnNameError(raw)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", vpnNameError(raw)
			}
		}
	}
	return name, nil
}

func vpnNameError(raw string) error {
	return common.NewError("VPN name must be a DNS name such as vpn.example.com:", raw)
}

// ParseDomainExpiry reads the domain's registration expiry date, YYYY-MM-DD;
// ok is false for "" — no date, no reminder.
func ParseDomainExpiry(raw string) (date time.Time, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	date, err = time.Parse(domainExpiryLayout, raw)
	if err != nil {
		return time.Time{}, false, common.NewError("domain expiry date must be YYYY-MM-DD:", raw)
	}
	return date, true, nil
}

// checkVPNName tidies the VPN name settings the form sent and refuses a name
// that is not one, a TTL out of range and a date that is not one.
func checkVPNName(s *AllSetting) error {
	name, err := NormalizeVPNName(s.VpnName)
	if err != nil {
		return err
	}
	s.VpnName = name
	if s.VpnNameTtl == 0 { // a form or an API call from before the setting
		s.VpnNameTtl = VPNNameDefaultTTL
	}
	if s.VpnNameTtl < 1 || s.VpnNameTtl > VPNNameMaxTTL {
		return common.NewErrorf("VPN name TTL must be 1-%d minutes: %d", VPNNameMaxTTL, s.VpnNameTtl)
	}
	s.DnsExitApiKey = strings.TrimSpace(s.DnsExitApiKey)
	date, ok, err := ParseDomainExpiry(s.DomainExpiry)
	if err != nil {
		return err
	}
	s.DomainExpiry = ""
	if ok {
		s.DomainExpiry = date.Format(domainExpiryLayout)
	}
	return nil
}
