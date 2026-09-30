package service

import (
	"errors"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The applicant's screens (#188 points 1–3, 8, 12, #220, docs/spec/users.md
// §12), on the client's screen (tgbot_screen_mysub.go) of someone with no
// user:
//   - «No subscription» offers «📝 Leave a request» (requestNewRoute) while
//     the rules let the account ask; otherwise it shows the pending request
//     («Request sent <date>, awaiting a decision» with «Cancel the
//     request»), the rejection with its reason and the date a new request
//     becomes possible, or that the account is blocked;
//   - «Leave a request» shows the captcha step: a web_app button opening the
//     captcha page (package captcha) on the active edge, and «I passed the
//     check», which looks again. The captcha's pass brings the comment step
//     to the chat by itself (requestCaptchaPassed);
//   - the comment step waits for a text of up to 200 characters, or «Skip»;
//     then the request is sent, the text deleted, the notification channel
//     told, and the screen shows the pending request.
//
// There is no way without the Mini App (point 12). The routes act on the
// sender's own request only and are pressed in their private chat
// (tgbot_access.go).

// Callback data and the chat state of the applicant's screens.
const (
	requestNewRoute     = "req_new"    // the captcha step, or the comment step once it is passed
	requestSkipAction   = "req_skip"   // send the request without a comment
	requestCancelAction = "req_cancel" // take the pending request back
	requestCommentState = "req_cmt"    // the chat waits for the comment
)

// requestDateLayout is how the screens write a date and time.
const requestDateLayout = "02.01.2006 15:04"

func requestDate(ms int64) string {
	return time.UnixMilli(ms).Local().Format(requestDateLayout)
}

// requestRoute runs a press of the applicant's screens; false for data that
// is not theirs.
func (t *Tgbot) requestRoute(tgId int64, data string) (screenReply, bool) {
	switch data {
	case requestNewRoute:
		return t.requestNew(tgId), true
	case requestSkipAction:
		return t.requestSubmit(tgId, ""), true
	case requestCancelAction:
		r, err := (&SubRequestService{}).Cancel(tgId)
		if err != nil {
			return screenReply{usersReply: t.usersError(err)}, true
		}
		reply := t.mysubHome(tgId)
		if r != nil {
			reply.toast = t.I18nBot("tgbot.request.cancelled")
		}
		return reply, true
	}
	return screenReply{}, false
}

// requestNone is «No subscription» with where the account stands with
// requests.
func (t *Tgbot) requestNone(tgId int64) screenReply {
	button := func(key, data string) []telego.InlineKeyboardButton {
		return tu.InlineKeyboardRow(t.mysubButton(t.I18nBot(key), data))
	}
	text := t.I18nBot("tgbot.mysub.none", "TgId=="+strconv.FormatInt(tgId, 10))
	var rows [][]telego.InlineKeyboardButton
	st, err := (&SubRequestService{}).Status(tgId)
	switch {
	case err != nil:
		logger.Warning("requests:", err)
	case st.Blocked:
		text += "\r\n\r\n" + t.I18nBot("tgbot.request.blocked")
	case st.Pending != nil:
		text = t.I18nBot("tgbot.request.pending", "Date=="+requestDate(st.Pending.CreatedAt))
		rows = append(rows, button("tgbot.request.cancel", requestCancelAction))
	case st.Rejected != nil:
		reason := st.Rejected.Reason
		if reason == "" {
			reason = t.I18nBot("tgbot.request.noReason")
		}
		text = t.I18nBot("tgbot.request.rejected", "Date=="+requestDate(st.Rejected.CreatedAt),
			"Reason=="+html.EscapeString(reason), "Next=="+requestDate(st.NextAt))
	default:
		rows = append(rows, button("tgbot.request.leave", requestNewRoute))
	}
	rows = append(rows, button("tgbot.buttons.refresh", screenMenuRoute))
	return screenReply{usersReply: usersReply{text: text, keyboard: tu.InlineKeyboard(rows...), route: screenMenuRoute}}
}

// requestNew is «Leave a request»: the comment step while the captcha's 30
// minutes run, the captcha step before. An account that may not ask gets its
// main menu.
func (t *Tgbot) requestNew(tgId int64) screenReply {
	st, err := (&SubRequestService{}).Status(tgId)
	if err != nil || !st.CanApply() {
		return t.mysubHome(tgId)
	}
	if subRequestWindows.open(tgId) {
		return t.requestCommentStep(tgId, "")
	}
	return t.requestCaptchaStep("")
}

// requestCaptchaStep asks for the captcha, with warning above when the
// person comes back to it.
func (t *Tgbot) requestCaptchaStep(warning string) screenReply {
	text := t.I18nBot("tgbot.request.captcha")
	if warning != "" {
		text = warning + "\r\n\r\n" + text
	}
	var rows [][]telego.InlineKeyboardButton
	if url := t.requestCaptchaURL(); url != "" {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.request.captchaButton")).
			WithWebApp(&telego.WebAppInfo{URL: url})))
	} else {
		text += "\r\n\r\n" + t.I18nBot("tgbot.request.captchaUnavailable")
	}
	rows = append(rows, tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.request.captchaDone"), requestNewRoute)))
	return screenReply{usersReply: usersReply{text: text, keyboard: tu.InlineKeyboard(rows...), route: requestNewRoute}}
}

// requestCaptchaURL is the captcha page on the subscription path as clients
// reach it — the active edge behind a chain — or "" when that is not https,
// which is all Telegram opens a Mini App on.
func (t *Tgbot) requestCaptchaURL() string {
	url, _ := t.subscriptionURLs(captcha.Segment)
	if !strings.HasPrefix(url, "https://") {
		return ""
	}
	return url
}

// requestCommentStep asks for the comment and makes the chat wait for it,
// with warning above after a refusal.
func (t *Tgbot) requestCommentStep(tgId int64, warning string) screenReply {
	userStates.set(tgId, requestCommentState)
	text := t.I18nBot("tgbot.request.comment")
	if warning != "" {
		text = warning + "\r\n\r\n" + text
	}
	kb := tu.InlineKeyboard(tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.request.skip"), requestSkipAction)))
	return screenReply{usersReply: usersReply{text: text, keyboard: kb, route: requestNewRoute}}
}

// requestSubmit sends the account's request with comment and shows where it
// stands; a refusal shows the step to go back to.
func (t *Tgbot) requestSubmit(tgId int64, comment string) screenReply {
	r, err := (&SubRequestService{}).Create(tgId, comment)
	var refusal *SubRequestRefusal
	switch {
	case errors.As(err, &refusal) && refusal.Code == SubRequestCommentTooLong:
		return t.requestCommentStep(tgId, t.I18nBot("tgbot.request.tooLong",
			"Count=="+strconv.Itoa(len([]rune(strings.TrimSpace(comment))))))
	case errors.As(err, &refusal) && refusal.Code == SubRequestNeedsCaptcha:
		return t.requestCaptchaStep(t.I18nBot("tgbot.request.captchaAgain"))
	case errors.As(err, &refusal):
		return t.mysubHome(tgId)
	case err != nil:
		return screenReply{usersReply: t.usersError(err)}
	}
	t.requestNotify(r)
	reply := t.mysubHome(tgId)
	reply.toast = t.I18nBot("tgbot.request.sent")
	return reply
}

// requestNotify tells the notification channel of a new request, as text:
// the admins review it in the bot (#221).
func (t *Tgbot) requestNotify(r *model.SubRequest) {
	text := t.I18nBot("tgbot.request.notify", "Account=="+requestAccountLabel(r.TgId))
	if r.Comment != "" {
		text += "\r\n" + t.I18nBot("tgbot.request.notifyComment", "Comment=="+html.EscapeString(r.Comment))
	}
	t.SendMsgToNotifyChannel(text)
}

// requestAccountLabel names the Telegram account tgId for the admins as the bot
// last saw it: its @nick, else its name and id.
func requestAccountLabel(tgId int64) string {
	u := telego.User{ID: tgId}
	if a, err := (&TgAccountService{}).Get(tgId); err == nil && a != nil {
		u.Username, u.FirstName, u.LastName = a.Username, a.FirstName, a.LastName
	}
	return tgSenderLabel(u)
}

// answerRequestText takes the comment of a chat that waits for one; false
// for another state. Only the applicant, in their private chat, answers it.
func (t *Tgbot) answerRequestText(message *telego.Message, state string) bool {
	if state != requestCommentState {
		return false
	}
	if message.From == nil || message.From.ID != message.Chat.ID || checkAdmin(message.From.ID) {
		return true
	}
	tgId := message.From.ID
	userStates.clear(message.Chat.ID)
	var reply screenReply
	if message.Text == "" {
		reply = t.requestCommentStep(tgId, t.I18nBot("tgbot.request.textOnly"))
	} else {
		reply = t.requestSubmit(tgId, message.Text)
	}
	t.requestShow(tgId, reply, message.MessageID)
	return true
}

// requestCaptchaPassed brings the comment step to the chat of the account
// that has just passed the captcha — or its main menu, when it may not ask.
func (t *Tgbot) requestCaptchaPassed(tgId int64) {
	t.requestShow(tgId, t.requestNew(tgId), 0)
}

// requestShow puts reply on the screen of the applicant's private chat and
// deletes their text textID (0 for none).
func (t *Tgbot) requestShow(tgId int64, reply screenReply, textID int) {
	sc := botScreens.of(tgId)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.client = tgId
	t.screenShow(tgId, sc, reply)
	if textID != 0 {
		t.deleteMessageTgBot(tgId, textID)
	}
}

// ExpireSubRequests expires the requests nobody decided on in 14 days and
// tells each person; the requests' job runs it.
func (t *Tgbot) ExpireSubRequests() {
	due, err := (&SubRequestService{}).ExpireDue()
	if err != nil {
		logger.Warning("requests: expire:", err)
	}
	for _, r := range due {
		t.SendMsgToTgbot(r.TgId, t.I18nBot("tgbot.request.expired", "Date=="+requestDate(r.CreatedAt)))
	}
}

// mysubSupport is the row «Write to the admin» — the Support-Url of the
// settings — for a paused or expired subscription; nil when there is no
// link Telegram opens.
func (t *Tgbot) mysubSupport() []telego.InlineKeyboardButton {
	url, err := t.settingService.GetSubSupportUrl()
	url = strings.TrimSpace(url)
	if err != nil || !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "tg://")) {
		return nil
	}
	return tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.request.writeAdmin")).WithURL(url))
}
