package service

import (
	"strconv"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/clientrouting"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The client routing (#263, entity/sub_client_routing.go): the routing the
// subscription hands Happ, Incy and v2RayTun in its routing header. Stored
// normalized, so a value saved again unchanged is the same string, and with
// the time it last changed beside it: Happ's profile carries that time as
// LastUpdated, and Happ downloads its geo files whenever it grows — a time
// that moved on every fetch, or on every save of an unchanged form, would
// have every Happ download them again and again.

const (
	subClientRoutingKey        = "subClientRouting"
	subClientRoutingUpdatedKey = "subClientRoutingUpdated"
)

// clientRoutingNow is the clock of the change time, a variable for tests.
var clientRoutingNow = time.Now

// GetSubClientRouting is the client routing — nil while it is off — and
// when it last changed, in Unix seconds. A routing stored without a time,
// past the form and the CLI, gets one now, once.
func (s *SettingService) GetSubClientRouting() (*clientrouting.Routing, int64, error) {
	raw, err := s.getString(subClientRoutingKey)
	if err != nil {
		return nil, 0, err
	}
	routing, err := clientrouting.Parse(raw)
	if err != nil || routing == nil {
		return nil, 0, err
	}
	updated := s.subClientRoutingUpdated()
	if updated == 0 {
		if updated, err = s.stampSubClientRouting(); err != nil {
			logger.Warning("client routing: could not store when it changed:", err)
		}
	}
	return routing, updated, nil
}

// SetSubClientRouting stores the client routing, "" turning it off; the CLI
// calls it. It refuses a value the apps could not use, as the settings form
// does, and moves the change time only when the routing changed.
func (s *SettingService) SetSubClientRouting(value string) error {
	normalized, err := clientrouting.NormalizeSetting(value)
	if err != nil {
		return common.NewErrorf("client routing: %v", err)
	}
	previous, _ := s.getString(subClientRoutingKey)
	if err := s.setString(subClientRoutingKey, normalized); err != nil {
		return err
	}
	return s.subClientRoutingSaved(previous, normalized)
}

// GetSubClientRoutingSetting is the client routing as stored, "" for none.
func (s *SettingService) GetSubClientRoutingSetting() (string, error) {
	return s.getString(subClientRoutingKey)
}

// subClientRoutingSaved moves the change time once a save has replaced
// previous with next, both normalized: only for a routing that changed and
// is on — turning it off leaves the time for the day it comes back.
func (s *SettingService) subClientRoutingSaved(previous, next string) error {
	if next == previous || next == "" {
		return nil
	}
	_, err := s.stampSubClientRouting()
	return err
}

func (s *SettingService) subClientRoutingUpdated() int64 {
	raw, err := s.getString(subClientRoutingUpdatedKey)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(raw, 10, 64)
	return n
}

// stampSubClientRouting stores the change time: now, and always later than
// the last one, or two changes within a second would look like one to Happ.
func (s *SettingService) stampSubClientRouting() (int64, error) {
	stamp := clientRoutingNow().Unix()
	if last := s.subClientRoutingUpdated(); stamp <= last {
		stamp = last + 1
	}
	return stamp, s.setString(subClientRoutingUpdatedKey, strconv.FormatInt(stamp, 10))
}
