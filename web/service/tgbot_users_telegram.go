package service

import (
	"html"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The user's Telegram in the bot (#186 point 8, docs/spec/users.md §11),
// admins only: a card in conflict says «⚠️ Telegram: конфликт», and its
// Telegram screen lists the ids on the user's clients with who else has each,
// «Назначить <tg_id>» for the ids nobody else owns, and «Отвязать Telegram»
// after a confirmation. SubUserService decides; the bot shows its refusal.
//
// Callback data: usr_tg <key> (the screen), usr_tga <key> <tgId> (assign),
// usr_tgu <key> (the question), usr_tguc <key> (unlink).

// usersTelegramCallback runs a button of the Telegram screen; ok is false for
// data that is not one.
func (t *Tgbot) usersTelegramCallback(data string) (reply usersReply, ok bool) {
	action, args, _ := strings.Cut(data, " ")
	key, rest, _ := strings.Cut(args, " ")
	switch action {
	case "usr_tg":
		return t.usersTelegramScreen(args), true
	case "usr_tga":
		tgId, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || tgId <= 0 {
			return t.usersCardReply(key), true
		}
		return t.usersTelegramDone(key)((&SubUserService{}).SetTelegram(key, tgId)), true
	case "usr_tgu":
		return t.usersUnlinkConfirm(args), true
	case "usr_tguc":
		return t.usersTelegramDone(args)((&SubUserService{}).UnlinkTelegram(args)), true
	}
	return usersReply{}, false
}

// usersTelegramDone shows the card after a change, or the refusal on top of it.
func (t *Tgbot) usersTelegramDone(key string) func(*SubUserView, error) usersReply {
	return func(v *SubUserView, err error) usersReply {
		if err != nil {
			return t.usersRefused(key, err)
		}
		return t.usersDone(v)
	}
}

// usersTelegramRow is the card's button to the Telegram screen: for a user
// with an id or in conflict.
func (t *Tgbot) usersTelegramRow(v *SubUserView) []telego.InlineKeyboardButton {
	if v.Technical || (v.TgId == 0 && !v.TgConflict) {
		return nil
	}
	label := t.I18nBot("tgbot.tgaccount.button")
	if v.TgConflict {
		label = t.I18nBot("tgbot.tgaccount.conflictButton")
	}
	return tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).WithCallbackData(t.encodeQuery("usr_tg " + v.SubId)))
}

// usersTelegramLine is the card's conflict line, "" without a conflict.
func (t *Tgbot) usersTelegramLine(v *SubUserView) string {
	if !v.TgConflict {
		return ""
	}
	return t.I18nBot("tgbot.tgaccount.conflict")
}

// tgAccountLabel is how an account is named after its id: " (@nick)", else
// " (First Last)", else nothing.
func tgAccountLabel(a *model.TgAccount) string {
	switch {
	case a == nil:
		return ""
	case a.Username != "":
		return " (@" + html.EscapeString(a.Username) + ")"
	}
	if name := strings.TrimSpace(a.FirstName + " " + a.LastName); name != "" {
		return " (" + html.EscapeString(name) + ")"
	}
	return ""
}

// usersTelegramScreen is the user's Telegram: its id and account, the ids on
// its clients with who else has each, and the buttons to resolve.
func (t *Tgbot) usersTelegramScreen(key string) usersReply {
	tg, err := (&SubUserService{}).Telegram(key)
	if err != nil {
		return t.usersError(err)
	}
	if tg.SubId == model.SubUserRobotKey || tg.SubId == model.SubUserMonitoringKey {
		return t.usersCardReply(key)
	}
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.tgaccount.title", "Name=="+html.EscapeString(tg.Name)))
	if tg.TgId != 0 {
		b.WriteString(t.I18nBot("tgbot.tgaccount.current", "TgId=="+strconv.FormatInt(tg.TgId, 10), "Account=="+tgAccountLabel(tg.Account)))
	} else {
		b.WriteString(t.I18nBot("tgbot.tgaccount.none"))
	}
	if tg.Conflict {
		b.WriteString(t.I18nBot("tgbot.tgaccount.conflict"))
	}
	var rows [][]telego.InlineKeyboardButton
	if len(tg.Candidates) > 0 {
		b.WriteString(t.I18nBot("tgbot.tgaccount.onClients"))
	}
	for _, c := range tg.Candidates {
		id := strconv.FormatInt(c.TgId, 10)
		b.WriteString(t.I18nBot("tgbot.tgaccount.candidate", "TgId=="+id, "Account=="+tgAccountLabel(c.Account),
			"Clients=="+html.EscapeString(strings.Join(c.Clients, ", "))))
		if c.Owner != "" {
			b.WriteString(t.I18nBot("tgbot.tgaccount.owner", "Name=="+html.EscapeString(c.Owner)))
		}
		if len(c.AlsoOn) > 0 {
			b.WriteString(t.I18nBot("tgbot.tgaccount.alsoOn", "Names=="+html.EscapeString(strings.Join(c.AlsoOn, ", "))))
		}
		if c.Assignable {
			rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tgaccount.assign", "TgId=="+id)).
				WithCallbackData(t.encodeQuery("usr_tga "+tg.SubId+" "+id))))
		}
	}
	if tg.TgId != 0 || len(tg.Candidates) > 0 {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tgaccount.unlink")).
			WithCallbackData(t.encodeQuery("usr_tgu "+tg.SubId))))
	}
	reply := usersReply{text: b.String(), route: "usr_tg " + tg.SubId}
	if len(rows) > 0 {
		reply.keyboard = tu.InlineKeyboard(rows...)
	}
	return reply
}

// usersUnlinkConfirm asks before the user and its clients lose their id.
func (t *Tgbot) usersUnlinkConfirm(key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	return usersReply{text: t.I18nBot("tgbot.tgaccount.confirmUnlink", "Name=="+html.EscapeString(v.Name)),
		keyboard: tu.InlineKeyboard(
			tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.users.confirm")).
				WithCallbackData(t.encodeQuery("usr_tguc " + key)))),
		route: "usr_tgu " + key}
}
