package service

import (
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
)

// startFrom is /start <args> as someone sends it in their private chat with
// the bot, which is what a deep link t.me/<bot>?start=<token> does.
func startFrom(tg *Tgbot, from telego.User, text string) {
	tg.answerCommand(&telego.Message{Text: text, Chat: telego.Chat{ID: from.ID, Type: "private"}, From: &from},
		from.ID, checkAdmin(from.ID))
}

// petrov is a person the bot has never linked: no admin, no user.
var petrov = telego.User{ID: usersTestChat, FirstName: "Petr", Username: "petrov"}

// TestStartInviteLinksTheSender (#187 point 2): Start with a user's invite
// links the sender's Telegram to the user and every one of its clients,
// shows the sender «My subscription», and tells the notification channel.
// The token is used up: a second person gets a refusal and nothing.
func TestStartInviteLinksTheSender(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 2}})
	inv, err := (&TgInviteService{}).Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}

	startFrom(tg, petrov, "/start "+inv.Token)
	text, labels := fake.lastSent(t)
	if !strings.Contains(text, "Telegram is linked to your subscription") || !strings.Contains(text, "My subscription</b> · ivan") ||
		strings.Join(labels, "|") != "🔗 Show subscription|📄 My configs|🔄 Refresh" {
		t.Errorf("the sender sees %q %q", text, labels)
	}
	v := mustGetUser(t, ivan.SubId)
	if v.TgId != usersTestChat {
		t.Errorf("ivan: tgId %d", v.TgId)
	}
	for _, c := range v.Clients {
		if c.TgId != usersTestChat {
			t.Errorf("client %s: tgId %d", c.Name, c.TgId)
		}
	}
	if got := fake.sentTo(testNotifyChannel); len(got) != 1 || got[0] != "🔗 ivan linked Telegram @petrov" {
		t.Errorf("the channel got %q", got)
	}

	// Somebody else with the same link: refused, told so, nothing moves.
	fake.calls = nil
	other := telego.User{ID: 778, FirstName: "Other"}
	startFrom(tg, other, "/start "+inv.Token)
	if text, _ := fake.lastSent(t); !strings.Contains(text, "This invite link is used up or was replaced") ||
		strings.Contains(text, "ivan") {
		t.Errorf("a used link: %q", text)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != usersTestChat {
		t.Errorf("a used link moved ivan to %d", v.TgId)
	}
}

// TestStartInviteOfALinkedAccount (#187 point 4): the sender's Telegram is
// another user's already. They are told to contact the admin — no name of
// the invite's user reaches them — the channel hears both names, and
// nothing moves.
func TestStartInviteOfALinkedAccount(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgId: usersTestChat, InboundIds: []int{1}})
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	inv, err := (&TgInviteService{}).Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}

	startFrom(tg, petrov, "/start "+inv.Token)
	sent := strings.Join(fake.sentTo(usersTestChat), "\n")
	if !strings.Contains(sent, "This Telegram is already linked to another subscription, contact the admin") ||
		strings.Contains(sent, "ivan") {
		t.Errorf("the sender got %q", sent)
	}
	if got := fake.sentTo(testNotifyChannel); len(got) != 1 || !strings.Contains(got[0], "@petrov") ||
		!strings.Contains(got[0], "ivan") || !strings.Contains(got[0], "anna") {
		t.Errorf("the channel got %q", got)
	}
	if v := mustGetUser(t, anna.SubId); v.TgId != usersTestChat {
		t.Errorf("anna: %d", v.TgId)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != 0 {
		t.Errorf("ivan: %d", v.TgId)
	}
}

// TestStartInviteExpired: past its seven days the link links nobody; the
// sender is told to ask for a new one, the channel hears it.
func TestStartInviteExpired(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1}})
	inv, err := (&TgInviteService{}).Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}
	inviteClock(t, time.Now().Add(8*24*time.Hour))

	startFrom(tg, petrov, "/start "+inv.Token)
	if text, _ := fake.lastSent(t); !strings.Contains(text, "This invite link has expired") || strings.Contains(text, "ivan") {
		t.Errorf("the sender sees %q", text)
	}
	if got := fake.sentTo(testNotifyChannel); len(got) != 1 || !strings.Contains(got[0], "expired") || !strings.Contains(got[0], "ivan") {
		t.Errorf("the channel got %q", got)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != 0 {
		t.Errorf("ivan: %d", v.TgId)
	}
}

// TestStartWithoutAnInviteIsAsBefore: a plain /start, and a start parameter
// that is no token of ours, open the sender's own screen as before; a
// token-shaped one nobody issued is refused. Neither changes a thing nor
// tells the channel.
func TestStartWithoutAnInviteIsAsBefore(t *testing.T) {
	tg := accessBotFixture(t)
	setSetting(t, "tgNotifyChatId", testNotifyChannel)
	fake := withFakeTelegram(t)
	before := botState(t)

	for _, text := range []string{"/start", "/start not+a+token"} {
		fake.calls = nil
		clientCommand(tg, text)
		if got, _ := fake.lastSent(t); !strings.Contains(got, "My subscription</b> · mine-1") || strings.Contains(got, "invite") {
			t.Errorf("%s: %q", text, got)
		}
	}
	fake.calls = nil
	clientCommand(tg, "/start "+strings.Repeat("x", 43))
	if got, _ := fake.lastSent(t); !strings.Contains(got, "This invite link is used up or was replaced") ||
		!strings.Contains(got, "My subscription</b> · mine-1") || strings.Contains(got, "other") {
		t.Errorf("unknown token: %q", got)
	}
	if got := fake.sentTo(testNotifyChannel); len(got) != 0 {
		t.Errorf("the channel got %q", got)
	}
	if after := botState(t); after != before {
		t.Fatalf("the starts changed the bot's state:\n before %s\n after  %s", before, after)
	}
}
