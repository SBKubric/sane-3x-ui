package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// Binding a user's Telegram by hand (#187 points 1 and 4, #219,
// docs/spec/users.md §11): an admin types a tg_id or the @nick of an account
// the bot has seen. An account and a user are one to one, so an id another
// user has is refused with that user named (SubUserConflictTgOwned), and
// «Перенести сюда» (MoveTelegram) takes it from them after a confirmation.
// The invite link, the third way, is tg_invite.go.

// The Telegram refusals the caller can act on.
const (
	// SubUserConflictTgOwned: the id is another user's (Owner, OwnerSubId);
	// the admin may move it here (MoveTelegram).
	SubUserConflictTgOwned = "tg_owned"
	// SubUserConflictTgNickUnknown: no account the bot has seen has the
	// nick; the person has not written to the bot yet, an invite link will do.
	SubUserConflictTgNickUnknown = "tg_nick_unknown"
)

// tgOwnedConflict refuses tgId because it is owner's.
func tgOwnedConflict(tgId int64, owner *model.SubUser) *SubUserConflict {
	return &SubUserConflict{Code: SubUserConflictTgOwned, TgId: tgId, Owner: owner.Name, OwnerSubId: owner.SubId,
		msg: fmt.Sprintf("Telegram id %d belongs to user %s", tgId, owner.Name)}
}

// tgNickPattern is what a Telegram @nick is made of: a letter, then
// letters, digits and '_', 4 to 32 in all.
var tgNickPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

// Resolve turns what an admin typed into a Telegram id: a positive number is
// the id itself; an @nick (the '@' may be left out) is the account that has
// it now among those the bot has seen (tg_accounts), and a nick nobody has is
// a SubUserConflictTgNickUnknown refusal.
func (s *TgAccountService) Resolve(entry string) (int64, error) {
	entry = strings.TrimSpace(entry)
	if id, err := strconv.ParseInt(entry, 10, 64); err == nil {
		if id <= 0 {
			return 0, common.NewErrorf("%d is not a Telegram user id", id)
		}
		return id, nil
	}
	nick := strings.TrimPrefix(entry, "@")
	if !tgNickPattern.MatchString(nick) {
		return 0, common.NewErrorf("%q is neither a Telegram id nor an @nick", entry)
	}
	a, err := s.ByUsername(nick)
	if err != nil {
		return 0, err
	}
	if a == nil {
		return 0, &SubUserConflict{Code: SubUserConflictTgNickUnknown,
			msg: fmt.Sprintf("@%s is unknown: the person has not written to the bot yet, send them an invite link", nick)}
	}
	return a.TgId, nil
}

// telegramOf is the Telegram id a request names: tgId, or the account of
// tgNick when that is given. Both must then name the same account.
func telegramOf(tgId int64, tgNick string) (int64, error) {
	if strings.TrimSpace(tgNick) == "" {
		return tgId, nil
	}
	id, err := (&TgAccountService{}).Resolve(tgNick)
	if err != nil {
		return 0, err
	}
	if tgId != 0 && tgId != id {
		return 0, common.NewErrorf("tgId %d and tgNick %s name different Telegram accounts", tgId, tgNick)
	}
	return id, nil
}

// SetTelegramOf is SetTelegram of the id tgId, or of the account with the
// @nick tgNick (#219): the users API takes either.
func (s *SubUserService) SetTelegramOf(key string, tgId int64, tgNick string) (*SubUserView, error) {
	id, err := telegramOf(tgId, tgNick)
	if err != nil {
		return nil, err
	}
	return s.SetTelegram(key, id)
}

// MoveTelegram — «Перенести сюда» — gives the user the Telegram id tgId
// that another user has: that user is unlinked first (it and its clients
// keep no id, as UnlinkTelegram leaves them), all under one lock. An id
// nobody has is simply set.
func (s *SubUserService) MoveTelegram(key string, tgId int64) (*SubUserView, error) {
	if tgId <= 0 {
		return nil, common.NewErrorf("%d is not a Telegram user id", tgId)
	}
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return nil, err
	}
	if owner := idx.tgIdOwner(tgId, u.SubId); owner != nil {
		if err := s.setTelegramLocked(idx, owner, 0); err != nil {
			return nil, err
		}
		logger.Infof("users: Telegram id %d moved from %s to %s", tgId, owner.Name, u.Name)
	}
	if err := s.setTelegramLocked(idx, u, tgId); err != nil {
		return nil, err
	}
	return s.viewOf(u.SubId)
}
