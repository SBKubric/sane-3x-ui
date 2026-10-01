package model

// The broadcast of the updated subscription link (#214, #222,
// docs/spec/users.md §13). Tables of their own, additive (ADR 0002).

// The triggers of a broadcast.
const (
	SubLinkTriggerAuto = "auto" // the links changed and an admin said «Yes»
	SubLinkTriggerAll  = "all"  // «📣 Send the links» of «⚙️ Server»: every user
	SubLinkTriggerUser = "user" // one user, from their card
	SubLinkTriggerAPI  = "api"  // the users page's button
)

// The reasons a link changes, as the detector tells them apart.
const (
	SubLinkReasonEdge    = "edge"    // the active edge changed (the host override)
	SubLinkReasonSubPath = "subPath" // the subscriptions' path changed
	SubLinkReasonFront   = "front"   // the address, port or scheme of the front changed
	SubLinkReasonSubId   = "subId"   // the user's subId changed
	SubLinkReasonPublic  = "public"  // the link goes through the public subscription address now (#224)
	SubLinkReasonVPNName = "vpnName" // the VLESS links name another VPN name, or none any more (#225)
)

// The outcomes of one delivery.
const (
	SubLinkSent       = "sent"        // the message went
	SubLinkFailed     = "failed"      // Telegram refused it; Error says why
	SubLinkBlocked    = "blocked"     // the person blocked the bot
	SubLinkNoTelegram = "no_telegram" // the user has no Telegram: for manual sending
)

// SubLinkKnown is the link a person — a Telegram account that is a user's
// Telegram — was last known to have: what the detector compares the current
// link with. A row is written when the person is first seen with a user, and
// again when an admin answers the question about a change (either way) or
// the link is sent to them. ConfHash is the hash of the user's tunnel
// clients' configs as last sent (or first seen): a broadcast attaches the
// .conf files when it differs.
//
// VpnName is the VPN name the person's VLESS links named then (#225), ""
// for none (or the override off). EdgeHost is the host override their
// configs were known with — the AWG Endpoint follows it — "" for none; NULL
// on a row from before #225, which the detector fills in silently.
type SubLinkKnown struct {
	TgId      int64   `json:"tgId" gorm:"primaryKey;autoIncrement:false"`
	SubId     string  `json:"subId" gorm:"not null;default:''"`
	URL       string  `json:"url" gorm:"not null;default:''"`
	ConfHash  string  `json:"confHash" gorm:"not null;default:''"`
	VpnName   string  `json:"vpnName" gorm:"not null;default:''"`
	EdgeHost  *string `json:"edgeHost"`
	UpdatedAt int64   `json:"updatedAt" gorm:"not null;default:0"` // ms
}

// SubLinkBroadcast is one broadcast in the journal: who started it and why,
// and how it went.
type SubLinkBroadcast struct {
	Id      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Trigger string `json:"trigger" gorm:"not null;default:''"`
	// Reasons are the change's reasons, comma-separated; "" for a manual one.
	Reasons string `json:"reasons" gorm:"not null;default:''"`
	// StartedBy is the admin who confirmed it: a Telegram id, or "panel".
	StartedBy  string `json:"startedBy" gorm:"not null;default:''"`
	CreatedAt  int64  `json:"createdAt" gorm:"not null;default:0;index"` // ms
	FinishedAt int64  `json:"finishedAt" gorm:"not null;default:0"`      // ms; 0 while it runs
	Sent       int    `json:"sent" gorm:"not null;default:0"`
	Failed     int    `json:"failed" gorm:"not null;default:0"`
	Blocked    int    `json:"blocked" gorm:"not null;default:0"`
	NoTelegram int    `json:"noTelegram" gorm:"not null;default:0"`
}

// SubLinkDelivery is one user of a broadcast and how their message went.
type SubLinkDelivery struct {
	Id          int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	BroadcastId int64  `json:"broadcastId" gorm:"not null;index"`
	SubId       string `json:"subId" gorm:"not null;default:''"`
	Name        string `json:"name" gorm:"not null;default:''"`
	TgId        int64  `json:"tgId" gorm:"not null;default:0"`
	Status      string `json:"status" gorm:"not null;default:''"`
	Error       string `json:"error" gorm:"not null;default:''"`
}
