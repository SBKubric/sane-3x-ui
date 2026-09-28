package model

// SubUser is a user of the panel's subscriptions (docs/spec/users.md): a
// person with one name, one subscription (its subId) and any number of
// clients, one per inbound. A client belongs to a user through its subId —
// the field of an xray client, the TunnelClientSub link of a tunnel client.
//
// The table is sub_users because users is upstream's table of panel
// administrators (User), and additive schema changes may not touch it
// (ADR 0002).
//
// Two technical users live here under reserved keys that are never a real
// subId: robot owns every client without a subscription, monitoring owns the
// probe accounts and their shared subId, whatever that subId currently is.
type SubUser struct {
	SubId     string `json:"subId" gorm:"primaryKey"`
	Name      string `json:"name" gorm:"not null;uniqueIndex"`
	TgId      int64  `json:"tgId" gorm:"not null;default:0"`
	Comment   string `json:"comment" gorm:"not null;default:''"`
	CreatedAt int64  `json:"createdAt" gorm:"autoCreateTime:milli"`
	UpdatedAt int64  `json:"updatedAt" gorm:"autoUpdateTime:milli"`
}

// The technical users: their names are reserved, their keys are not subIds.
const (
	SubUserRobot      = "robot"
	SubUserMonitoring = "monitoring"

	// SubUserRobotKey and SubUserMonitoringKey are the primary keys of the
	// technical users. They start with '@' and the client guards refuse them
	// as a subId, so no real subscription can collide with them.
	SubUserRobotKey      = "@robot"
	SubUserMonitoringKey = "@monitoring"
)

// IsTechnical reports whether the user is robot or monitoring.
func (u *SubUser) IsTechnical() bool {
	return u.SubId == SubUserRobotKey || u.SubId == SubUserMonitoringKey
}
