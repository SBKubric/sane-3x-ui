package service

import (
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestNewUserTelegramChoices (#187 point 3): the Telegram step offers
// «Enter tg_id / @nick», «Create an invite link» and «Later»; the entry
// takes an @nick of an account the bot has seen.
func TestNewUserTelegramChoices(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "petr")
	if _, err := writeTgAccount(model.TgAccount{TgId: 6161, Username: "petr_tg"}); err != nil {
		t.Fatal(err)
	}
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels,
		"✏️ Enter tg_id / @nick|🔗 Create an invite link|Later ▶|✖ Cancel") {
		t.Errorf("the Telegram step: %q", labels)
	}
	fake.press(t, tg, 1, "✏️ Enter tg_id / @nick")
	wantScreen(t, fake, "step 5/5", "Send the user's Telegram id (a number) or @nick")
	adminText(t, tg, 400, "@nobody_tg")
	wantScreen(t, fake, "step 5/5", "⚠️", "@nobody_tg is unknown", "invite link")
	adminText(t, tg, 401, "@Petr_TG")
	wantScreen(t, fake, "Check the new user", "Telegram: 6161")

	fake.press(t, tg, 1, "Create")
	if v, err := (&SubUserService{}).Find("petr"); err != nil || v.TgId != 6161 {
		t.Fatalf("created: %+v, %v", v, err)
	}
}

// TestNewUserInviteLink (#187 point 3): «Create an invite link» creates the
// user without Telegram and shows its invite link — with its QR and the way
// to the card — instead of the card.
func TestNewUserInviteLink(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "petr")
	fake.press(t, tg, 1, "🔗 Create an invite link")
	wantScreen(t, fake, "Check the new user", "Telegram: invite link after creation")

	fake.press(t, tg, 1, "Create")
	v, err := (&SubUserService{}).Find("petr")
	if err != nil || v.TgId != 0 {
		t.Fatalf("created: %+v, %v", v, err)
	}
	inv, err := (&TgInviteService{}).Current(v.SubId)
	if err != nil || inv == nil {
		t.Fatalf("no invite: %v", err)
	}
	wantScreen(t, fake, "User petr created", "/sub/"+v.SubId, "Invite link", "https://t.me/test_bot?start="+inv.Token)
	labels := strings.Join(newUserScreen(fake).labels, "|")
	if !strings.Contains(labels, "🖼 QR code|🔄 Reissue|👤 petr") {
		t.Errorf("buttons: %q", labels)
	}
	fake.press(t, tg, 1, "👤 petr")
	wantScreen(t, fake, "<b>petr</b>", "📱 Telegram: not linked")
}

// TestNewUserLaterDropsTheInvite: «Later» after a choice made before binds
// nothing.
func TestNewUserLaterDropsTheInvite(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "petr")
	fake.press(t, tg, 1, "🔗 Create an invite link")
	fake.press(t, tg, 1, "Change")
	fake.press(t, tg, 1, "Next")
	fake.press(t, tg, 1, "Next")
	fake.press(t, tg, 1, "Later")
	wantScreen(t, fake, "Check the new user", "Telegram: later")
	fake.press(t, tg, 1, "Create")
	v, err := (&SubUserService{}).Find("petr")
	if err != nil {
		t.Fatal(err)
	}
	if inv, err := (&TgInviteService{}).Current(v.SubId); err != nil || inv != nil {
		t.Errorf("an invite after «Later»: %+v, %v", inv, err)
	}
}
