package model

// TunnelClientSub links a tunnel client (AmneziaWG or WireGuard) to a
// subscription (docs/spec/tunnel-subscription.md §2). It is a table of its own
// rather than a SubId field on TunnelClient so the upstream model stays
// untouched (ADR 0002): the upstream legacy↔merged field-coverage tests would
// break on a new field.
//
// The foreign key to tunnel_clients.uuid cascades on delete, so removing a
// tunnel client by any path — the service, the CLI, a bulk delete — takes its
// link along without any change to the upstream tunnel service.
type TunnelClientSub struct {
	ClientUUID string `json:"clientUuid" gorm:"primaryKey;column:client_uuid"`
	// Kind is TunnelKindAwg or TunnelKindWg, denormalised so a reader knows
	// which tunnel service owns the client without a join.
	Kind      string `json:"kind" gorm:"not null"`
	SubId     string `json:"subId" gorm:"not null;index"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`

	Client *TunnelClient `json:"-" gorm:"foreignKey:ClientUUID;references:UUID;constraint:OnDelete:CASCADE"`
}
