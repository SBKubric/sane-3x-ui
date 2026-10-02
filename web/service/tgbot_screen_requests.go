package service

import (
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The admin's side of requests (#188 points 4–7, #221, docs/spec/users.md
// §12), on the admin's screen: the notification channel only says a request
// came (requestNotify), the admins decide here.
//   - «📥 Incoming requests (N)» in the main menu lists the pending requests,
//     oldest first, and «🚫 Blocked (N)» the blocked accounts;
//   - a request's card: the account's @nick, its name, the tg_id, the
//     comment, the date and the account's past rejections, with
//     «✅ Approve» (the request defaults of the settings),
//     «⚙️ Approve with changes» (the «New user» review, filled in, the
//     Telegram bound), «❌ Reject» (a preset reason or one typed) and
//     «🚫 Block» / «✅ Unblock»; a decided request's card says how;
//   - an account's card, from the blocked list: «✅ Unblock» / «🚫 Block»;
//   - both cards say when the account passed the captcha, with «🧩 Reset
//     the captcha»: its next request asks for the captcha again. A block
//     resets it too.
//
// The applicant is told on a new screen of their own chat: «Your
// subscription is ready» and the link to the subscription page over «My
// subscription» (no files, #245), or the rejection with its reason and the
// date a new request becomes possible. Every route here is
// an admin's: none is in the access list of tgbot_access.go.

// Callback data of the admin's request screens, and the chat state of a
// typed reason.
const (
	requestsListRoute    = "rq_l"    // rq_l <page>: the pending requests
	requestCardRoute     = "rq_c"    // rq_c <id>: a request's card
	requestApproveAction = "rq_ok"   // rq_ok <id>: approve with the request defaults
	requestEditAction    = "rq_ed"   // rq_ed <id>: the «New user» review of the request
	requestRejectRoute   = "rq_rj"   // rq_rj <id>: the reasons
	requestRejectAction  = "rq_rjp"  // rq_rjp <id> <n>: reject with the preset reason n
	requestReasonAction  = "rq_rjt"  // rq_rjt <id>: ask for a reason
	requestBlockAction   = "rq_b"    // rq_b <1|0> <tgId> [<id>]: block or unblock, then the request's card or the account's
	requestBlockedRoute  = "rq_bl"   // rq_bl <page>: the blocked accounts
	requestAccountRoute  = "rq_a"    // rq_a <tgId>: an account's card
	requestCaptchaAction = "rq_cr"   // rq_cr <tgId> [<id>]: reset the account's captcha, then the request's card or the account's
	requestReasonState   = "usr_rqr" // the chat waits for a reason (a users state: admins only)
)

// requestReasons are the keys of the preset reasons of a rejection.
var requestReasons = []string{"tgbot.requests.reasonFull", "tgbot.requests.reasonUnknown"}

// requestReasonMax bounds a typed reason, as the comment is bounded.
const requestReasonMax = subRequestCommentMax

// requestShortDate is how a list line dates a request.
const requestShortDate = "02.01 15:04"

// requestAdminCallback runs the buttons of these screens; false for data
// that is not theirs.
func (t *Tgbot) requestAdminCallback(chatId int64, data string) (screenReply, bool) {
	action, args, _ := strings.Cut(data, " ")
	fields := strings.Fields(args)
	num := func(i int) int64 {
		if i >= len(fields) {
			return 0
		}
		n, _ := strconv.ParseInt(fields[i], 10, 64)
		return n
	}
	// small is for list pages and reason indexes.
	small := func(i int) int {
		if i >= len(fields) {
			return 0
		}
		n, _ := strconv.Atoi(fields[i])
		return n
	}
	switch action {
	case requestsListRoute:
		return t.requestsList(small(0), ""), true
	case requestCardRoute:
		return t.requestCard(num(0), ""), true
	case requestApproveAction:
		return t.requestApprove(chatId, num(0)), true
	case requestEditAction:
		return screenReply{usersReply: t.newUserFromRequest(chatId, num(0))}, true
	case requestRejectRoute:
		return t.requestRejectMenu(num(0)), true
	case requestRejectAction:
		n := small(1)
		if n < 0 || n >= len(requestReasons) {
			return t.requestCard(num(0), ""), true
		}
		return t.requestReject(chatId, num(0), t.I18nBot(requestReasons[n])), true
	case requestReasonAction:
		return t.requestAskReason(chatId, num(0)), true
	case requestBlockAction:
		return t.requestBlock(chatId, num(0) == 1, num(1), num(2)), true
	case requestBlockedRoute:
		return t.requestsBlocked(small(0)), true
	case requestAccountRoute:
		return t.requestAccount(num(0)), true
	case requestCaptchaAction:
		return t.requestCaptchaReset(num(0), num(1)), true
	}
	return screenReply{}, false
}

// requestsMenuButton is the main menu's «📥 Incoming requests (N)».
func (t *Tgbot) requestsMenuButton() telego.InlineKeyboardButton {
	n, err := (&SubRequestService{}).CountPending()
	if err != nil {
		logger.Warning("requests:", err)
	}
	return tu.InlineKeyboardButton(t.I18nBot("tgbot.requests.menu", "Count=="+strconv.FormatInt(n, 10))).
		WithCallbackData(requestsListRoute + " 0")
}

// requestAccountName names the account tgId for a button: its @nick, else
// its name, else the id.
func requestAccountName(tgId int64, a *model.TgAccount) string {
	switch {
	case a == nil:
	case a.Username != "":
		return "@" + a.Username
	case strings.TrimSpace(a.FirstName+" "+a.LastName) != "":
		return strings.TrimSpace(a.FirstName + " " + a.LastName)
	}
	return strconv.FormatInt(tgId, 10)
}

// requestAccountOf is the account tgId as the bot last saw it; nil when it
// never did.
func requestAccountOf(tgId int64) *model.TgAccount {
	a, err := (&TgAccountService{}).Get(tgId)
	if err != nil {
		logger.Warning("requests:", err)
	}
	return a
}

// requestAdminLabel is how a decision records the admin of chatId.
func requestAdminLabel(chatId int64) string {
	return requestAccountName(chatId, requestAccountOf(chatId))
}

// requestsList is a page of the pending requests, each opening its card,
// with warning above it ("" for none).
func (t *Tgbot) requestsList(page int, warning string) screenReply {
	requests := &SubRequestService{}
	pending, err := requests.Pending()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	page, pages, from, to := screenPageOf(page, len(pending), screenPage)
	text := t.I18nBot("tgbot.requests.title", "Count=="+strconv.Itoa(len(pending)))
	if len(pending) == 0 {
		text += "\r\n" + t.I18nBot("tgbot.requests.none")
	}
	if warning != "" {
		text = warning + "\r\n\r\n" + text
	}
	var rows [][]telego.InlineKeyboardButton
	for _, r := range pending[from:to] {
		label := requestAccountName(r.TgId, requestAccountOf(r.TgId)) + " · " + time.UnixMilli(r.CreatedAt).Local().Format(requestShortDate)
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).
			WithCallbackData(fmt.Sprintf("%s %d", requestCardRoute, r.Id))))
	}
	if pager := screenPager(requestsListRoute, page, pages); pager != nil {
		rows = append(rows, pager)
	}
	if blocked, err := requests.Blocked(); err == nil && len(blocked) > 0 {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(
			t.I18nBot("tgbot.requests.blockedList", "Count=="+strconv.Itoa(len(blocked)))).WithCallbackData(requestBlockedRoute+" 0")))
	}
	reply := screenReply{usersReply: usersReply{text: text, route: fmt.Sprintf("%s %d", requestsListRoute, page)}}
	if len(rows) > 0 {
		reply.keyboard = tu.InlineKeyboard(rows...)
	}
	return reply
}

// requestAccountLines are the account's lines of a card: who, the name,
// the tg_id.
func (t *Tgbot) requestAccountLines(tgId int64) string {
	a := requestAccountOf(tgId)
	name := t.I18nBot("tgbot.newUser.none")
	if a != nil {
		if full := strings.TrimSpace(a.FirstName + " " + a.LastName); full != "" {
			name = html.EscapeString(full)
		}
	}
	return t.I18nBot("tgbot.requests.account", "Account=="+html.EscapeString(requestAccountName(tgId, a)),
		"Name=="+name, "TgId=="+strconv.FormatInt(tgId, 10))
}

// requestCard is the card of request id, with warning above it ("" for
// none): pending, the decisions; decided, how; and the account's block.
func (t *Tgbot) requestCard(id int64, warning string) screenReply {
	requests := &SubRequestService{}
	r, err := requests.Get(id)
	if err != nil {
		return t.requestsList(0, t.requestError(err))
	}
	var b strings.Builder
	if warning != "" {
		b.WriteString(warning + "\r\n\r\n")
	}
	comment := t.I18nBot("tgbot.newUser.none")
	if r.Comment != "" {
		comment = html.EscapeString(r.Comment)
	}
	b.WriteString(t.I18nBot("tgbot.requests.card", "Id=="+strconv.FormatInt(r.Id, 10)))
	b.WriteString(t.requestAccountLines(r.TgId))
	b.WriteString(t.I18nBot("tgbot.requests.cardRequest", "Comment=="+comment, "Date=="+requestDate(r.CreatedAt)))
	var past []model.SubRequest
	rejections, err := requests.Rejections(r.TgId)
	if err != nil {
		logger.Warning("requests:", err)
	}
	for _, p := range rejections {
		if p.Id != r.Id {
			past = append(past, p)
		}
	}
	if len(past) > 0 {
		b.WriteString(t.I18nBot("tgbot.requests.rejections", "Count=="+strconv.Itoa(len(past))))
		for _, p := range past[:min(len(past), 5)] {
			b.WriteString(t.I18nBot("tgbot.requests.rejection", "Date=="+requestDate(p.CreatedAt),
				"Reason=="+html.EscapeString(t.requestReasonText(p.Reason))))
		}
	}
	by := html.EscapeString(r.DecidedBy)
	switch r.Status {
	case model.SubRequestApproved:
		b.WriteString(t.I18nBot("tgbot.requests.statusApproved", "Date=="+requestDate(r.DecidedAt), "By=="+by))
	case model.SubRequestRejected:
		b.WriteString(t.I18nBot("tgbot.requests.statusRejected", "Date=="+requestDate(r.DecidedAt), "By=="+by,
			"Reason=="+html.EscapeString(t.requestReasonText(r.Reason))))
	case model.SubRequestCancelled:
		b.WriteString(t.I18nBot("tgbot.requests.statusCancelled", "Date=="+requestDate(r.DecidedAt)))
	case model.SubRequestExpired:
		b.WriteString(t.I18nBot("tgbot.requests.statusExpired", "Date=="+requestDate(r.DecidedAt)))
	}
	blocked := false
	if st, err := requests.Status(r.TgId); err == nil {
		blocked = st.Blocked
	}
	if blocked {
		b.WriteString(t.I18nBot("tgbot.requests.blockedLine"))
	}
	passed := t.requestCaptchaLine(&b, r.TgId)
	button := func(key, data string) []telego.InlineKeyboardButton {
		return tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot(key)).WithCallbackData(data))
	}
	var rows [][]telego.InlineKeyboardButton
	if r.Status == model.SubRequestPending {
		rows = append(rows,
			button("tgbot.requests.approve", fmt.Sprintf("%s %d", requestApproveAction, r.Id)),
			button("tgbot.requests.approveEdit", fmt.Sprintf("%s %d", requestEditAction, r.Id)),
			button("tgbot.requests.reject", fmt.Sprintf("%s %d", requestRejectRoute, r.Id)))
	}
	if passed {
		rows = append(rows, t.requestCaptchaRow(r.TgId, r.Id))
	}
	rows = append(rows, t.requestBlockRow(r.TgId, blocked, r.Id))
	return screenReply{usersReply: usersReply{text: b.String(), keyboard: tu.InlineKeyboard(rows...),
		route: fmt.Sprintf("%s %d", requestCardRoute, r.Id)}}
}

// requestBlockRow is «🚫 Block» or «✅ Unblock» for the account tgId, back
// to the card of request id, or to the account's card for 0.
func (t *Tgbot) requestBlockRow(tgId int64, blocked bool, id int64) []telego.InlineKeyboardButton {
	key, on := "tgbot.requests.block", 1
	if blocked {
		key, on = "tgbot.requests.unblock", 0
	}
	data := fmt.Sprintf("%s %d %d", requestBlockAction, on, tgId)
	if id != 0 {
		data += fmt.Sprintf(" %d", id)
	}
	return tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot(key)).WithCallbackData(data))
}

// requestReasonText is a rejection's reason as the admins read it.
func (t *Tgbot) requestReasonText(reason string) string {
	if reason == "" {
		return t.I18nBot("tgbot.request.noReason")
	}
	return reason
}

// requestError words a refusal of a decision.
func (t *Tgbot) requestError(err error) string {
	var refusal *SubRequestRefusal
	if errors.As(err, &refusal) && refusal.Code == SubRequestNotPending {
		return t.I18nBot("tgbot.requests.notPending")
	}
	return t.I18nBot("tgbot.newUser.error", "Error=="+html.EscapeString(err.Error()))
}

// requestApprove is «✅ Approve»: the user with the request defaults, its
// card, and the applicant told. A refusal shows on the request's card.
func (t *Tgbot) requestApprove(chatId, id int64) screenReply {
	r, err := (&SubRequestService{}).Get(id)
	if err != nil {
		return t.requestsList(0, t.requestError(err))
	}
	v, err := (&SubRequestService{}).ApproveWithDefaults(id, requestAdminLabel(chatId))
	if err != nil {
		reply := t.requestCard(id, t.requestError(err))
		reply.toast = t.I18nBot("tgbot.answers.errorOperation")
		return reply
	}
	return t.requestApproved(v, r.TgId)
}

// requestApproved shows the user v a request made, and tells the applicant
// tgId once the screen is updated: ready, and the subscription page's link.
func (t *Tgbot) requestApproved(v *SubUserView, tgId int64) screenReply {
	reply := screenReply{usersReply: t.usersCardOf(v)}
	reply.toast = t.I18nBot("tgbot.requests.approvedToast")
	reply.text = t.I18nBot("tgbot.requests.approved", "Name=="+html.EscapeString(v.Name)) + "\r\n\r\n" + reply.text
	head := t.I18nBot("tgbot.requests.ready") + "\r\n" + t.mysubLink(v)
	reply.after = func(int64) { t.requestTell(tgId, head) }
	return reply
}

// requestTell shows the applicant tgId their main menu on a new screen —
// «My subscription» once approved, the rejection after one — with head
// above it ("" for none). An admin (who cannot have asked through the bot
// since) gets it as a plain message, leaving their screen alone.
func (t *Tgbot) requestTell(tgId int64, head string) {
	reply := t.mysubHome(tgId)
	if head != "" {
		reply.text = head + "\r\n\r\n" + reply.text
	}
	if checkAdmin(tgId) {
		t.SendMsgToTgbot(tgId, reply.text)
		return
	}
	sc := botScreens.of(tgId)
	sc.mu.Lock()
	sc.client = tgId
	sc.mu.Unlock()
	t.clientOpen(tgId, reply)
}

// requestRejectMenu offers the reasons of a rejection: the presets and one
// to type.
func (t *Tgbot) requestRejectMenu(id int64) screenReply {
	r, err := (&SubRequestService{}).Get(id)
	if err != nil {
		return t.requestsList(0, t.requestError(err))
	}
	if r.Status != model.SubRequestPending {
		return t.requestCard(id, t.I18nBot("tgbot.requests.notPending"))
	}
	var rows [][]telego.InlineKeyboardButton
	for n, key := range requestReasons {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot(key)).
			WithCallbackData(fmt.Sprintf("%s %d %d", requestRejectAction, id, n))))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.requests.reasonOwn")).
		WithCallbackData(fmt.Sprintf("%s %d", requestReasonAction, id))))
	text := t.I18nBot("tgbot.requests.rejectTitle",
		"Account=="+html.EscapeString(requestAccountName(r.TgId, requestAccountOf(r.TgId))))
	return screenReply{usersReply: usersReply{text: text, keyboard: tu.InlineKeyboard(rows...),
		route: fmt.Sprintf("%s %d", requestRejectRoute, id)}}
}

// requestReject rejects request id with reason, shows the pending requests
// left, and tells the applicant.
func (t *Tgbot) requestReject(chatId, id int64, reason string) screenReply {
	requests := &SubRequestService{}
	r, err := requests.Get(id)
	if err == nil {
		err = requests.Reject(id, requestAdminLabel(chatId), reason)
	}
	if err != nil {
		reply := t.requestCard(id, t.requestError(err))
		reply.toast = t.I18nBot("tgbot.answers.errorOperation")
		return reply
	}
	reply := t.requestsList(0, "")
	reply.toast = t.I18nBot("tgbot.requests.rejected")
	reply.after = func(int64) { t.requestTell(r.TgId, "") }
	return reply
}

// requestAskReason makes the chat wait for the reason of rejecting id.
func (t *Tgbot) requestAskReason(chatId, id int64) screenReply {
	usersSessions.with(chatId, func(s *usersSession) { s.rejectRequest = id })
	return screenReply{usersReply: t.usersAsk(chatId, requestReasonState, t.I18nBot("tgbot.requests.reasonPrompt"))}
}

// requestReasonTyped takes the typed reason: up to 200 characters, else the
// chat is asked again.
func (t *Tgbot) requestReasonTyped(chatId int64, text string) screenReply {
	var id int64
	usersSessions.with(chatId, func(s *usersSession) { id = s.rejectRequest })
	if id == 0 {
		return screenReply{usersReply: t.usersExpired()}
	}
	reason := strings.TrimSpace(text)
	if n := len([]rune(reason)); n == 0 || n > requestReasonMax {
		return screenReply{usersReply: t.usersAsk(chatId, requestReasonState, t.I18nBot("tgbot.requests.reasonTooLong",
			"Count=="+strconv.Itoa(n))+"\r\n\r\n"+t.I18nBot("tgbot.requests.reasonPrompt"))}
	}
	usersSessions.with(chatId, func(s *usersSession) { s.rejectRequest = 0 })
	return t.requestReject(chatId, id, reason)
}

// requestBlock is «🚫 Block» (the account's pending request rejected with
// it) or «✅ Unblock», back on the card of request id, or on the account's
// card for 0.
func (t *Tgbot) requestBlock(chatId int64, block bool, tgId, id int64) screenReply {
	if tgId <= 0 {
		return t.requestsList(0, "")
	}
	requests := &SubRequestService{}
	var err error
	toast := t.I18nBot("tgbot.requests.unblocked")
	if block {
		err, toast = requests.Block(tgId, requestAdminLabel(chatId)), t.I18nBot("tgbot.requests.blocked")
	} else {
		err = requests.SetBlocked(tgId, false)
	}
	var reply screenReply
	switch {
	case err != nil:
		return screenReply{usersReply: t.usersError(err)}
	case id != 0:
		reply = t.requestCard(id, "")
	default:
		reply = t.requestAccount(tgId)
	}
	reply.toast = toast
	return reply
}

// requestsBlocked is a page of the blocked accounts, each opening its card.
func (t *Tgbot) requestsBlocked(page int) screenReply {
	blocked, err := (&SubRequestService{}).Blocked()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	page, pages, from, to := screenPageOf(page, len(blocked), screenPage)
	var rows [][]telego.InlineKeyboardButton
	for i := range blocked[from:to] {
		a := &blocked[from+i]
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(requestAccountName(a.TgId, a)).
			WithCallbackData(fmt.Sprintf("%s %d", requestAccountRoute, a.TgId))))
	}
	if pager := screenPager(requestBlockedRoute, page, pages); pager != nil {
		rows = append(rows, pager)
	}
	reply := screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.requests.blockedTitle", "Count=="+strconv.Itoa(len(blocked))),
		route: fmt.Sprintf("%s %d", requestBlockedRoute, page)}}
	if len(rows) > 0 {
		reply.keyboard = tu.InlineKeyboard(rows...)
	}
	return reply
}

// requestAccount is the card of the account tgId: who, whether it is
// blocked, with «✅ Unblock» or «🚫 Block».
func (t *Tgbot) requestAccount(tgId int64) screenReply {
	st, err := (&SubRequestService{}).Status(tgId)
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.requests.accountTitle") + t.requestAccountLines(tgId))
	if st.Blocked {
		b.WriteString(t.I18nBot("tgbot.requests.blockedLine"))
	}
	var rows [][]telego.InlineKeyboardButton
	if t.requestCaptchaLine(&b, tgId) {
		rows = append(rows, t.requestCaptchaRow(tgId, 0))
	}
	rows = append(rows, t.requestBlockRow(tgId, st.Blocked, 0))
	return screenReply{usersReply: usersReply{text: b.String(), keyboard: tu.InlineKeyboard(rows...),
		route: fmt.Sprintf("%s %d", requestAccountRoute, tgId)}}
}

// requestCaptchaLine writes when the account tgId passed the captcha, and
// reports whether it has; nothing for an account that has not.
func (t *Tgbot) requestCaptchaLine(b *strings.Builder, tgId int64) bool {
	st, err := (&TgCaptchaService{}).State(tgId)
	if err != nil {
		logger.Warning("captcha:", err)
		return false
	}
	if st.PassedAt == 0 {
		return false
	}
	b.WriteString(t.I18nBot("tgbot.requests.captchaPassed", "Date=="+requestDate(st.PassedAt)))
	return true
}

// requestCaptchaRow is «🧩 Reset the captcha» for the account tgId, back to
// the card of request id, or to the account's card for 0.
func (t *Tgbot) requestCaptchaRow(tgId, id int64) []telego.InlineKeyboardButton {
	data := fmt.Sprintf("%s %d", requestCaptchaAction, tgId)
	if id != 0 {
		data += fmt.Sprintf(" %d", id)
	}
	return tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.requests.captchaReset")).WithCallbackData(data))
}

// requestCaptchaReset is «🧩 Reset the captcha»: the account tgId passes it
// again on its next request; back on the card of request id, or on the
// account's card for 0.
func (t *Tgbot) requestCaptchaReset(tgId, id int64) screenReply {
	if tgId <= 0 {
		return t.requestsList(0, "")
	}
	if err := (&TgCaptchaService{}).Reset(tgId); err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	var reply screenReply
	if id != 0 {
		reply = t.requestCard(id, "")
	} else {
		reply = t.requestAccount(tgId)
	}
	reply.toast = t.I18nBot("tgbot.requests.captchaResetDone")
	return reply
}

// newUserFromRequest is «⚙️ Approve with changes»: the «New user» review of
// the request's Draft — its name, the request defaults, the applicant's
// Telegram — for the admin to change and create.
func (t *Tgbot) newUserFromRequest(chatId, id int64) usersReply {
	req, err := (&SubRequestService{}).Draft(id)
	if err != nil {
		return t.requestCard(id, t.requestError(err)).usersReply
	}
	inbounds, err := (&SubUserService{}).Inbounds()
	if err != nil {
		return t.usersError(err)
	}
	d := &usersDraft{step: newUserStepReview, inbounds: inbounds, selected: map[int]bool{}, name: req.Name,
		gb: int(req.TotalGB >> 30), days: int(-req.ExpiryTime / 86400000), tgId: req.TgId, request: id}
	for _, ib := range req.InboundIds {
		d.selected[ib] = true
	}
	usersSessions.with(chatId, func(s *usersSession) { s.draft = d })
	return t.newUserView(chatId, d, "")
}
