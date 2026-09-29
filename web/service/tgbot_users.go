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
// collects the operator's choices. Admins only: the upstream handlers call in
// here from their admin branches.
//
// Each handler turns a button (callback data) or a typed text into a
// usersReply; answerUsersCallback and answerUsersText hand the reply to
// Telegram. The callback data of these flows starts with "usr_", plus the
// upstream "add_client" button, which the create flow takes over.

// usersReply is what a users handler wants shown.
type usersReply struct {
	toast    string // the callback's answer; "" = a plain acknowledgement
	text     string // message text (HTML); "" with edit = keep the text, change the keyboard
	keyboard *telego.InlineKeyboardMarkup
	// edit replaces the message the button is on instead of sending a new one.
	edit bool
}

// Chat states (userStates) of the users flows: the text the chat waits for.
const (
	usersStateName    = "usr_name"
	usersStateComment = "usr_comment"
	usersStateSearch  = "usr_search"
	usersStateAssign  = "usr_assign"
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

// answerUsersCallback handles a button of the users flows; false for data
// that is not theirs.
func (t *Tgbot) answerUsersCallback(query *telego.CallbackQuery, data string) bool {
	chatId := query.Message.GetChat().ID
	reply, ok := t.usersCallback(chatId, data)
	if !ok {
		return false
	}
	t.sendCallbackAnswerTgBot(query.ID, reply.toast)
	t.showUsersReply(chatId, query.Message.GetMessageID(), reply)
	return true
}

// answerUsersText handles a text the chat was asked for by a users flow;
// false for the states of other flows.
func (t *Tgbot) answerUsersText(message *telego.Message, state string) bool {
	if !strings.HasPrefix(state, "usr_") {
		return false
	}
	if message.From == nil || !checkAdmin(message.From.ID) {
		return true
	}
	reply, _ := t.usersText(message.Chat.ID, state, message.Text)
	t.showUsersReply(message.Chat.ID, 0, reply)
	return true
}

func (t *Tgbot) showUsersReply(chatId int64, messageID int, r usersReply) {
	switch {
	case r.edit && messageID != 0 && r.text == "":
		if r.keyboard != nil {
			t.editMessageCallbackTgBot(chatId, messageID, r.keyboard)
		}
	case r.edit && messageID != 0 && r.keyboard != nil:
		t.editMessageTgBot(chatId, messageID, r.text, r.keyboard)
	case r.edit && messageID != 0:
		t.editMessageTgBot(chatId, messageID, r.text)
	case r.text != "" && r.keyboard != nil:
		t.SendMsgToTgbot(chatId, r.text, r.keyboard)
	case r.text != "":
		t.SendMsgToTgbot(chatId, r.text)
	}
}

// usersCallback runs a button of the users flows. ok is false for data that
// is not theirs.
func (t *Tgbot) usersCallback(chatId int64, data string) (reply usersReply, ok bool) {
	action, args, _ := strings.Cut(data, " ")
	key, rest, _ := strings.Cut(args, " ")
	n, _ := strconv.Atoi(args)
	id, _ := strconv.Atoi(rest)
	switch action {
	// create
	case "add_client":
		return t.usersCreateStart(chatId), true
	case "usr_t":
		return t.usersCreateToggle(chatId, n), true
	case "usr_pr":
		return t.usersCreateProtocols(chatId), true
	case "usr_next":
		return t.usersCreateNext(chatId), true
	case "usr_nm":
		return t.usersAsk(chatId, usersStateName, t.I18nBot("tgbot.users.namePrompt")), true
	case "usr_cm":
		return t.usersAsk(chatId, usersStateComment, t.I18nBot("tgbot.users.commentPrompt")), true
	case "usr_sum":
		return t.usersDraftReply(chatId, true), true
	case "usr_tr", "usr_ex", "usr_ip":
		return usersReply{keyboard: t.usersLimitKeyboard(action), edit: true}, true
	case "usr_trs", "usr_exs", "usr_ips":
		return t.usersSetLimit(chatId, action, n), true
	case "usr_tri", "usr_exi", "usr_ipi":
		return t.usersKeypadPress(action, args), true
	case "usr_ok":
		return t.usersCreateSubmit(chatId, false), true
	case "usr_lnk":
		return t.usersCreateSubmit(chatId, true), true
	case "usr_x":
		usersSessions.drop(chatId)
		return usersReply{text: t.I18nBot("tgbot.messages.cancel"), edit: true}, true
	// the Users section
	case "usr_menu":
		return t.usersMenu(chatId), true
	case "usr_c":
		return t.usersCardReply(args, true), true
	case "usr_apc":
		return t.usersAddFromClient(args), true
	case "usr_apm":
		return t.usersAddMenu(args, true), true
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
		return t.usersDelete(args), true
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
	case usersStateName:
		if text == "" {
			return t.usersAsk(chatId, usersStateName, t.I18nBot("tgbot.users.namePrompt")), true
		}
		if !t.usersEditDraft(chatId, func(d *usersDraft) { d.name = text }) {
			return t.usersExpired(), true
		}
		return t.usersDraftReply(chatId, false), true
	case usersStateComment:
		if !t.usersEditDraft(chatId, func(d *usersDraft) { d.comment = text }) {
			return t.usersExpired(), true
		}
		return t.usersDraftReply(chatId, false), true
	case usersStateSearch:
		// A probe's name opens its read-only card (#183).
		if reply, ok := t.probeSearch(text); ok {
			return reply, true
		}
		v, err := (&SubUserService{}).Find(text)
		if err != nil {
			userStates[chatId] = usersStateSearch
			return t.usersError(err), true
		}
		return t.usersCardReply(v.SubId, false), true
	case usersStateAssign:
		return t.usersAssign(chatId, text), true
	}
	return usersReply{}, false
}

// usersAsk makes the chat wait for a text.
func (t *Tgbot) usersAsk(chatId int64, state, prompt string) usersReply {
	userStates[chatId] = state
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
