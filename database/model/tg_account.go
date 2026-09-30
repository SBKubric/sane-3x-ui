package model

// TgAccount is a Telegram account the bot has seen (#186, docs/spec/users.md
// §11): anyone who wrote to the bot or pressed one of its buttons, admin or
// not, with or without a user. The bot refreshes the row as it sees the
// account; a user points at its account through sub_users.tg_id, one to one.
//
// Username is the @nick without the '@', "" for none. A nick belongs to one
// account at a time: when it shows up on another account, the last seen one
// keeps it and the old one's is cleared. No history is kept.
type TgAccount struct {
	TgId      int64  `json:"tgId" gorm:"primaryKey;autoIncrement:false"`
	Username  string `json:"username" gorm:"not null;default:'';index"`
	FirstName string `json:"firstName" gorm:"not null;default:''"`
	LastName  string `json:"lastName" gorm:"not null;default:''"`
	LastSeen  int64  `json:"lastSeen" gorm:"not null;default:0"` // ms
}
