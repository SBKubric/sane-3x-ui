package service

import (
	"strings"

	"github.com/mymmrac/telego"
)

// clientCallbacks are the buttons of the client menu (SendAnswer for a
// non-admin): the only callbacks the bot serves to a sender who is not an
// admin. Everything else is for admins.
var clientCallbacks = map[string]bool{
	"client_traffic":          true,
	"client_commands":         true,
	"client_sub_links":        true,
	"client_individual_links": true,
	"client_qr_links":         true,
}

// clientLinkCallbacks name one of the sender's clients by email after a
// space: the buttons of the client menu's link lists.
var clientLinkCallbacks = []string{"client_sub_links", "client_individual_links", "client_qr_links"}

// clientMayPress reports whether a sender who is not an admin may press a
// button carrying this data: a button of the client menu, or a link button
// naming a client of the sender's own.
func (t *Tgbot) clientMayPress(query *telego.CallbackQuery) bool {
	if clientCallbacks[query.Data] {
		return true
	}
	for _, action := range clientLinkCallbacks {
		if email, ok := strings.CutPrefix(query.Data, action+" "); ok {
			return t.ownsClient(query.From.ID, email)
		}
	}
	return false
}

// ownsClient reports whether the client of that email carries the Telegram
// user's ID.
func (t *Tgbot) ownsClient(tgUserID int64, email string) bool {
	if tgUserID == 0 || email == "" {
		return false
	}
	traffics, err := t.inboundService.GetClientTrafficTgBot(tgUserID)
	if err != nil {
		return false
	}
	for _, traffic := range traffics {
		if traffic.Email == email {
			return true
		}
	}
	return false
}

// fromAdmin reports whether the message was sent by an admin. Every chat
// state (userStates) belongs to an admin's flow, so only an admin's text may
// answer one.
func fromAdmin(message *telego.Message) bool {
	return message.From != nil && checkAdmin(message.From.ID)
}
