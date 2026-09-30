package service

import (
	"context"
	"html"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The screen (#191, docs/spec/users.md §10): an admin's chat with the bot is
// one message, edited in place, that shows the menu, a list or a card.
// /start deletes the old screen and sends a new one at the bottom; a press on
// any other message changes nothing; the admin's own text (a search, a name)
// is deleted once it is handled. Files and links still come as messages of
// their own.
//
// A screen shows a view: its route is the callback data that renders it
// again, so «⬅️ Back» re-renders the route on top of the back stack and
// «🏠 Menu» empties the stack. A view without a route (a question, a
// refusal) is transient: back leads to the view it covered. The chats'
// screens live in memory; after a restart the first press adopts the
// message it was made on and shows the main menu there.

// Callback data of the screen's own buttons, and the main menu's route.
const (
	screenBackData  = "nav_back"
	screenMenuData  = "nav_menu"
	screenMenuRoute = "s_menu"
)

// screenBackMax bounds the back stack.
const screenBackMax = 32

// screenReply is what a screen handler wants shown: the view (usersReply),
// the files that go to the chat below the screen, and anything else to do
// after the screen is updated.
type screenReply struct {
	usersReply
	files []tunnelFile
	after func(chatId int64)
}

// botScreen is one chat's screen.
type botScreen struct {
	mu    sync.Mutex
	msgID int      // the screen message; 0 = none known
	route string   // the view shown; "" = a transient view
	back  []string // the routes to go back to, the last on top
}

type screenStore struct {
	mu sync.Mutex
	m  map[int64]*botScreen
}

var botScreens = &screenStore{m: map[int64]*botScreen{}}

// of is the chat's screen, a blank one for a chat the bot knows nothing of.
func (s *screenStore) of(chatId int64) *botScreen {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.m[chatId]
	if sc == nil {
		sc = &botScreen{}
		s.m[chatId] = sc
	}
	return sc
}

func (s *screenStore) drop(chatId int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, chatId)
}

// navigate moves the screen to route. The main menu empties the stack; a
// route already on the stack cuts the stack back to it (the way back from a
// confirmation, or to a card seen before); otherwise the view left behind
// goes on top.
func (sc *botScreen) navigate(route string, root bool) {
	switch {
	case route == screenMenuRoute:
		sc.back = nil
	case root:
		sc.back = []string{screenMenuRoute}
	case route != "" && slices.Contains(sc.back, route):
		sc.back = sc.back[:slices.Index(sc.back, route)]
	case sc.route != "" && sc.route != route:
		sc.back = append(sc.back, sc.route)
		if len(sc.back) > screenBackMax {
			sc.back = sc.back[len(sc.back)-screenBackMax:]
		}
	}
	sc.route = route
}

// screenStart is /start: the main menu in a new screen at the bottom.
func (t *Tgbot) screenStart(chatId int64) {
	t.screenOpen(chatId, t.screenMainMenu())
}

// screenOpen shows reply in a new screen at the bottom, the old one deleted;
// back leads to the main menu. The commands that open a view use it.
func (t *Tgbot) screenOpen(chatId int64, reply screenReply) {
	sc := botScreens.of(chatId)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.msgID != 0 {
		t.deleteMessageTgBot(chatId, sc.msgID)
	}
	sc.msgID, sc.route, sc.back = 0, screenMenuRoute, nil
	if reply.text == "" {
		reply = t.screenMainMenu()
	}
	sc.navigate(reply.route, false)
	sc.msgID = t.screenSend(chatId, reply.text, t.screenKeyboard(sc, reply.keyboard))
	t.screenAfter(chatId, reply)
}

// screenPress handles an admin's button. A press on a message other than the
// chat's screen changes nothing.
func (t *Tgbot) screenPress(query *telego.CallbackQuery) {
	chatId := query.Message.GetChat().ID
	msgID := query.Message.GetMessageID()
	sc := botScreens.of(chatId)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.msgID == 0 {
		// The screen is unknown (the panel restarted): this message becomes
		// the screen, with the main menu.
		sc.msgID, sc.route, sc.back = msgID, "", nil
		t.sendCallbackAnswerTgBot(query.ID, "")
		t.screenShow(chatId, sc, t.screenMainMenu())
		return
	}
	if msgID != sc.msgID {
		t.sendCallbackAnswerTgBot(query.ID, t.I18nBot("tgbot.screen.stale"))
		return
	}
	delete(userStates, chatId)
	data, err := t.decodeQuery(query.Data)
	if err != nil {
		// The button's data is gone (it outlived the hash storage): show the
		// view afresh.
		data = sc.route
	}
	reply := t.screenDispatch(chatId, sc, data)
	t.sendCallbackAnswerTgBot(query.ID, reply.toast)
	t.screenShow(chatId, sc, reply)
}

// screenText shows the reply to the admin's text on the screen and deletes
// the text.
func (t *Tgbot) screenText(chatId int64, textID int, reply screenReply) {
	sc := botScreens.of(chatId)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	t.screenShow(chatId, sc, reply)
	t.deleteMessageTgBot(chatId, textID)
}

// screenDispatch turns a press into the view to show: the screen's own
// buttons, or a handler's.
func (t *Tgbot) screenDispatch(chatId int64, sc *botScreen, data string) screenReply {
	switch data {
	case screenMenuData, "":
		return t.screenMainMenu()
	case screenBackData:
		route := screenMenuRoute
		if n := len(sc.back); n > 0 {
			route, sc.back = sc.back[n-1], sc.back[:n-1]
		}
		sc.route = ""
		return t.screenRoute(chatId, route)
	}
	return t.screenRoute(chatId, data)
}

// screenRoute runs the handler of a callback.
func (t *Tgbot) screenRoute(chatId int64, data string) screenReply {
	if r, ok := t.screenMenuCallback(chatId, data); ok {
		return r
	}
	if r, ok := t.probeCallback(data); ok {
		return screenReply{usersReply: r}
	}
	if r, ok := t.usersCallback(chatId, data); ok {
		return screenReply{usersReply: r}
	}
	if r, ok := t.tunnelCallback(data); ok {
		return r
	}
	if r, ok := t.clientCardCallback(data); ok {
		return r
	}
	return t.screenMainMenu()
}

// screenShow puts reply on the screen: a new view, other buttons for the
// same view, or nothing but the toast.
func (t *Tgbot) screenShow(chatId int64, sc *botScreen, reply screenReply) {
	switch {
	case reply.text != "":
		sc.navigate(reply.route, reply.root)
		sc.msgID = t.screenEdit(chatId, sc.msgID, reply.text, t.screenKeyboard(sc, reply.keyboard))
	case reply.keyboard != nil:
		t.screenEditKeyboard(chatId, sc, t.screenKeyboard(sc, reply.keyboard))
	}
	t.screenAfter(chatId, reply)
}

// screenAfter sends the reply's files and runs its side work.
func (t *Tgbot) screenAfter(chatId int64, reply screenReply) {
	if err := t.sendTunnelFiles(chatId, reply.files); err != nil {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation")+"\r\n"+html.EscapeString(err.Error()))
	}
	if reply.after != nil {
		reply.after(chatId)
	}
}

// screenKeyboard is the view's buttons plus the way back and home; the main
// menu has neither.
func (t *Tgbot) screenKeyboard(sc *botScreen, kb *telego.InlineKeyboardMarkup) *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	if kb != nil {
		rows = append(rows, kb.InlineKeyboard...)
	}
	if sc.route != screenMenuRoute {
		var nav []telego.InlineKeyboardButton
		if len(sc.back) > 0 {
			nav = append(nav, tu.InlineKeyboardButton(t.I18nBot("tgbot.screen.back")).WithCallbackData(screenBackData))
		}
		nav = append(nav, tu.InlineKeyboardButton(t.I18nBot("tgbot.screen.home")).WithCallbackData(screenMenuData))
		rows = append(rows, nav)
	}
	return tu.InlineKeyboard(rows...)
}

// screenTextLimit keeps a screen within Telegram's 4096 characters.
const screenTextLimit = 4000

func screenFit(text string) string {
	if len([]rune(text)) <= screenTextLimit {
		return text
	}
	return string([]rune(text)[:screenTextLimit]) + "…"
}

// screenSend sends a new screen; 0 when Telegram refused it.
func (t *Tgbot) screenSend(chatId int64, text string, kb *telego.InlineKeyboardMarkup) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	msg, err := bot.SendMessage(ctx, &telego.SendMessageParams{ChatID: tu.ID(chatId), Text: screenFit(text),
		ParseMode: "HTML", ReplyMarkup: kb})
	if err != nil {
		logger.Warning("tgbot screen: send:", err)
		return 0
	}
	return msg.MessageID
}

// screenUnchanged tells Telegram's refusal to edit a message into itself.
func screenUnchanged(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

// screenEdit shows text and kb on the screen msgID; when that message is
// gone it sends a new screen instead. It returns the screen's message.
func (t *Tgbot) screenEdit(chatId int64, msgID int, text string, kb *telego.InlineKeyboardMarkup) int {
	if msgID != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := bot.EditMessageText(ctx, &telego.EditMessageTextParams{ChatID: tu.ID(chatId), MessageID: msgID,
			Text: screenFit(text), ParseMode: "HTML", ReplyMarkup: kb})
		if err == nil || screenUnchanged(err) {
			return msgID
		}
		logger.Warning("tgbot screen: edit:", err)
	}
	return t.screenSend(chatId, text, kb)
}

// screenEditKeyboard changes the screen's buttons only.
func (t *Tgbot) screenEditKeyboard(chatId int64, sc *botScreen, kb *telego.InlineKeyboardMarkup) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := bot.EditMessageReplyMarkup(ctx, &telego.EditMessageReplyMarkupParams{ChatID: tu.ID(chatId),
		MessageID: sc.msgID, ReplyMarkup: kb})
	if err != nil && !screenUnchanged(err) {
		logger.Warning("tgbot screen: edit buttons:", err)
	}
}
