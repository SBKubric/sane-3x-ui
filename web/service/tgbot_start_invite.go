package service

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// /start <token> (#187 point 2, #219, docs/spec/users.md §11): the deep
// link of an invite, pressed by anyone — the person the admin sent it to,
// who is no admin and usually has no user yet. TgInviteService decides; the
// sender sees the outcome over their own main menu («My subscription» once
// linked), and the notification channel hears of it. The sender learns
// nothing of any user but their own: a refusal names nobody.

// tgInviteTokenPattern is what a start parameter of ours can look like; any
// other parameter is a plain /start.
var tgInviteTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// answerStartInvite runs /start <token> in a private chat; false for any
// other command, which then runs as before.
func (t *Tgbot) answerStartInvite(message *telego.Message, isAdmin bool) bool {
	command, _, args := tu.ParseCommand(message.Text)
	if command != "start" || len(args) != 1 || message.From == nil || message.Chat.ID != message.From.ID ||
		!tgInviteTokenPattern.MatchString(args[0]) {
		return false
	}
	from := *message.From
	line := t.I18nBot("tgbot.answers.errorOperation")
	r, err := (&TgInviteService{}).Redeem(args[0], from.ID)
	if err != nil {
		logger.Warning("telegram invite:", err)
	} else {
		line = t.startInviteOutcome(r, from)
	}
	var home screenReply
	if isAdmin {
		home = t.screenMainMenu()
	} else {
		home = t.mysubHome(from.ID)
	}
	home.text = line + "\r\n\r\n" + home.text
	if isAdmin {
		t.screenOpen(message.Chat.ID, home)
	} else {
		t.clientOpen(message.Chat.ID, home)
	}
	return true
}

// startInviteOutcome tells the channel what the redeem came to and returns
// the line the sender sees.
func (t *Tgbot) startInviteOutcome(r *TgInviteRedeem, from telego.User) string {
	account := "Account==" + tgSenderLabel(from)
	name := ""
	if r.User != nil {
		name = "Name==" + html.EscapeString(r.User.Name)
	}
	switch r.Outcome {
	case TgInviteLinked:
		t.SendMsgToNotifyChannel(t.I18nBot("tgbot.tginvite.notifyLinked", name, account))
		return t.I18nBot("tgbot.tginvite.linked")
	case TgInviteAlready:
		return t.I18nBot("tgbot.tginvite.linked")
	case TgInviteExpired:
		t.SendMsgToNotifyChannel(t.I18nBot("tgbot.tginvite.notifyExpired", name, account))
		return t.I18nBot("tgbot.tginvite.expired")
	case TgInviteTaken:
		t.SendMsgToNotifyChannel(t.I18nBot("tgbot.tginvite.notifyTaken", name, account,
			"Owner=="+html.EscapeString(r.Owner.Name)))
		return t.I18nBot("tgbot.tginvite.taken")
	}
	// Used up or replaced. A token nobody issued names nobody to tell of.
	if r.User != nil {
		t.SendMsgToNotifyChannel(t.I18nBot("tgbot.tginvite.notifyUsed", name, account))
	}
	return t.I18nBot("tgbot.tginvite.used")
}

// tgSenderLabel names a Telegram account for the admins: its @nick, else
// its name and id.
func tgSenderLabel(u telego.User) string {
	if u.Username != "" {
		return "@" + html.EscapeString(u.Username)
	}
	id := strconv.FormatInt(u.ID, 10)
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return html.EscapeString(name) + " (" + id + ")"
	}
	return id
}
