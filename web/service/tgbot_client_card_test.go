package service

import (
	"strings"
	"testing"
)

// clientPress runs a button of the xray client card and fails when nothing
// handles it.
func clientPress(t *testing.T, bot *Tgbot, data string) screenReply {
	t.Helper()
	reply, ok := bot.clientCardCallback(data)
	if !ok {
		t.Fatalf("callback %q not handled", data)
	}
	return reply
}

// TestClientCardButtons: the xray client card keeps every upstream action
// and gains its links and the way to its user.
func TestClientCardButtons(t *testing.T) {
	bot := usersBotFixture(t)
	owner, err := (&SubUserService{}).Get("s-other")
	if err != nil {
		t.Fatal(err)
	}

	reply := clientPress(t, bot, "client_get_usage other-nl")
	want := []string{"🔄 Refresh", "📈 Reset Traffic", "🚧 Traffic Limit", "📅 Change Expiry Date", "🔢 IP Log", "🔢 IP Limit",
		"👤 Set Telegram User", "🔘 Enable / Disable", "📄 Links and QR", "👤 " + owner.Name}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("buttons:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(reply.text, "other-nl") || reply.route != "client_get_usage other-nl" {
		t.Errorf("card: %+v", reply.usersReply)
	}
	if user := button(t, reply.keyboard, "👤 "+owner.Name); user != "usr_c s-other" {
		t.Errorf("user button = %q", user)
	}
	if refresh := clientPress(t, bot, button(t, reply.keyboard, "Refresh")); refresh.route != reply.route {
		t.Errorf("refresh shows %q", refresh.route)
	}
	if links := clientPress(t, bot, button(t, reply.keyboard, "Links and QR")); links.after == nil || links.text != "" {
		t.Errorf("links go below the screen: %+v", links)
	}

	if none := clientPress(t, bot, "client_get_usage nobody"); none.text == "" || none.keyboard != nil || none.route != "" {
		t.Errorf("no such client: %+v", none)
	}
}

// TestClientCardToggle: enable/disable asks in place of the buttons, then
// switches the client and shows the card again.
func TestClientCardToggle(t *testing.T) {
	bot := usersBotFixture(t)
	ask := clientPress(t, bot, button(t, clientPress(t, bot, "client_get_usage other-nl").keyboard, "Enable / Disable"))
	if ask.text != "" || ask.route != "" {
		t.Errorf("the question changes only the buttons: %+v", ask.usersReply)
	}
	if cancel := button(t, ask.keyboard, "Cancel"); cancel != "client_cancel other-nl" {
		t.Errorf("cancel = %q", cancel)
	}
	reply := clientPress(t, bot, button(t, ask.keyboard, "Confirm Enable/Disable"))
	if reply.toast != "✅ other-nl: Disabled successfully." || reply.route != "client_get_usage other-nl" {
		t.Errorf("after the toggle: %+v", reply.usersReply)
	}
	v, _ := (&SubUserService{}).Get("s-other")
	if c := clientByName(t, v, "other-nl"); c.Enable {
		t.Error("the client stayed enabled")
	}
}

// TestClientCardTrafficLimit: a limit typed on the keypad is saved.
func TestClientCardTrafficLimit(t *testing.T) {
	bot := usersBotFixture(t)
	reply := clientPress(t, bot, button(t, clientPress(t, bot, "client_get_usage other-nl").keyboard, "Traffic Limit"))
	reply = clientPress(t, bot, button(t, reply.keyboard, "Custom"))
	reply = clientPress(t, bot, button(t, reply.keyboard, "1"))
	reply = clientPress(t, bot, button(t, reply.keyboard, "5"))
	reply = clientPress(t, bot, button(t, reply.keyboard, "Confirm adding: 15"))
	if reply.toast != "✅ other-nl: Traffic limit saved successfully." {
		t.Errorf("after the limit: %+v", reply.usersReply)
	}
	v, _ := (&SubUserService{}).Get("s-other")
	if c := clientByName(t, v, "other-nl"); c.TotalGB != 15<<30 {
		t.Errorf("limit: %d", c.TotalGB)
	}
}

// TestClientCardSubScreens: the IP log and the Telegram user are views of
// their own; the Telegram user also sends the picker, which needs a message
// of its own.
func TestClientCardSubScreens(t *testing.T) {
	bot := usersBotFixture(t)
	card := clientPress(t, bot, "client_get_usage other-nl")
	ips := clientPress(t, bot, button(t, card.keyboard, "IP Log"))
	if ips.route != "ip_log other-nl" || !strings.Contains(strings.Join(buttonTexts(t, ips.keyboard), "|"), "Clear IPs") {
		t.Errorf("IP log: %+v", ips.usersReply)
	}
	tg := clientPress(t, bot, button(t, card.keyboard, "Set Telegram User"))
	if tg.route != "tg_user other-nl" || tg.after == nil {
		t.Errorf("Telegram user: %+v", tg.usersReply)
	}
}
