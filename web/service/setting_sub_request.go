package service

import "github.com/coinman-dev/3ax-ui/v2/web/entity"

// SubRequestDefaults are what «✅ Approve» gives the user a request for a
// subscription makes (#188 point 5, #221): a client in each of the inbounds
// — nil for every enabled one — with the traffic per protocol (GB, 0 =
// unlimited) and the expiry (days after the first use, 0 = never). The
// settings form edits them on the Telegram tab.
type SubRequestDefaults struct {
	InboundIds []int
	TrafficGB  int
	ExpiryDays int
}

// GetSubRequestDefaults reads the request defaults.
func (s *SettingService) GetSubRequestDefaults() (*SubRequestDefaults, error) {
	value, err := s.getString("subRequestInbounds")
	if err != nil {
		return nil, err
	}
	ids, err := entity.ParseSubRequestInbounds(value)
	if err != nil {
		return nil, err
	}
	gb, err := s.getInt("subRequestTrafficGB")
	if err != nil {
		return nil, err
	}
	days, err := s.getInt("subRequestExpiryDays")
	if err != nil {
		return nil, err
	}
	return &SubRequestDefaults{InboundIds: ids, TrafficGB: gb, ExpiryDays: days}, nil
}
