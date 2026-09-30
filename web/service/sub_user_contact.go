package service

import (
	"net/mail"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The checks the bot's «New user» dialogue (#193) runs a step at a time,
// before Create runs them all again: a name, a contact email and a
// Telegram id each refused on the step that asks for it.

// subUserMaxContactEmail bounds a contact email, as RFC 5321 bounds a path.
const subUserMaxContactEmail = 254

// CheckContactEmail returns the contact email trimmed, "" for none, or why it
// is not one: a bare address (no display name, no list) with a dotted domain.
func CheckContactEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", nil
	}
	bad := common.NewErrorf("%q is not an email address", email)
	if len(email) > subUserMaxContactEmail {
		return "", bad
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", bad
	}
	at := strings.LastIndex(email, "@")
	if domain := email[at+1:]; !strings.Contains(strings.Trim(domain, "."), ".") {
		return "", bad
	}
	return email, nil
}

// CheckNewName reports why a new user may not be called name (trimmed), as
// Create would refuse it: empty, too long, reserved or taken.
func (s *SubUserService) CheckNewName(name string) error {
	idx, err := s.synced()
	if err != nil {
		return err
	}
	return idx.checkNewUserName(strings.TrimSpace(name))
}

// CheckNewTgId reports why a new user may not have the Telegram id tgId: a
// Telegram account and a user are one to one (map #178), so an id that is
// already another user's, or on another user's client, is refused. 0 is no
// id and always passes.
func (s *SubUserService) CheckNewTgId(tgId int64) error {
	idx, err := s.synced()
	if err != nil {
		return err
	}
	return idx.checkNewTgId(tgId)
}

func (idx *subUserIndex) checkNewTgId(tgId int64) error {
	switch {
	case tgId == 0:
		return nil
	case tgId < 0:
		return common.NewErrorf("%d is not a Telegram user id", tgId)
	}
	for _, u := range idx.sortedUsers() {
		if !u.IsTechnical() && u.TgId == tgId {
			return common.NewErrorf("Telegram id %d belongs to user %s", tgId, u.Name)
		}
	}
	for _, c := range idx.clients {
		if u := idx.users[c.owner]; c.TgId == tgId && u != nil && !u.IsTechnical() {
			return common.NewErrorf("Telegram id %d belongs to user %s (client %s)", tgId, u.Name, c.Name)
		}
	}
	return nil
}
