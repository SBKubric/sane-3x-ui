package service

import (
	"slices"
	"strings"

	"github.com/mymmrac/telego"
)

// clientCallbacks are the buttons the bot serves as they are to a sender
// who is not an admin: the main menu of their own screens (#194) with the
// way back and home, and the buttons of the old client menu, which open
// those screens now. Everything else is for admins, except the routes below
// that name something of the sender's own.
var clientCallbacks = map[string]bool{
	screenMenuRoute:           true,
	screenMenuData:            true,
	screenBackData:            true,
	"client_traffic":          true,
	"client_commands":         true,
	"client_sub_links":        true,
	"client_individual_links": true,
	"client_qr_links":         true,
	// The applicant's own request (#220): only in their private chat.
	requestNewRoute:     true,
	requestSkipAction:   true,
	requestCancelAction: true,
}

// requestCallbacks act on the sender's own request (tgbot_screen_request.go):
// pressed only in the sender's private chat with the bot, whose screen they
// change and where the comment is asked for.
var requestCallbacks = []string{requestNewRoute, requestSkipAction, requestCancelAction}

// clientUserRoutes name one of the sender's users by subId after a space:
// the client's screens (#194).
var clientUserRoutes = []string{mysubUserRoute, mysubSubRoute, mysubConfigsRoute, mysubLinksAction}

// clientLinkCallbacks name one of the sender's clients by email after a
// space: the old client menu's link lists and an old client card's usage,
// which open the client's screens now.
var clientLinkCallbacks = []string{"client_sub_links", "client_individual_links", "client_qr_links", screenClientAction}

// clientMayPress reports whether a sender who is not an admin may press a
// button carrying this data: one of clientCallbacks, or a route naming a
// user of the sender's own, a tunnel client of theirs by uuid, or a client
// of theirs by email. telegramSubUsers says whose they are. Data longer
// than Telegram takes comes as a hash (encodeQuery), and the check is on
// what it stands for. A hash the bot no longer knows (#199) stands for
// nothing: it may be pressed, and the chat's screen shows its view afresh
// (screenPressAs), through the client's routes, which check it again.
func (t *Tgbot) clientMayPress(query *telego.CallbackQuery) bool {
	data, err := t.decodeQuery(query.Data)
	if err != nil {
		return true
	}
	if clientCallbacks[data] {
		return !slices.Contains(requestCallbacks, data) ||
			query.Message != nil && query.Message.GetChat().ID == query.From.ID
	}
	action, arg, _ := strings.Cut(data, " ")
	if arg == "" {
		return false
	}
	from := query.From.ID
	switch {
	case slices.Contains(clientUserRoutes, action):
		return telegramSubUser(from, arg) != nil
	case action == mysubTunnelAction:
		return telegramTunnelUser(from, arg) != nil
	case slices.Contains(clientLinkCallbacks, action):
		return telegramClientUser(from, arg) != nil
	}
	return false
}

// fromAdmin reports whether the message was sent by an admin. Every chat
// state (userStates) but the applicant's comment belongs to an admin's flow,
// so only an admin's text may answer one; the comment (requestCommentState)
// only the applicant answers, in their private chat (answerRequestText).
func fromAdmin(message *telego.Message) bool {
	return message.From != nil && checkAdmin(message.From.ID)
}
