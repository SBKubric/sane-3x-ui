package model

// TgInvite is an invite link to bind a user's Telegram (#187, #219,
// docs/spec/users.md §11): t.me/<bot>?start=<Token>. Whoever presses Start
// with it gets their Telegram account linked to the user, once, within
// seven days. «🔄 Reissue» revokes the user's open invite and makes a new one.
// A table of its own, additive (ADR 0002).
type TgInvite struct {
	// Token is the start parameter: random, [A-Za-z0-9_-], at most the 64
	// characters Telegram passes.
	Token     string `json:"token" gorm:"primaryKey"`
	SubId     string `json:"subId" gorm:"not null;index"`
	CreatedAt int64  `json:"createdAt" gorm:"not null;default:0"` // ms
	ExpiresAt int64  `json:"expiresAt" gorm:"not null;default:0"` // ms
	// UsedAt and UsedBy are when and by which Telegram account the invite
	// was used; 0 while it is not.
	UsedAt  int64 `json:"usedAt" gorm:"not null;default:0"`
	UsedBy  int64 `json:"usedBy" gorm:"not null;default:0"`
	Revoked bool  `json:"revoked" gorm:"not null;default:false"`
}
