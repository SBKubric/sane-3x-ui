package service

import (
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// markOnline makes the xray clients and tunnel clients of these emails seen
// just now, as the traffic job leaves them.
func markOnline(t *testing.T, emails ...string) {
	t.Helper()
	now := time.Now().UnixMilli()
	db := database.GetDB()
	if err := db.Model(&xray.ClientTraffic{}).Where("email IN ?", emails).Update("last_online", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.TunnelClient{}).Where("email IN ?", emails).Update("last_online", now).Error; err != nil {
		t.Fatal(err)
	}
}

// TestScreenOnlineListsUsers: «🟢 Online» lists the users with a client
// online, with the protocols they are online with, each opening the user's
// card; a client nobody owns (robot's) opens its own card; probes and the
// users who are offline are left out. «🔄 Refresh» draws it again.
func TestScreenOnlineListsUsers(t *testing.T) {
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 2}})
	mustCreateUser(t, SubUserCreate{Name: "anna", InboundIds: []int{5}})
	mustCreateUser(t, SubUserCreate{Name: "boris", InboundIds: []int{2}})
	awgPeer(t, 90, "stray-awg", "")
	awgPeer(t, 91, ProbeTunnelEmail("mc1", "direct"), "")
	markOnline(t, "ivan-de", "anna-awg", "stray-awg", ProbeTunnelEmail("mc1", "direct"))
	fake := withScreenTelegram(t)
	screen := func() *screenMessage { return fake.messages[1] }

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "🟢 Online")
	if !strings.Contains(screen().text, "Online now: 3") {
		t.Errorf("online: %q", screen().text)
	}
	want := []string{"🟢 anna · AWG", "🟢 ivan · Trojan", "🤖 stray-awg · AWG", "🔄 Refresh", "⬅️ Back", "🏠 Menu"}
	if got := screen().labels; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("online buttons:\n got %q\nwant %q", got, want)
	}

	if data := screen().data[labelIndex(t, screen(), "ivan")]; data != "usr_c "+ivan.SubId {
		t.Errorf("ivan's button: %q", data)
	}
	fake.press(t, tg, 1, "ivan")
	if !strings.Contains(screen().text, "<b>ivan</b>") {
		t.Fatalf("ivan's card: %q", screen().text)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	fake.press(t, tg, 1, "stray-awg")
	if !strings.Contains(screen().text, "stray-awg") || !strings.Contains(strings.Join(screen().labels, "|"), "Config") {
		t.Errorf("robot's client card: %q %q", screen().text, screen().labels)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	fake.press(t, tg, 1, "🔄 Refresh")
	if !strings.Contains(screen().text, "Online now: 3") || fake.sent() != 1 {
		t.Errorf("refresh: %q, calls %q", screen().text, fake.calls)
	}
}

// TestScreenOnlineEmpty: with nobody online the screen says so and keeps
// «🔄 Refresh».
func TestScreenOnlineEmpty(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	reply := screenPressData(t, tg, "s_onl")
	if !strings.Contains(reply.text, "Online now: 0") || reply.route != "s_onl 0" {
		t.Errorf("empty: %+v", reply.usersReply)
	}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != "🔄 Refresh" {
		t.Errorf("empty buttons: %q", got)
	}
}
