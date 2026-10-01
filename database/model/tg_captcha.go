package model

// TgCaptcha is where a Telegram account stands with the bot's captcha (#188,
// #220, docs/spec/users.md §12), keyed by nothing but its tg_id. Once the
// account has passed it is remembered: its later requests go without the
// captcha, until an admin resets the pass («Сбросить капчу») or blocks the
// account. A table of its own, additive (ADR 0002).
type TgCaptcha struct {
	TgId int64 `json:"tgId" gorm:"primaryKey;autoIncrement:false"`
	// PassedAt is when the account passed (ms); 0 while it has not, or since
	// an admin reset it.
	PassedAt int64 `json:"passedAt" gorm:"not null;default:0"`
	// Attempts counts the solutions the account posted with a valid
	// initData, right or wrong; LastAttemptAt and LastFailAt are ms.
	Attempts      int   `json:"attempts" gorm:"not null;default:0"`
	LastAttemptAt int64 `json:"lastAttemptAt" gorm:"not null;default:0"`
	LastFailAt    int64 `json:"lastFailAt" gorm:"not null;default:0"`
}

// TableName keeps the table's name as the spec writes it.
func (TgCaptcha) TableName() string { return "tg_captcha" }

// TgCaptchaSolution is an ALTCHA solution the panel has accepted, by its
// challenge's signature: each works once, across restarts too. A row is
// kept while its challenge lives (ExpiresAt, ms), then cleaned up.
type TgCaptchaSolution struct {
	Signature string `json:"signature" gorm:"primaryKey"`
	TgId      int64  `json:"tgId" gorm:"not null;default:0"`
	ExpiresAt int64  `json:"expiresAt" gorm:"not null;default:0;index"`
}

// TableName names the table beside tg_captcha.
func (TgCaptchaSolution) TableName() string { return "tg_captcha_solutions" }
