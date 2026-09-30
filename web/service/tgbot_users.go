package service

import (
	"errors"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The Telegram bot's users flows (#169, docs/spec/users.md): creating a user
// with several protocols at once, and the «Users» section — search, the user
// card and its buttons. Everything goes through SubUserService; the bot only
// collects the operator's choices. Admins only: the admin's screen
// (tgbot_screen.go) calls in here.
//
// Each handler turns a button (callback data) or a typed text into a
// usersReply; the screen shows it, answerUsersText for a text. The callback
// data of these flows starts with "usr_"; «➕ New user» is the dialogue of
// tgbot_screen_newuser.go, which takes over the upstream "add_client" button.

// usersReply is what a users handler wants shown.
type usersReply struct {
	toast    string // the callback's answer; "" = a plain acknowledgement
	text     string // message text (HTML); "" = keep the text, change the keyboard
	keyboard *telego.InlineKeyboardMarkup
	// route is the callback data that shows this view again, for the
	// screen's way back (tgbot_screen.go); "" for a view that cannot be
	// shown again, such as a question or a refusal.
	route string
	// root leaves only the main menu to go back to: the view shown before
	// is gone, as a deleted user's card is.
	root bool
}

// Chat states (userStates) of the users flows: the text the chat waits for.
const (
	usersStateSearch = "usr_search"
	usersStateAssign = "usr_assign"
)

// usersSession is one chat's state between two messages.
type usersSession struct {
	draft *usersDraft
	// assignClient is the robot client the chat is asked a user for.
	assignClient string
}

type usersSessionStore struct {
	mu sync.Mutex
	m  map[int64]*usersSession
}

var usersSessions = &usersSessionStore{m: map[int64]*usersSession{}}

// with runs fn on the chat's session under the store's lock.
func (s *usersSessionStore) with(chatId int64, fn func(*usersSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.m[chatId]
	if sess == nil {
		sess = &usersSession{}
		s.m[chatId] = sess
	}
	fn(sess)
}

func (s *usersSessionStore) drop(chatId int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, chatId)
}

// answerUsersText handles a text the chat was asked for by a users flow;
// false for the states of other flows. The answer goes on the chat's screen
// and the text is deleted (#191).
func (t *Tgbot) answerUsersText(message *telego.Message, state string) bool {
	if !strings.HasPrefix(state, "usr_") {
		return false
	}
	if !fromAdmin(message) {
		return true
	}
	userStates.clear(message.Chat.ID)
	reply, _ := t.usersText(message.Chat.ID, state, message.Text)
	t.screenText(message.Chat.ID, message.MessageID, screenReply{usersReply: reply})
	return true
}

// usersCallback runs a button of the users flows. ok is false for data that
// is not theirs.
func (t *Tgbot) usersCallback(chatId int64, data string) (reply usersReply, ok bool) {
	// «➕ New user» (#193): tgbot_screen_newuser.go.
	if reply, ok := t.newUserCallback(chatId, data); ok {
		return reply, true
	}
	// The user's Telegram (#186): tgbot_users_telegram.go.
	if reply, ok := t.usersTelegramCallback(data); ok {
		return reply, true
	}
	action, args, _ := strings.Cut(data, " ")
	key, rest, _ := strings.Cut(args, " ")
	n, _ := strconv.Atoi(args)
	id, _ := strconv.Atoi(rest)
	switch action {
	case "usr_x":
		usersSessions.drop(chatId)
		menu := t.screenMainMenu().usersReply
		menu.toast = t.I18nBot("tgbot.messages.cancel")
		return menu, true
	// the Users section
	case "usr_menu":
		return t.usersList(chatId, 0), true
	case usersListAction:
		return t.usersList(chatId, n), true
	case usersFoundAction:
		return t.usersFound(chatId, args), true
	case usersSubAction:
		return t.usersSubscription(args), true
	case "usr_c":
		return t.usersCardReply(args), true
	case "usr_apm":
		return t.usersAddMenu(args), true
	case "usr_ap", "usr_apl":
		return t.usersAddProtocol(key, id, action == "usr_apl"), true
	case "usr_rpm":
		return t.usersRemoveMenu(args), true
	case "usr_rp":
		return t.usersRemoveConfirm(key, id), true
	case "usr_rpc":
		return t.usersRemoveProtocol(key, id), true
	case "usr_en":
		return t.usersSetEnable(key, rest == "1"), true
	case "usr_del":
		return t.usersDeleteConfirm(args), true
	case "usr_delc":
		return t.usersDelete(chatId, args), true
	case "usr_rob":
		return t.usersRobotList(n), true
	case "usr_as":
		usersSessions.with(chatId, func(s *usersSession) { s.assignClient = args })
		return t.usersAsk(chatId, usersStateAssign, t.I18nBot("tgbot.users.assignPrompt", "Client=="+html.EscapeString(args))), true
	}
	return usersReply{}, false
}

// usersText runs a text the chat was asked for. ok is false for the states of
// other flows.
func (t *Tgbot) usersText(chatId int64, state, text string) (reply usersReply, ok bool) {
	text = strings.TrimSpace(text)
	switch state {
	case usersStateNewUser:
		return t.newUserText(chatId, text), true
	case usersStateSearch:
		// A probe's name opens its read-only card (#183).
		if reply, ok := t.probeSearch(text); ok {
			return reply, true
		}
		return t.usersSearchReply(chatId, text), true
	case usersStateAssign:
		return t.usersAssign(chatId, text), true
	}
	return usersReply{}, false
}

// usersAsk makes the chat wait for a text.
func (t *Tgbot) usersAsk(chatId int64, state, prompt string) usersReply {
	userStates.set(chatId, state)
	return usersReply{text: prompt, keyboard: tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("usr_x")))}
}

// --- replies -----------------------------------------------------------------

// usersError shows a refusal or failure of the service as it is worded.
func (t *Tgbot) usersError(err error) usersReply {
	return usersReply{toast: t.I18nBot("tgbot.answers.errorOperation"),
		text: t.I18nBot("tgbot.messages.error_add_client", "error=="+html.EscapeString(err.Error()))}
}

// usersExpired answers a button whose draft is gone (a restart, or another
// flow started in the chat).
func (t *Tgbot) usersExpired() usersReply {
	return usersReply{toast: t.I18nBot("tgbot.users.expired"), text: t.I18nBot("tgbot.users.expired")}
}

// awgLinkable returns the conflict when err asks the operator whether to link
// an existing AmneziaWG client.
func awgLinkable(err error) (*SubUserConflict, bool) {
	var conflict *SubUserConflict
	if errors.As(err, &conflict) && conflict.Code == SubUserConflictAwgLinkable {
		return conflict, true
	}
	return nil, false
}

// usersExpiry words a client's expiry: none, a date, or days after first use.
func (t *Tgbot) usersExpiry(ms int64) string {
	switch {
	case ms == 0:
		return t.I18nBot("tgbot.unlimited")
	case ms < 0:
		return t.I18nBot("tgbot.users.afterFirstUse", "Days=="+strconv.FormatInt(ms/-86400000, 10))
	default:
		return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
	}
}

// usersTraffic words a traffic limit in bytes, 0 = unlimited.
func (t *Tgbot) usersTraffic(bytes int64) string {
	if bytes == 0 {
		return t.I18nBot("tgbot.unlimited")
	}
	return common.FormatTraffic(bytes)
}
