package service

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The create flow: a toggle keyboard of the inbounds, then the name and the
// limits every new client gets, then SubUserService.Create — all or nothing.

// usersDraft is a user being created: the inbounds offered and ticked, and
// the parameters every new client gets.
type usersDraft struct {
	inbounds     []SubUserInbound
	selected     map[int]bool
	name         string
	comment      string
	params       SubUserParams
	linkExisting bool
}

// request is the draft as the service takes it, the inbounds in the order
// they were offered.
func (d *usersDraft) request() SubUserCreate {
	req := SubUserCreate{Name: d.name, Comment: d.comment, SubUserParams: d.params, LinkExisting: d.linkExisting}
	for _, ib := range d.inbounds {
		if d.selected[ib.Id] {
			req.InboundIds = append(req.InboundIds, ib.Id)
		}
	}
	return req
}

// usersEditDraft changes the chat's draft; false when there is none.
func (t *Tgbot) usersEditDraft(chatId int64, fn func(*usersDraft)) bool {
	found := false
	usersSessions.with(chatId, func(s *usersSession) {
		if s.draft != nil {
			fn(s.draft)
			found = true
		}
	})
	return found
}

// usersCreateStart opens a new draft with the enabled inbounds ticked.
func (t *Tgbot) usersCreateStart(chatId int64) usersReply {
	inbounds, err := (&SubUserService{}).Inbounds()
	if err != nil {
		return t.usersError(err)
	}
	if len(inbounds) == 0 {
		return usersReply{text: t.I18nBot("tgbot.answers.getInboundsFailed")}
	}
	draft := &usersDraft{inbounds: inbounds, selected: map[int]bool{}}
	for _, ib := range inbounds {
		draft.selected[ib.Id] = ib.Enable
	}
	usersSessions.with(chatId, func(s *usersSession) { s.draft = draft })
	return usersReply{toast: t.I18nBot("tgbot.buttons.addClient"), text: t.I18nBot("tgbot.users.chooseProtocols"),
		keyboard: t.usersToggleKeyboard(draft)}
}

// usersCreateToggle ticks or unticks one inbound of the draft.
func (t *Tgbot) usersCreateToggle(chatId int64, inboundId int) usersReply {
	var kb *telego.InlineKeyboardMarkup
	if !t.usersEditDraft(chatId, func(d *usersDraft) {
		d.selected[inboundId] = !d.selected[inboundId]
		kb = t.usersToggleKeyboard(d)
	}) {
		return t.usersExpired()
	}
	return usersReply{keyboard: kb, edit: true}
}

// usersCreateProtocols goes back from the summary to the toggle keyboard.
func (t *Tgbot) usersCreateProtocols(chatId int64) usersReply {
	var kb *telego.InlineKeyboardMarkup
	if !t.usersEditDraft(chatId, func(d *usersDraft) { kb = t.usersToggleKeyboard(d) }) {
		return t.usersExpired()
	}
	return usersReply{text: t.I18nBot("tgbot.users.chooseProtocols"), keyboard: kb, edit: true}
}

// usersCreateNext leaves the toggle keyboard: for the name, or back to the
// summary when the draft has one.
func (t *Tgbot) usersCreateNext(chatId int64) usersReply {
	var ticked int
	var named bool
	if !t.usersEditDraft(chatId, func(d *usersDraft) {
		ticked = len(d.request().InboundIds)
		named = d.name != ""
	}) {
		return t.usersExpired()
	}
	if ticked == 0 {
		return usersReply{toast: t.I18nBot("tgbot.users.noneSelected")}
	}
	if named {
		return t.usersDraftReply(chatId, true)
	}
	return t.usersAsk(chatId, usersStateName, t.I18nBot("tgbot.users.namePrompt"))
}

// usersToggleKeyboard is the draft's inbounds, ✅ ticked and ⬜ not, then
// Next and Cancel.
func (t *Tgbot) usersToggleKeyboard(d *usersDraft) *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	for _, ib := range d.inbounds {
		mark := "⬜"
		if d.selected[ib.Id] {
			mark = "✅"
		}
		label := fmt.Sprintf("%s %s (%s)", mark, ib.Remark, ib.Protocol)
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).
			WithCallbackData(fmt.Sprintf("usr_t %d", ib.Id))))
	}
	rows = append(rows,
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.users.next")).WithCallbackData("usr_next")),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("usr_x")),
	)
	return tu.InlineKeyboard(rows...)
}

// usersDraftReply shows the draft's summary with its buttons.
func (t *Tgbot) usersDraftReply(chatId int64, edit bool) usersReply {
	var text string
	if !t.usersEditDraft(chatId, func(d *usersDraft) { text = t.usersDraftText(d) }) {
		return t.usersExpired()
	}
	button := func(key, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(t.I18nBot(key)).WithCallbackData(data)
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(button("tgbot.users.changeName", "usr_nm"), button("tgbot.users.changeProtocols", "usr_pr")),
		tu.InlineKeyboardRow(button("tgbot.buttons.limitTraffic", "usr_tr"), button("tgbot.buttons.resetExpire", "usr_ex")),
		tu.InlineKeyboardRow(button("tgbot.buttons.change_comment", "usr_cm"), button("tgbot.buttons.ipLimit", "usr_ip")),
		tu.InlineKeyboardRow(button("tgbot.users.create", "usr_ok")),
		tu.InlineKeyboardRow(button("tgbot.buttons.cancel", "usr_x")),
	)
	return usersReply{text: text, keyboard: kb, edit: edit}
}

func (t *Tgbot) usersDraftText(d *usersDraft) string {
	var protocols []string
	for _, ib := range d.inbounds {
		if d.selected[ib.Id] {
			protocols = append(protocols, fmt.Sprintf("%s (%s)", ib.Remark, ib.Protocol))
		}
	}
	ipLimit := t.I18nBot("tgbot.unlimited")
	if d.params.LimitIp > 0 {
		ipLimit = strconv.Itoa(d.params.LimitIp)
	}
	return t.I18nBot("tgbot.users.draft",
		"Name=="+html.EscapeString(d.name),
		"Protocols=="+html.EscapeString(strings.Join(protocols, ", ")),
		"Traffic=="+t.usersTraffic(d.params.TotalGB),
		"Exp=="+t.usersExpiry(d.params.ExpiryTime),
		"IpLimit=="+ipLimit,
		"Comment=="+html.EscapeString(d.comment))
}

// --- limits --------------------------------------------------------------------

// The limits the draft asks for: a preset keyboard ("usr_tr"), a setter
// ("usr_trs <n>") and a keypad for any number ("usr_tri <n> [digit]").
var usersLimitCallbacks = map[string]struct{ set, pad string }{
	"usr_tr": {"usr_trs", "usr_tri"},
	"usr_ex": {"usr_exs", "usr_exi"},
	"usr_ip": {"usr_ips", "usr_ipi"},
}

// usersLimitKeyboard is the preset keyboard of a limit, as the upstream
// add-client dialogue has it: traffic in GB, expiry in days after first use,
// the IP limit.
func (t *Tgbot) usersLimitKeyboard(limit string) *telego.InlineKeyboardMarkup {
	cb := usersLimitCallbacks[limit]
	preset := func(label string, n int) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(fmt.Sprintf("%s %d", cb.set, n))
	}
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("usr_sum")),
		tu.InlineKeyboardRow(preset(t.I18nBot("tgbot.unlimited"), 0),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(cb.pad+" 0")),
	}
	switch limit {
	case "usr_tr":
		for _, row := range [][]int{{1, 5, 10}, {20, 30, 40}, {50, 60, 80}, {100, 150, 200}} {
			var buttons []telego.InlineKeyboardButton
			for _, gb := range row {
				buttons = append(buttons, preset(fmt.Sprintf("%d GB", gb), gb))
			}
			rows = append(rows, buttons)
		}
	case "usr_ex":
		days := func(n int) telego.InlineKeyboardButton {
			return preset(t.I18nBot("tgbot.add")+" "+strconv.Itoa(n)+" "+t.I18nBot("tgbot.days"), n)
		}
		months := func(n, d int) telego.InlineKeyboardButton {
			unit := t.I18nBot("tgbot.months")
			if n == 1 {
				unit = t.I18nBot("tgbot.month")
			}
			return preset(t.I18nBot("tgbot.add")+" "+strconv.Itoa(n)+" "+unit, d)
		}
		rows = append(rows,
			tu.InlineKeyboardRow(days(7), days(10)),
			tu.InlineKeyboardRow(days(14), days(20)),
			tu.InlineKeyboardRow(months(1, 30), months(3, 90)),
			tu.InlineKeyboardRow(months(6, 180), months(12, 365)))
	case "usr_ip":
		for _, row := range [][]int{{1, 2}, {3, 4}, {5, 6, 7}, {8, 9, 10}} {
			var buttons []telego.InlineKeyboardButton
			for _, n := range row {
				buttons = append(buttons, preset(strconv.Itoa(n), n))
			}
			rows = append(rows, buttons)
		}
	}
	return tu.InlineKeyboard(rows...)
}

// usersSetLimit stores a limit in the draft and shows the summary again.
func (t *Tgbot) usersSetLimit(chatId int64, set string, n int) usersReply {
	if n < 0 {
		n = 0
	}
	if !t.usersEditDraft(chatId, func(d *usersDraft) {
		switch set {
		case "usr_trs":
			d.params.TotalGB = int64(n) << 30
		case "usr_exs":
			d.params.ExpiryTime = -int64(n) * 86400000
		case "usr_ips":
			d.params.LimitIp = n
		}
	}) {
		return t.usersExpired()
	}
	reply := t.usersDraftReply(chatId, true)
	reply.toast = t.I18nBot("tgbot.answers.successfulOperation")
	return reply
}

// usersKeypadPress is a key of the keypad: args is the number so far and the
// key — a digit, -1 to delete one, -2 to clear.
func (t *Tgbot) usersKeypadPress(pad, args string) usersReply {
	var set string
	for _, cb := range usersLimitCallbacks {
		if cb.pad == pad {
			set = cb.set
		}
	}
	fields := strings.Fields(args)
	n := 0
	if len(fields) > 0 {
		n, _ = strconv.Atoi(fields[0])
	}
	if len(fields) > 1 {
		key, _ := strconv.Atoi(fields[1])
		next := n
		switch key {
		case -2:
			next = 0
		case -1:
			next = n / 10
		default:
			next = n*10 + key
		}
		if next == n {
			return usersReply{}
		}
		if next >= 999999 {
			return usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}
		}
		n = next
	}
	key := func(label string, k int) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(fmt.Sprintf("%s %d %d", pad, n, k))
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("usr_sum")),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(n))).
			WithCallbackData(fmt.Sprintf("%s %d", set, n))),
		tu.InlineKeyboardRow(key("1", 1), key("2", 2), key("3", 3)),
		tu.InlineKeyboardRow(key("4", 4), key("5", 5), key("6", 6)),
		tu.InlineKeyboardRow(key("7", 7), key("8", 8), key("9", 9)),
		tu.InlineKeyboardRow(key("🔄", -2), key("0", 0), key("⬅️", -1)),
	)
	return usersReply{keyboard: kb, edit: true}
}

// --- submit --------------------------------------------------------------------

// usersCreateSubmit creates the user. An unlinked AmneziaWG client with the
// name the new client would get is offered for linking first.
func (t *Tgbot) usersCreateSubmit(chatId int64, linkExisting bool) usersReply {
	var req *SubUserCreate
	t.usersEditDraft(chatId, func(d *usersDraft) {
		if linkExisting {
			d.linkExisting = true
		}
		r := d.request()
		req = &r
	})
	if req == nil {
		return t.usersExpired()
	}
	v, err := (&SubUserService{}).Create(*req)
	if conflict, ok := awgLinkable(err); ok {
		return usersReply{text: t.I18nBot("tgbot.users.linkAwg", "Client=="+html.EscapeString(conflict.Client)),
			keyboard: tu.InlineKeyboard(tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.users.link")).WithCallbackData("usr_lnk"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("usr_sum"),
			))}
	}
	if err != nil {
		return t.usersError(err)
	}
	usersSessions.with(chatId, func(s *usersSession) { s.draft = nil })
	text, kb := t.usersCard(v)
	return usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation"),
		text: t.I18nBot("tgbot.users.created", "Name=="+html.EscapeString(v.Name)) + "\r\n\r\n" + text, keyboard: kb, edit: true}
}
