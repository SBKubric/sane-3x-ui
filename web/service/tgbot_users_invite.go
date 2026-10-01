package service

import (
	"context"
	"errors"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"
)

// Binding a user's Telegram from its card (#187 points 1–4, #219,
// docs/spec/users.md §11), admins only. A card without Telegram says
// «📱 Telegram: not linked» and offers:
//   - «🔗 Invite link»: the user's open invite as t.me/<bot>?start=<token>,
//     «🖼 QR» for its picture and «🔄 Reissue» for a new one that voids it;
//   - «✏️ Enter tg_id or @nick» (also on the Telegram screen of a user who
//     has one): a number, or the @nick of an account the bot has seen. A
//     nick nobody has is answered with the hint to send the invite link;
//     an id another user has names that user and offers «➡️ Move here»,
//     which asks first.
//
// Callback data: usr_tgi <key> (the invite), usr_tgr <key> (reissue),
// usr_tgq <key> (its QR, a file: tgbot_screen_menu.go), usr_tge <key> (the
// entry), usr_tgm <key> <tgId> (the move's question), usr_tgmc <key> <tgId>
// (the move).

// Callback data of the invite and the entry.
const (
	tgInviteAction   = "usr_tgi"
	tgReissueAction  = "usr_tgr"
	tgInviteQRAction = "usr_tgq"
	tgEntryAction    = "usr_tge"
	tgMoveAction     = "usr_tgm"
	tgMoveDoAction   = "usr_tgmc"
)

// usersStateTelegram is the chat state while the entry waits for a tg_id or
// an @nick; the user is the session's telegramKey.
const usersStateTelegram = "usr_tgset"

// usersInviteCallback runs a button of the invite and the entry; ok is false
// for data that is not one.
func (t *Tgbot) usersInviteCallback(chatId int64, data string) (reply usersReply, ok bool) {
	action, args, _ := strings.Cut(data, " ")
	key, rest, _ := strings.Cut(args, " ")
	switch action {
	case tgInviteAction:
		return t.usersInvite(args, false), true
	case tgReissueAction:
		return t.usersInvite(args, true), true
	case tgEntryAction:
		return t.usersTelegramAsk(chatId, args), true
	case tgMoveAction, tgMoveDoAction:
		tgId, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || tgId <= 0 {
			return t.usersCardReply(key), true
		}
		if action == tgMoveAction {
			return t.usersMoveConfirm(key, tgId), true
		}
		return t.usersTelegramDone(key)((&SubUserService{}).MoveTelegram(key, tgId)), true
	}
	return usersReply{}, false
}

// usersTelegramUnlinkedRows are the card's ways to bind Telegram: for a
// regular user without one.
func (t *Tgbot) usersTelegramUnlinkedRows(v *SubUserView) [][]telego.InlineKeyboardButton {
	if v.Technical || v.TgId != 0 {
		return nil
	}
	return [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tginvite.inviteButton")).
			WithCallbackData(t.encodeQuery(tgInviteAction + " " + v.SubId))),
		t.usersEntryRow(v.SubId),
	}
}

// usersEntryRow is «✏️ Enter tg_id or @nick» for the user under key.
func (t *Tgbot) usersEntryRow(key string) []telego.InlineKeyboardButton {
	return tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tginvite.enterButton")).
		WithCallbackData(t.encodeQuery(tgEntryAction + " " + key)))
}

// --- the invite link ------------------------------------------------------------

// usersInvite shows the user's open invite link, or a new one when reissue
// (the old one stops working). A user with Telegram, or a technical one,
// gets its card: invites bind the ones without.
func (t *Tgbot) usersInvite(key string, reissue bool) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	if v.Technical || v.TgId != 0 {
		return t.usersCardOf(v)
	}
	invites := &TgInviteService{}
	issue := invites.Invite
	if reissue {
		issue = invites.Reissue
	}
	inv, err := issue(key)
	if err != nil {
		return t.usersRefused(key, err)
	}
	name := "Name==" + html.EscapeString(v.Name)
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.tginvite.title", name))
	var rows [][]telego.InlineKeyboardButton
	if link := t.tgInviteLink(inv.Token); link != "" {
		b.WriteString(t.I18nBot("tgbot.tginvite.link", "Link=="+html.EscapeString(link)))
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.screen.subQR")).
			WithCallbackData(t.encodeQuery(tgInviteQRAction+" "+key))))
	} else {
		b.WriteString(t.I18nBot("tgbot.tginvite.noBot"))
	}
	b.WriteString(t.I18nBot("tgbot.tginvite.about", name,
		"Until=="+time.UnixMilli(inv.ExpiresAt).Format("02.01.2006 15:04")))
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tginvite.reissueButton")).
		WithCallbackData(t.encodeQuery(tgReissueAction+" "+key))))
	reply := usersReply{text: b.String(), keyboard: tu.InlineKeyboard(rows...), route: tgInviteAction + " " + key}
	if reissue {
		reply.toast = t.I18nBot("tgbot.tginvite.reissued")
	}
	return reply
}

// usersInviteQR sends the QR of the user's open invite link as a picture.
func (t *Tgbot) usersInviteQR(key string) screenReply {
	failed := screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}
	v, err := (&SubUserService{}).Get(key)
	if err != nil || v.Technical {
		return failed
	}
	inv, err := (&TgInviteService{}).Current(key)
	if err != nil || inv == nil {
		return failed
	}
	link := t.tgInviteLink(inv.Token)
	if link == "" {
		return failed
	}
	png, err := qrcode.Encode(link, qrcode.Medium, 320)
	if err != nil {
		return failed
	}
	return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation")},
		files: []tunnelFile{{name: v.Name + "-telegram.png", data: png}}}
}

// botSelf caches the bot's own @username (getMe) for the bot it was read
// from: a restart of the bot, with maybe another token, reads it again.
var botSelf struct {
	mu       sync.Mutex
	bot      *telego.Bot
	username string
}

// BotUsername is the running bot's @username (without the '@'), "" when the
// bot is not running or Telegram does not say.
func (t *Tgbot) BotUsername() string {
	tgBotMutex.Lock()
	b, running := bot, isRunning
	tgBotMutex.Unlock()
	if b == nil || !running {
		return ""
	}
	botSelf.mu.Lock()
	defer botSelf.mu.Unlock()
	if botSelf.bot == b && botSelf.username != "" {
		return botSelf.username
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	me, err := b.GetMe(ctx)
	if err != nil {
		logger.Warning("telegram: could not read the bot's @username:", err)
		return ""
	}
	botSelf.bot, botSelf.username = b, me.Username
	return me.Username
}

// tgInviteLink is the deep link of an invite token: t.me/<bot>?start=<token>;
// "" while the bot's @username is unknown.
func (t *Tgbot) tgInviteLink(token string) string {
	name := t.BotUsername()
	if name == "" {
		return ""
	}
	return "https://t.me/" + name + "?start=" + token
}

// --- the typed tg_id or @nick ---------------------------------------------------

// usersTelegramAsk makes the chat wait for the user's tg_id or @nick.
func (t *Tgbot) usersTelegramAsk(chatId int64, key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	if v.Technical {
		return t.usersCardOf(v)
	}
	usersSessions.with(chatId, func(s *usersSession) { s.telegramKey = key })
	return t.usersAsk(chatId, usersStateTelegram, t.I18nBot("tgbot.tginvite.enterPrompt", "Name=="+html.EscapeString(v.Name)))
}

// usersTelegramText takes the typed tg_id or @nick and links it. A nick the
// bot has not seen, an id another user has, or a text that is neither
// leaves the chat waiting for another try, the reason above the prompt.
func (t *Tgbot) usersTelegramText(chatId int64, text string) usersReply {
	var key string
	usersSessions.with(chatId, func(s *usersSession) { key = s.telegramKey })
	if key == "" {
		return t.usersExpired()
	}
	retry := func(why string, rows ...[]telego.InlineKeyboardButton) usersReply {
		reply := t.usersTelegramAsk(chatId, key)
		reply.text = "⚠️ " + why + "\r\n\r\n" + reply.text
		if reply.keyboard != nil {
			reply.keyboard.InlineKeyboard = append(rows, reply.keyboard.InlineKeyboard...)
		}
		return reply
	}
	tgId, err := (&TgAccountService{}).Resolve(text)
	var conflict *SubUserConflict
	switch {
	case errors.As(err, &conflict) && conflict.Code == SubUserConflictTgNickUnknown:
		nick := "@" + strings.TrimPrefix(text, "@")
		return retry(t.I18nBot("tgbot.tginvite.nickUnknown", "Nick=="+html.EscapeString(nick)),
			tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tginvite.inviteButton")).
				WithCallbackData(t.encodeQuery(tgInviteAction+" "+key))))
	case err != nil:
		return retry(t.I18nBot("tgbot.tginvite.badEntry", "Text=="+html.EscapeString(text)))
	}
	v, err := (&SubUserService{}).SetTelegram(key, tgId)
	switch {
	case errors.As(err, &conflict) && conflict.Code == SubUserConflictTgOwned:
		id := strconv.FormatInt(tgId, 10)
		return retry(t.I18nBot("tgbot.tginvite.owned", "TgId=="+id, "Owner=="+html.EscapeString(conflict.Owner)),
			tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.tginvite.moveButton")).
				WithCallbackData(t.encodeQuery(tgMoveAction+" "+key+" "+id))))
	case err != nil:
		return t.usersRefused(key, err)
	}
	usersSessions.with(chatId, func(s *usersSession) { s.telegramKey = "" })
	return t.usersDone(v)
}

// --- «Move here» ----------------------------------------------------------------

// usersMoveConfirm asks before the id is taken from the user who has it.
func (t *Tgbot) usersMoveConfirm(key string, tgId int64) usersReply {
	users := &SubUserService{}
	v, err := users.Get(key)
	if err != nil {
		return t.usersError(err)
	}
	owner := ""
	if all, err := users.List(); err == nil {
		for _, u := range all {
			if !u.Technical && u.SubId != key && u.TgId == tgId {
				owner = u.Name
			}
		}
	}
	if owner == "" { // nobody has it any more: nothing to take
		return t.usersTelegramDone(key)(users.SetTelegram(key, tgId))
	}
	id := strconv.FormatInt(tgId, 10)
	return usersReply{
		text: t.I18nBot("tgbot.tginvite.moveAsk", "TgId=="+id, "Owner=="+html.EscapeString(owner),
			"Name=="+html.EscapeString(v.Name)),
		keyboard: tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.users.confirm")).
			WithCallbackData(t.encodeQuery(tgMoveDoAction + " " + key + " " + id)))),
		route: tgMoveAction + " " + key + " " + id}
}
