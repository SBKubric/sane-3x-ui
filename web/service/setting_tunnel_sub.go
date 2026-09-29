package service

// Tunnel subscription settings (docs/spec/tunnel-subscription.md §5). The keys
// live in defaultValueMap and entity.AllSetting; no migration is needed, a key
// appears on the first settings save. Like the other subscription paths they
// take effect after a panel restart.

// GetSubTunEnable reports whether the sub server serves /tun at all.
func (s *SettingService) GetSubTunEnable() (bool, error) {
	return s.getBool("subTunEnable")
}

// GetSubTunPath is the prefix of the tunnel subscription route, "/tun/" by
// default.
func (s *SettingService) GetSubTunPath() (string, error) {
	return s.getString("subTunPath")
}

// GetSubTunURI is the owner's override of the route's public URL, "" for one
// built from the sub server's address.
func (s *SettingService) GetSubTunURI() (string, error) {
	return s.getString("subTunURI")
}

// publishedTunPath is the tunnel subscription's path while the route is on,
// "" otherwise: what the chain document tells the hops to serve (spec §7) and
// what nginx publishes.
func (s *SettingService) publishedTunPath() string {
	if on, err := s.GetSubTunEnable(); err != nil || !on {
		return ""
	}
	path, err := s.GetSubTunPath()
	if err != nil || path == "/" {
		return ""
	}
	return path
}
