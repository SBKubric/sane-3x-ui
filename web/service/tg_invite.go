package service

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"gorm.io/gorm"
)

// Invite links (#187 point 2, #219, docs/spec/users.md §11): the way to bind
// the Telegram of someone who never wrote to the bot. The admin gets
// t.me/<bot>?start=<token> for a user and sends it; whoever presses Start
// with it has their Telegram linked to the user (SetTelegram, so the id goes
// onto every client). A user has one open invite at a time, for seven days;
// «🔄 Reissue» revokes it and makes a new one. A used, revoked or expired
// token links nobody, nor does one for an account that is another user's
// already: nothing moves then, the admins are told.

// tgInviteTTL is how long an invite link works.
const tgInviteTTL = 7 * 24 * time.Hour

// tgInviteNow is the invites' clock; tests move it.
var tgInviteNow = time.Now

// tgInviteMu serialises the redeems: a token is used once, whoever presses
// first.
var tgInviteMu sync.Mutex

// The outcomes of a redeem.
const (
	TgInviteLinked  = "linked"  // the sender's Telegram is the user's now
	TgInviteAlready = "already" // it was already, by this invite or otherwise
	TgInviteExpired = "expired" // past its seven days
	TgInviteUsed    = "used"    // used by someone else, reissued, or never issued
	TgInviteTaken   = "taken"   // the sender's Telegram is another user's (Owner)
)

// TgInviteRedeem is what a /start <token> came to.
type TgInviteRedeem struct {
	Outcome string
	// User is the invite's user; nil for a token nobody issued, or whose
	// user is gone.
	User *SubUserView
	// Owner is the user whose Telegram the sender's is (TgInviteTaken).
	Owner *SubUserView
}

// TgInviteService issues and redeems invite links.
type TgInviteService struct{}

// newTgInviteToken is a start parameter Telegram takes: 32 random bytes,
// 43 characters of [A-Za-z0-9_-].
func newTgInviteToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// open reports whether the invite still links whoever redeems it.
func tgInviteOpen(inv *model.TgInvite, now time.Time) bool {
	return !inv.Revoked && inv.UsedAt == 0 && now.UnixMilli() < inv.ExpiresAt
}

// Current is the user's open invite; nil when it has none.
func (s *TgInviteService) Current(key string) (*model.TgInvite, error) {
	var invites []model.TgInvite
	if err := database.GetDB().Where("sub_id = ? AND revoked = ? AND used_at = 0", key, false).
		Order("created_at DESC").Find(&invites).Error; err != nil {
		return nil, err
	}
	now := tgInviteNow()
	for i := range invites {
		if tgInviteOpen(&invites[i], now) {
			return &invites[i], nil
		}
	}
	return nil, nil
}

// Invite is the user's open invite, a new one when it has none.
func (s *TgInviteService) Invite(key string) (*model.TgInvite, error) {
	if err := s.checkUser(key); err != nil {
		return nil, err
	}
	tgInviteMu.Lock()
	defer tgInviteMu.Unlock()
	cur, err := s.Current(key)
	if err != nil || cur != nil {
		return cur, err
	}
	return s.issueLocked(key)
}

// Reissue — «🔄 Перевыпустить» — revokes the user's invites that are not
// used and makes a new one.
func (s *TgInviteService) Reissue(key string) (*model.TgInvite, error) {
	if err := s.checkUser(key); err != nil {
		return nil, err
	}
	tgInviteMu.Lock()
	defer tgInviteMu.Unlock()
	return s.issueLocked(key)
}

// checkUser refuses an invite for anyone but an existing regular user.
func (s *TgInviteService) checkUser(key string) error {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return err
	}
	if v.Technical {
		return common.NewErrorf("%s is a technical user and has no Telegram", v.Name)
	}
	return nil
}

// issueLocked revokes the user's unused invites and stores a new one.
func (s *TgInviteService) issueLocked(key string) (*model.TgInvite, error) {
	token, err := newTgInviteToken()
	if err != nil {
		return nil, err
	}
	now := tgInviteNow()
	inv := &model.TgInvite{Token: token, SubId: key, CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(tgInviteTTL).UnixMilli()}
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.TgInvite{}).Where("sub_id = ? AND used_at = 0", key).Update("revoked", true).Error; err != nil {
			return err
		}
		return tx.Create(inv).Error
	})
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// Redeem is /start <token> from the Telegram account tgId. Only a failure
// to read or write is an error; a refusal is an outcome.
func (s *TgInviteService) Redeem(token string, tgId int64) (*TgInviteRedeem, error) {
	tgInviteMu.Lock()
	defer tgInviteMu.Unlock()
	var inv model.TgInvite
	err := database.GetDB().First(&inv, "token = ?", token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &TgInviteRedeem{Outcome: TgInviteUsed}, nil
	}
	if err != nil {
		return nil, err
	}
	users := &SubUserService{}
	u, err := users.Get(inv.SubId)
	if err != nil || u.Technical {
		return &TgInviteRedeem{Outcome: TgInviteUsed}, nil
	}
	out := &TgInviteRedeem{User: u}
	now := tgInviteNow()
	switch {
	case inv.UsedAt != 0 && inv.UsedBy == tgId && u.TgId == tgId:
		out.Outcome = TgInviteAlready
		return out, nil
	case inv.UsedAt != 0 || inv.Revoked:
		out.Outcome = TgInviteUsed
		return out, nil
	case !tgInviteOpen(&inv, now):
		out.Outcome = TgInviteExpired
		return out, nil
	case u.TgId != 0 && u.TgId != tgId:
		// The user got a Telegram another way since: the invite is spent.
		out.Outcome = TgInviteUsed
		return out, nil
	}
	v, err := users.SetTelegram(u.SubId, tgId)
	var conflict *SubUserConflict
	if errors.As(err, &conflict) && conflict.Code == SubUserConflictTgOwned {
		out.Outcome = TgInviteTaken
		if out.Owner, err = users.Get(conflict.OwnerSubId); err != nil {
			return nil, err
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := database.GetDB().Model(&model.TgInvite{}).Where("token = ?", token).
		Updates(map[string]any{"used_at": now.UnixMilli(), "used_by": tgId}).Error; err != nil {
		return nil, err
	}
	out.User, out.Outcome = v, TgInviteLinked
	if u.TgId == tgId {
		out.Outcome = TgInviteAlready
	}
	return out, nil
}

// TgInviteView is an invite as the users page shows it.
type TgInviteView struct {
	Token string `json:"token"`
	SubId string `json:"subId"`
	// Link is t.me/<bot>?start=<token>; "" while the bot's @username is
	// unknown (the bot is not running).
	Link      string `json:"link"`
	ExpiresAt int64  `json:"expiresAt"` // ms
}

// View is the invite with its link; nil for none.
func (s *TgInviteService) View(inv *model.TgInvite) *TgInviteView {
	if inv == nil {
		return nil
	}
	return &TgInviteView{Token: inv.Token, SubId: inv.SubId, Link: (&Tgbot{}).tgInviteLink(inv.Token), ExpiresAt: inv.ExpiresAt}
}

// Open is the open invite of an existing regular user; nil when it has none.
func (s *TgInviteService) Open(key string) (*model.TgInvite, error) {
	if err := s.checkUser(key); err != nil {
		return nil, err
	}
	return s.Current(key)
}
