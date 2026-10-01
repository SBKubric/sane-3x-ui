package service

import (
	"context"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The link broadcast in the bot (#214, #222, docs/spec/users.md §13), admins
// only:
//   - the question: when the links change (sub_link.go), every admin gets
//     «Send the new link to N users?» with «✅ Yes» / «✖ No» in their
//     private chat, once the changes have settled for subLinkQuiet — a burst
//     makes one question. Nothing goes to the users without a «Yes». The
//     question is a message of its own, not the screen; whoever answers
//     first answers for all, and the others' questions say so;
//   - «📣 Send links» on «⚙️ Server»: the screen with the audience and the
//     journal's last broadcasts, «📣 Send to all (N)» behind a confirmation;
//   - «📣 Send the link» on a user's card, behind a confirmation;
//   - the report's «📋 Links for manual sending».
//
// The question's and the report's buttons are pressed on messages that are
// not the screen, so answerCallback hands them here before the screen
// (subLinkPress); someone who is no admin never gets here — clientMayPress
// lets none of them through.

// Callback data of the broadcast.
const (
	subLinkYesAction    = "sl_yes" // sl_yes <question>: send
	subLinkNoAction     = "sl_no"  // sl_no <question>: do not
	subLinkManualAction = "sl_man" // sl_man <broadcast id>: the links of those it did not reach

	subLinkScreenRoute = "s_slk"  // «📣 Send links» of «⚙️ Server»
	subLinkAllAsk      = "s_slka" // «Send the new link to N users?»
	subLinkAllData     = "s_slkc" // sending to all, confirmed
	subLinkUserAsk     = "slk_u"  // slk_u <subId>: «Send ivan the link?»
	subLinkUserData    = "slk_uc" // slk_uc <subId>: sending, confirmed
)

// subLinkQuiet is how long the changes must stay as they are before the
// admins are asked: the steps of one move (a switch, a reinstall) make one
// question.
const subLinkQuiet = 2 * time.Minute

// subLinkJournalLines is how many broadcasts the screen lists.
const subLinkJournalLines = 5

// subLinkWatcher is the debounce and the question out.
type subLinkWatcher struct {
	mu        sync.Mutex
	signature string    // the changes as last seen
	since     time.Time // since when they are so
	asked     string    // the changes the open question is about
	gen       int       // the question's number
	text      string    // its text
	prompts   map[int64]int
}

var subLinkWatch = &subLinkWatcher{}

func (w *subLinkWatcher) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.signature, w.since, w.asked, w.text, w.prompts = "", time.Time{}, "", "", nil
}

// CheckSubLinks is the periodic look (job.SubLinkWatchJob): it asks the
// admins once the links' changes have settled.
func (t *Tgbot) CheckSubLinks() {
	if !t.IsRunning() {
		return
	}
	changes, err := t.subLinkScan()
	if err != nil {
		logger.Warning("link broadcast: look:", err)
		return
	}
	w := subLinkWatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(changes.users) == 0 {
		w.signature, w.asked = "", ""
		return
	}
	if len(changes.recipients()) == 0 {
		// Only disabled users changed: nobody to send to, nothing to ask.
		if err := subLinkAcknowledge(changes.users); err != nil {
			logger.Warning("link broadcast:", err)
		}
		return
	}
	now := subLinkNow()
	signature := changes.signature()
	if signature != w.signature {
		w.signature, w.since = signature, now
		return
	}
	if now.Sub(w.since) < subLinkQuiet || w.asked == signature {
		return
	}
	t.subLinkAsk(w, changes)
}

// subLinkAsk sends the admins the question about changes, in place of any
// open one.
func (t *Tgbot) subLinkAsk(w *subLinkWatcher, changes subLinkChanges) {
	for chat, msgID := range w.prompts {
		t.deleteMessageTgBot(chat, msgID)
	}
	w.gen++
	w.asked = changes.signature()
	lines := []string{t.I18nBot("tgbot.sublink.question"),
		t.I18nBot("tgbot.sublink.reasons", "Reasons=="+t.subLinkReasonWords(strings.Join(changes.reasons(), ",")))}
	if n := len(changes.noTelegram); n > 0 {
		lines = append(lines, t.I18nBot("tgbot.sublink.reportNoTg", "Count=="+strconv.Itoa(n)))
	}
	lines = append(lines, "", t.I18nBot("tgbot.sublink.askAll", "Count=="+strconv.Itoa(len(changes.recipients()))))
	w.text = strings.Join(lines, "\r\n")
	gen := strconv.Itoa(w.gen)
	kb := tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.sublink.yes")).WithCallbackData(subLinkYesAction+" "+gen),
		tu.InlineKeyboardButton(t.I18nBot("tgbot.sublink.no")).WithCallbackData(subLinkNoAction+" "+gen)))
	w.prompts = map[int64]int{}
	for _, admin := range adminIds {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		msg, err := bot.SendMessage(ctx, tu.Message(tu.ID(admin), w.text).WithParseMode("HTML").WithReplyMarkup(kb))
		cancel()
		if err != nil {
			logger.Warning("link broadcast: ask", admin, err)
			continue
		}
		w.prompts[admin] = msg.MessageID
	}
}

// subLinkPress handles the question's and the report's buttons; false for
// any other.
func (t *Tgbot) subLinkPress(query *telego.CallbackQuery) bool {
	data, err := t.decodeQuery(query.Data)
	if err != nil {
		return false
	}
	action, arg, _ := strings.Cut(data, " ")
	switch action {
	case subLinkYesAction, subLinkNoAction:
		t.sendCallbackAnswerTgBot(query.ID, t.subLinkAnswer(query, action == subLinkYesAction, arg))
	case subLinkManualAction:
		id, _ := strconv.ParseInt(arg, 10, 64)
		t.sendCallbackAnswerTgBot(query.ID, "")
		t.subLinkManualLinks(query.Message.GetChat().ID, id)
	default:
		return false
	}
	return true
}

// subLinkAnswer answers question gen: the links go («Yes») or not, and either
// way the changes are settled. It returns the toast.
func (t *Tgbot) subLinkAnswer(query *telego.CallbackQuery, yes bool, gen string) string {
	w := subLinkWatch
	w.mu.Lock()
	if w.prompts == nil || gen != strconv.Itoa(w.gen) {
		w.mu.Unlock()
		return t.I18nBot("tgbot.sublink.outdated")
	}
	prompts, text := w.prompts, w.text
	w.prompts, w.text = nil, ""
	w.mu.Unlock()

	admin := subLinkAdminName(query.From)
	changes, err := t.subLinkScan()
	if err == nil {
		err = subLinkAcknowledge(changes.users)
	}
	if err != nil {
		return t.I18nBot("tgbot.answers.errorOperation") + " " + err.Error()
	}
	result := t.I18nBot("tgbot.sublink.answeredNo", "Admin=="+html.EscapeString(admin))
	toast := result
	// The links went back as they were meanwhile: nobody to send to.
	if yes && len(changes.recipients()) > 0 {
		var users []*SubUserView
		for _, c := range changes.recipients() {
			users = append(users, c.view)
		}
		chat := query.Message.GetChat().ID
		b, err := t.subLinkStart(subLinkJob{trigger: model.SubLinkTriggerAuto, reasons: changes.reasons(),
			startedBy: strconv.FormatInt(query.From.ID, 10), reportTo: []int64{chat}, users: users, noTelegram: changes.noTelegram})
		if err != nil {
			return t.I18nBot("tgbot.answers.errorOperation") + " " + err.Error()
		}
		result = t.I18nBot("tgbot.sublink.answeredYes", "Admin=="+html.EscapeString(admin),
			"Id=="+strconv.FormatInt(b.Id, 10), "Count=="+strconv.Itoa(len(users)))
		toast = t.I18nBot("tgbot.sublink.sending")
	}
	for chat, msgID := range prompts {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := bot.EditMessageText(ctx, &telego.EditMessageTextParams{ChatID: tu.ID(chat), MessageID: msgID,
			Text: text + "\r\n\r\n" + result, ParseMode: "HTML"})
		cancel()
		if err != nil {
			logger.Warning("link broadcast: the question in", chat, err)
		}
	}
	return toast
}

// subLinkAdminName is how the question names who answered it.
func subLinkAdminName(u telego.User) string {
	if u.Username != "" {
		return "@" + u.Username
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	return strconv.FormatInt(u.ID, 10)
}

// --- the screens ---------------------------------------------------------------

// subLinkCallback runs the broadcast's buttons on the admin's screen; false
// for data that is not theirs.
func (t *Tgbot) subLinkCallback(chatId int64, data string) (screenReply, bool) {
	action, arg, _ := strings.Cut(data, " ")
	switch action {
	case subLinkScreenRoute:
		return t.screenSubLinks(""), true
	case subLinkAllAsk:
		users, _, err := subLinkAudience()
		if err != nil {
			return screenReply{usersReply: t.usersError(err)}, true
		}
		return t.screenConfirm(t.I18nBot("tgbot.sublink.askAll", "Count=="+strconv.Itoa(len(users))), subLinkAllData), true
	case subLinkAllData:
		started, err := t.subLinkStartAll(model.SubLinkTriggerAll, strconv.FormatInt(chatId, 10), []int64{chatId})
		if err != nil {
			return t.screenSubLinks("⚠️ " + html.EscapeString(err.Error())), true
		}
		return t.screenSubLinks(t.I18nBot("tgbot.sublink.started", "Id=="+strconv.FormatInt(started.Id, 10),
			"Count=="+strconv.Itoa(started.Recipients))), true
	case subLinkUserAsk:
		v, err := subLinkRecipient(arg)
		if err != nil {
			return screenReply{usersReply: t.usersRefused(arg, err)}, true
		}
		return t.screenConfirm(t.I18nBot("tgbot.sublink.askUser", "Name=="+html.EscapeString(v.Name)),
			t.encodeQuery(subLinkUserData+" "+v.SubId)), true
	case subLinkUserData:
		v, err := subLinkRecipient(arg)
		if err != nil {
			return screenReply{usersReply: t.usersRefused(arg, err)}, true
		}
		b, err := t.subLinkStart(subLinkJob{trigger: model.SubLinkTriggerUser, startedBy: strconv.FormatInt(chatId, 10),
			reportTo: []int64{chatId}, users: []*SubUserView{v}})
		if err != nil {
			return screenReply{usersReply: t.usersRefused(arg, err)}, true
		}
		reply := t.usersCardReply(v.SubId)
		reply.text = t.I18nBot("tgbot.sublink.started", "Id=="+strconv.FormatInt(b.Id, 10), "Count==1") + "\r\n\r\n" + reply.text
		return screenReply{usersReply: reply}, true
	}
	return screenReply{}, false
}

// Why the link cannot go to a user.
var (
	errSubLinkNoTelegram = common.NewError("the user has no Telegram: pass the link on by hand")
	errSubLinkDisabled   = common.NewError("the user is disabled")
)

// subLinkRecipient is the user under key when the link can go to them: a
// regular, enabled user with Telegram.
func subLinkRecipient(key string) (*SubUserView, error) {
	v, err := (&SubUserService{}).Get(key)
	switch {
	case err != nil:
		return nil, err
	case v.Technical:
		return nil, common.NewErrorf("%s is a technical user and has no subscription link", v.Name)
	case v.TgId == 0:
		return nil, errSubLinkNoTelegram
	case !v.Enable:
		return nil, errSubLinkDisabled
	}
	return v, nil
}

// screenSubLinks is «📣 Send links»: who the link reaches, the journal's
// last broadcasts, «📣 Send to all (N)»; result goes on top.
func (t *Tgbot) screenSubLinks(result string) screenReply {
	users, noTelegram, err := subLinkAudience()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	lines := []string{}
	if result != "" {
		lines = append(lines, result, "")
	}
	lines = append(lines, t.I18nBot("tgbot.sublink.screen", "Count=="+strconv.Itoa(len(users)),
		"NoTelegram=="+strconv.Itoa(len(noTelegram))), "", t.I18nBot("tgbot.sublink.journal"))
	var journal []model.SubLinkBroadcast
	if err := database.GetDB().Order("id desc").Limit(subLinkJournalLines).Find(&journal).Error; err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	if len(journal) == 0 {
		lines = append(lines, t.I18nBot("tgbot.sublink.journalNone"))
	}
	for _, b := range journal {
		lines = append(lines, t.subLinkJournalLine(b))
	}
	var rows [][]telego.InlineKeyboardButton
	if len(users) > 0 {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(
			t.I18nBot("tgbot.sublink.sendAll", "Count=="+strconv.Itoa(len(users)))).WithCallbackData(subLinkAllAsk)))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(subLinkScreenRoute)))
	return screenReply{usersReply: usersReply{text: strings.Join(lines, "\r\n"), keyboard: tu.InlineKeyboard(rows...),
		route: subLinkScreenRoute}}
}

// subLinkJournalLine is one broadcast of the journal.
func (t *Tgbot) subLinkJournalLine(b model.SubLinkBroadcast) string {
	trigger := t.I18nBot("tgbot.sublink.trigger." + b.Trigger)
	if reasons := t.subLinkReasonWords(b.Reasons); reasons != "" {
		trigger += " (" + reasons + ")"
	}
	line := t.I18nBot("tgbot.sublink.journalLine", "Id=="+strconv.FormatInt(b.Id, 10),
		"Date=="+time.UnixMilli(b.CreatedAt).Format("02.01 15:04"), "Trigger=="+trigger,
		"Sent=="+strconv.Itoa(b.Sent), "Failed=="+strconv.Itoa(b.Failed+b.Blocked), "NoTelegram=="+strconv.Itoa(b.NoTelegram))
	if b.FinishedAt == 0 {
		line += " ⏳"
	}
	return line
}
