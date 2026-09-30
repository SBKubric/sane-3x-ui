package model

// The statuses of a request.
const (
	SubRequestPending   = "pending"   // waits for an admin
	SubRequestApproved  = "approved"  // a user was made for it (#221)
	SubRequestRejected  = "rejected"  // an admin said no, with a reason
	SubRequestCancelled = "cancelled" // the applicant took it back
	SubRequestExpired   = "expired"   // nobody decided in 14 days
)

// SubRequest is a request for a subscription that a Telegram account leaves
// in the bot (#188, #220, docs/spec/users.md §12): after a captcha, with an
// optional comment, for an admin to approve or reject. An account has one
// pending request at a time. A table of its own, additive (ADR 0002).
type SubRequest struct {
	Id      int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	TgId    int64  `json:"tgId" gorm:"not null;index"`
	Comment string `json:"comment" gorm:"not null;default:''"`
	Status  string `json:"status" gorm:"not null;default:'pending';index"`
	// CreatedAt and DecidedAt are ms; DecidedAt is 0 while the request waits,
	// then when it was approved, rejected, cancelled or expired.
	CreatedAt int64 `json:"createdAt" gorm:"not null;default:0"`
	DecidedAt int64 `json:"decidedAt" gorm:"not null;default:0"`
	// DecidedBy is the admin who approved or rejected it; "" for the
	// applicant's cancel and for the expiry.
	DecidedBy string `json:"decidedBy" gorm:"not null;default:''"`
	// Reason is the rejection's reason, shown to the applicant.
	Reason string `json:"reason" gorm:"not null;default:''"`
}
