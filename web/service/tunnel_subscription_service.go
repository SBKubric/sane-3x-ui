package service

import (
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"

	"gorm.io/gorm/clause"
)

// TunnelSubscriptionService keeps the link between a tunnel client and its
// subscription (docs/spec/tunnel-subscription.md §2–§3): an xray client
// carries its subId in its own settings, a tunnel client gets it from the
// tunnel_client_subs table. Only the link half of the spec lives here; the
// /tun route and its cache come with the tunnel subscription itself.
type TunnelSubscriptionService struct{}

// Set links the tunnel client to subId, replacing any link it had. An empty
// subId removes the link.
func (s *TunnelSubscriptionService) Set(clientUUID, kind, subId string) error {
	if subId == "" {
		return s.Clear(clientUUID)
	}
	link := &model.TunnelClientSub{ClientUUID: clientUUID, Kind: kind, SubId: subId}
	return database.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{"kind", "sub_id", "updated_at"}),
	}).Create(link).Error
}

// Clear removes the tunnel client's link, if it has one.
func (s *TunnelSubscriptionService) Clear(clientUUID string) error {
	return database.GetDB().Where("client_uuid = ?", clientUUID).Delete(&model.TunnelClientSub{}).Error
}

// SubIdsByUUIDs returns the subId of every linked client among uuids; an
// unlinked client is absent from the map.
func (s *TunnelSubscriptionService) SubIdsByUUIDs(uuids []string) (map[string]string, error) {
	out := make(map[string]string, len(uuids))
	if len(uuids) == 0 {
		return out, nil
	}
	var links []model.TunnelClientSub
	if err := database.GetDB().Where("client_uuid IN ?", uuids).Find(&links).Error; err != nil {
		return nil, err
	}
	for _, l := range links {
		out[l.ClientUUID] = l.SubId
	}
	return out, nil
}

// allLinks returns every link keyed by client uuid.
func (s *TunnelSubscriptionService) allLinks() (map[string]string, error) {
	var links []model.TunnelClientSub
	if err := database.GetDB().Find(&links).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(links))
	for _, l := range links {
		out[l.ClientUUID] = l.SubId
	}
	return out, nil
}
