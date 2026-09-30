package service

import (
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/web/entity"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// «⚙️ Server» → «📣 Notification channel» (#202): the admin sets, tests and
// disables the notification channel (#195) from the bot. The screen shows
// the channel (tgNotifyChatId) and the last test message's outcome, and
// waits for the channel: an @username, a chat id, or any post forwarded
// from the channel, whose id the bot takes from the forward's origin. The
// admin's message is deleted once it is handled. Saving writes the setting
// the panel's settings tab writes and at once sends the test message the
// tab's «Send test» sends (SendNotifyTest); the bot reads the setting on
// every notification, so nothing needs a restart. Admins only.

// Callback data of the screen.
const (
	screenNotifyRoute    = "s_ntf"   // the screen
	screenNotifyTestData = "s_ntft"  // «🧪 Test»
	screenNotifyOffAsk   = "s_ntfo"  // «Disable the channel?»
	screenNotifyOffData  = "s_ntfoc" // disabling, confirmed
)

// notifyStateChannel is the chat state (userStates) of the screen: it waits
// for the channel.
const notifyStateChannel = "ntf_chan"

// notifyTestResult is how a test message went.
type notifyTestResult struct {
	value string // the channel as tested
	err   string // Telegram's refusal; "" = sent
	at    time.Time
}

// notifyTestRecord keeps the last test message, whether the bot or the
// panel's settings tab sent it. It lives in memory: after a restart the
// screen has no test to show.
type notifyTestRecord struct {
	mu   sync.Mutex
	last *notifyTestResult
}

var notifyLastTest = &notifyTestRecord{}

func (r *notifyTestRecord) record(value string, err error) {
	result := &notifyTestResult{value: strings.TrimSpace(value), at: time.Now()}
	if err != nil {
		result.err = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = result
}

// of is the last test when it went to value; false for none, or for a test
// of another channel (one typed in the settings tab and not saved).
func (r *notifyTestRecord) of(value string) (notifyTestResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil || r.last.value != value {
		return notifyTestResult{}, false
	}
	return *r.last, true
}

// notifyCallback runs the screen's buttons; false for data that is not
// theirs.
func (t *Tgbot) notifyCallback(chatId int64, data string) (screenReply, bool) {
	switch data {
	case screenNotifyRoute:
		return t.screenNotify(chatId), true
	case screenNotifyTestData:
		return t.screenNotifyTest(chatId), true
	case screenNotifyOffAsk:
		return t.screenConfirm(t.I18nBot("tgbot.notify.offAsk"), screenNotifyOffData), true
	case screenNotifyOffData:
		if err := t.settingService.SetTgNotifyChatId(""); err != nil {
			return t.screenNotifySaying(chatId, t.notifyFailed(err)), true
		}
		return t.screenNotifySaying(chatId, t.I18nBot("tgbot.notify.offDone")), true
	}
	return screenReply{}, false
}

// answerNotifyText handles a message the screen waits for; false for the
// states of other flows. Someone who is not an admin changes nothing.
func (t *Tgbot) answerNotifyText(message *telego.Message, state string) bool {
	if state != notifyStateChannel {
		return false
	}
	if !fromAdmin(message) {
		return true
	}
	chatId := message.Chat.ID
	userStates.clear(chatId)
	t.screenText(chatId, message.MessageID, t.notifySet(chatId, message))
	return true
}

// notifySet makes the channel the message names the notification channel
// and sends it the test message; the screen says how both went.
func (t *Tgbot) notifySet(chatId int64, message *telego.Message) screenReply {
	value, refusal := notifyChannelOf(message)
	if refusal != "" {
		return t.screenNotifySaying(chatId, t.I18nBot(refusal))
	}
	if err := t.settingService.SetTgNotifyChatId(value); err != nil {
		return t.screenNotifySaying(chatId, t.notifyFailed(err))
	}
	_ = t.SendNotifyTest(value) // the screen shows the outcome as the last test
	return t.screenNotifySaying(chatId, t.I18nBot("tgbot.notify.saved", "Channel=="+notifyCode(value)))
}

// notifyChannelOf is the channel a message names: the chat a forwarded post
// comes from (a channel, or a group posting as itself), else the text, an
// @username or a chat id. refusal is the key of the words for a message
// that names no channel.
func notifyChannelOf(message *telego.Message) (value, refusal string) {
	switch origin := message.ForwardOrigin.(type) {
	case nil:
	case *telego.MessageOriginChannel:
		return strconv.FormatInt(origin.Chat.ID, 10), ""
	case *telego.MessageOriginChat:
		return strconv.FormatInt(origin.SenderChat.ID, 10), ""
	default:
		return "", "tgbot.notify.notFromChannel"
	}
	value = strings.TrimSpace(message.Text)
	if value == "" || !entity.ValidTgNotifyChatId(value) {
		return "", "tgbot.notify.notChannel"
	}
	return value, ""
}

// screenNotifyTest sends the test message to the channel set and shows the
// screen with its outcome.
func (t *Tgbot) screenNotifyTest(chatId int64) screenReply {
	toast := ""
	if value, _ := t.settingService.GetTgNotifyChatId(); value != "" {
		toast = t.I18nBot("tgbot.notify.testSent")
		if err := t.SendNotifyTest(value); err != nil {
			toast = t.I18nBot("tgbot.notify.testFailed")
		}
	}
	reply := t.screenNotify(chatId)
	reply.toast = toast
	return reply
}

// screenNotifySaying is the screen with a line on top: how an action went.
func (t *Tgbot) screenNotifySaying(chatId int64, result string) screenReply {
	reply := t.screenNotify(chatId)
	reply.text = result + "\r\n\r\n" + reply.text
	return reply
}

// screenNotify is the screen: the channel, the last test, how to set the
// channel; it waits for the channel.
func (t *Tgbot) screenNotify(chatId int64) screenReply {
	value, err := t.settingService.GetTgNotifyChatId()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	userStates.set(chatId, notifyStateChannel)
	button := func(key, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(t.I18nBot(key)).WithCallbackData(data)
	}
	lines := []string{t.I18nBot("tgbot.notify.title", "Channel=="+t.I18nBot("tgbot.notify.unset"))}
	var rows [][]telego.InlineKeyboardButton
	if value != "" {
		lines = []string{t.I18nBot("tgbot.notify.title", "Channel=="+notifyCode(value)), t.notifyLastTestLine(value)}
		rows = append(rows, tu.InlineKeyboardRow(button("tgbot.notify.test", screenNotifyTestData),
			button("tgbot.notify.off", screenNotifyOffAsk)))
	}
	rows = append(rows, tu.InlineKeyboardRow(button("tgbot.buttons.refresh", screenNotifyRoute)))
	lines = append(lines, "", t.I18nBot("tgbot.notify.howTo"))
	return screenReply{usersReply: usersReply{text: strings.Join(lines, "\r\n"), keyboard: tu.InlineKeyboard(rows...),
		route: screenNotifyRoute}}
}

// notifyLastTestLine words the last test of the channel value.
func (t *Tgbot) notifyLastTestLine(value string) string {
	last, ok := notifyLastTest.of(value)
	switch {
	case !ok:
		return t.I18nBot("tgbot.notify.noTest")
	case last.err != "":
		return t.I18nBot("tgbot.notify.lastFailed", "Error=="+html.EscapeString(last.err),
			"Time=="+last.at.Format("2006-01-02 15:04:05"))
	}
	return t.I18nBot("tgbot.notify.lastOk", "Time=="+last.at.Format("2006-01-02 15:04:05"))
}

// notifyFailed words a failure to store the setting.
func (t *Tgbot) notifyFailed(err error) string {
	return t.I18nBot("tgbot.notify.saveFailed", "Error=="+html.EscapeString(err.Error()))
}

// notifyCode is a channel as the screen shows it.
func notifyCode(value string) string {
	return "<code>" + html.EscapeString(value) + "</code>"
}
