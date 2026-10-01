package service

import (
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"

	"golang.org/x/net/publicsuffix"
)

// The VPN name settings (#225, entity/vpn_name.go). They live in
// defaultValueMap and entity.AllSetting like the other preferences, so no
// migration is needed; the CLI (`x-ui setting -vpnName …`) writes them too,
// through the setters below, which check what they store as the form does.

// GetDnsExitApiKey is the DNSExit API key; "" for none, and then the panel
// never calls DNSExit.
func (s *SettingService) GetDnsExitApiKey() (string, error) {
	key, err := s.getString("dnsExitApiKey")
	return strings.TrimSpace(key), err
}

// SetDnsExitApiKey stores the DNSExit API key; "" removes it.
func (s *SettingService) SetDnsExitApiKey(key string) error {
	return s.setString("dnsExitApiKey", strings.TrimSpace(key))
}

// GetVPNName is the VPN name, such as vpn.example.com; "" for none. A stored
// value that is not a name (written past the form) reads as none.
func (s *SettingService) GetVPNName() (string, error) {
	raw, err := s.getString("vpnName")
	if err != nil {
		return "", err
	}
	name, err := entity.NormalizeVPNName(raw)
	if err != nil {
		return "", nil
	}
	return name, nil
}

// SetVPNName checks and stores the VPN name; "" removes it.
func (s *SettingService) SetVPNName(raw string) error {
	name, err := entity.NormalizeVPNName(raw)
	if err != nil {
		return err
	}
	return s.setString("vpnName", name)
}

// GetVPNNameTTL is the TTL of the VPN name's record in minutes, the default
// for a stored value out of range.
func (s *SettingService) GetVPNNameTTL() (int, error) {
	ttl, err := s.getInt("vpnNameTtl")
	if err != nil || ttl < 1 || ttl > entity.VPNNameMaxTTL {
		return entity.VPNNameDefaultTTL, err
	}
	return ttl, nil
}

// SetVPNNameTTL stores the record's TTL, 1 to entity.VPNNameMaxTTL minutes.
func (s *SettingService) SetVPNNameTTL(ttl int) error {
	if ttl < 1 || ttl > entity.VPNNameMaxTTL {
		return common.NewErrorf("VPN name TTL must be 1-%d minutes: %d", entity.VPNNameMaxTTL, ttl)
	}
	return s.setInt("vpnNameTtl", ttl)
}

// GetDomainExpiry is the domain's registration expiry date, YYYY-MM-DD; ""
// for none.
func (s *SettingService) GetDomainExpiry() (string, error) {
	raw, err := s.getString("domainExpiry")
	if err != nil {
		return "", err
	}
	date, ok, err := entity.ParseDomainExpiry(raw)
	if err != nil || !ok {
		return "", nil
	}
	return date.Format("2006-01-02"), nil
}

// SetDomainExpiry checks and stores the expiry date; "" removes it.
func (s *SettingService) SetDomainExpiry(raw string) error {
	date, ok, err := entity.ParseDomainExpiry(raw)
	if err != nil {
		return err
	}
	if !ok {
		return s.setString("domainExpiry", "")
	}
	return s.setString("domainExpiry", date.Format("2006-01-02"))
}

// GetLinkOverride is the address the VLESS links (and the other xray
// configs of a subscription) name while the host override is on: the VPN
// name when one is set — it follows the active edge in DNS — the active
// edge's host otherwise. The subscription links themselves, the AWG
// Endpoint and the monitoring probes keep the edge's host
// (GetProxyOverride): the front of an edge serves the subscriptions by
// address only, and the tunnel configs stay by IP (map #52).
func (s *SettingService) GetLinkOverride() (string, bool) {
	host, on := s.GetProxyOverride()
	if !on {
		return "", false
	}
	if name, _ := s.GetVPNName(); name != "" {
		return name, true
	}
	return host, true
}

// vpnNameZone is the domain DNSExit keeps the VPN name's record in: the
// registered domain (example.com for vpn.example.com, example.co.uk for
// vpn.example.co.uk).
func vpnNameZone(name string) string {
	if zone, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
		return zone
	}
	labels := strings.Split(name, ".")
	if len(labels) <= 2 {
		return name
	}
	return strings.Join(labels[len(labels)-2:], ".")
}
