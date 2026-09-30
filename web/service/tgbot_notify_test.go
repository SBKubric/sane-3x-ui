package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

// The notification channel (#195): what goes there, what stays with the
// admins, and what happens while none is set.

const (
	testNotifyChannel = "@notify_channel"
	testNotifyAdmin   = int64(1001)
)

// notifyBotFixture is the users fixture with the bot talking to a
// fakeTelegram, one admin, and channel as the notification channel ("" for
// none).
func notifyBotFixture(t *testing.T, channel string) (*Tgbot, *fakeTelegram) {
	t.Helper()
	tg := usersBotFixture(t)
	setSetting(t, "tgNotifyChatId", channel)
	fake := withFakeTelegram(t)
	prevAdmins := adminIds
	adminIds = []int64{testNotifyAdmin}
	t.Cleanup(func() { adminIds = prevAdmins })
	return tg, fake
}

// sentTo is the text of every message the bot sent to chat, which is a
// chat id or an @username.
func (f *fakeTelegram) sentTo(chat any) []string {
	want := fmt.Sprint(chat)
	if id, ok := chat.(int64); ok {
		want = fmt.Sprint(float64(id)) // how a JSON number decodes
	}
	var out []string
	for _, c := range f.calls {
		if c.method == "sendMessage" && fmt.Sprint(c.params["chat_id"]) == want {
			text, _ := c.params["text"].(string)
			out = append(out, text)
		}
	}
	return out
}

// sentMessages counts every message the bot sent, wherever it went.
func (f *fakeTelegram) sentMessages() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c.method, "send") {
			n++
		}
	}
	return n
}

func anyContains(texts []string, part string) bool {
	for _, s := range texts {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

// TestNotifyChannelGetsLoginNotices: a successful and a failed panel login
// are announced in the channel and no longer in the admin's chat.
func TestNotifyChannelGetsLoginNotices(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)

	tg.UserLoginNotify("admin", "", "203.0.113.5", "2026-09-29 10:00:00", LoginSuccess)
	tg.UserLoginNotify("admin", "guess", "203.0.113.6", "2026-09-29 10:01:00", LoginFail)

	channel := fake.sentTo(testNotifyChannel)
	if !anyContains(channel, "Logged in to the panel successfully") || !anyContains(channel, "Login attempt to the panel failed") {
		t.Errorf("channel got:\n%s", fake.texts())
	}
	if got := fake.sentTo(testNotifyAdmin); len(got) != 0 {
		t.Errorf("the admin got %q", got)
	}
}

// TestNotifyChannelGetsMonitoringAlerts: monitoring events and mon-server
// going quiet and coming back reach the channel, and the events are settled.
func TestNotifyChannelGetsMonitoringAlerts(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)

	ev := monEventRow(model.MonEventKindMonClient, "mc-1", "", 0, "", "ONLINE", "OFFLINE", "", time.Now().UnixMilli())
	if ids := tg.NotifyMonitoringEvents([]model.MonEvent{ev}); len(ids) != 1 {
		t.Errorf("settled %v, want the one event", ids)
	}
	tg.NotifyMonitoringStale(time.Now())
	tg.NotifyMonitoringBack(5 * time.Minute)

	channel := fake.sentTo(testNotifyChannel)
	for _, want := range []string{"mon-client mc-1 OFFLINE", "monitoring silent since", "monitoring back (was silent 5 min)"} {
		if !anyContains(channel, want) {
			t.Errorf("channel lacks %q:\n%s", want, fake.texts())
		}
	}
	if len(channel) != 3 {
		t.Errorf("channel got %d messages, want 3", len(channel))
	}
	if got := fake.sentTo(testNotifyAdmin); len(got) != 0 {
		t.Errorf("the admin got %q", got)
	}
}

// TestNotifyChannelGetsTheReport: the periodic report — its header, the
// server summary and the exhausted clients — goes to the channel, while the
// database backup that comes with it stays in the admin's chat.
func TestNotifyChannelGetsTheReport(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	setSetting(t, "tgBotBackup", "true")

	tg.SendReport()

	channel := fake.sentTo(testNotifyChannel)
	for _, want := range []string{"Scheduled Reports", "💻 Host:", "Exhausted"} {
		if !anyContains(channel, want) {
			t.Errorf("channel lacks %q:\n%s", want, fake.texts())
		}
	}
	if anyContains(channel, "Backup Time") {
		t.Errorf("the backup went to the channel:\n%s", fake.texts())
	}
	admin := fake.sentTo(testNotifyAdmin)
	if !anyContains(admin, "Backup Time") {
		t.Errorf("the admin did not get the backup:\n%s", fake.texts())
	}
	for _, report := range []string{"Scheduled Reports", "💻 Host:", "Exhausted"} {
		if anyContains(admin, report) {
			t.Errorf("the admin still got %q", report)
		}
	}
}

// TestNotifyChannelByChatId: a numeric channel id is addressed as a number,
// for the CPU alert and every other notification alike.
func TestNotifyChannelByChatId(t *testing.T) {
	const channel = int64(-1001234567890)
	tg, fake := notifyBotFixture(t, fmt.Sprint(channel))

	tg.SendMsgToNotifyChannel("🔴 CPU Load 97.00% exceeds the threshold of 80%")

	if got := fake.sentTo(channel); len(got) != 1 || !strings.Contains(got[0], "CPU Load") {
		t.Errorf("channel %d got %q:\n%s", channel, got, fake.texts())
	}
}

// notifyUnsetWarnings counts the warnings about the missing channel in the
// panel's log buffer.
func notifyUnsetWarnings() int {
	n := 0
	for _, line := range logger.GetLogs(10000, "WARNING") {
		if strings.Contains(line, "no notification channel") {
			n++
		}
	}
	return n
}

// TestNotifyChannelUnsetSendsNothing: with no channel set the notifications
// go nowhere — not to the admins either — the backup still reaches the
// admins, monitoring events stay un-notified, and the log says why once.
func TestNotifyChannelUnsetSendsNothing(t *testing.T) {
	tg, fake := notifyBotFixture(t, "")
	before := notifyUnsetWarnings()

	tg.UserLoginNotify("admin", "guess", "203.0.113.6", "2026-09-29 10:01:00", LoginFail)
	tg.NotifyMonitoringStale(time.Now())
	ev := monEventRow(model.MonEventKindMonClient, "mc-1", "", 0, "", "ONLINE", "OFFLINE", "", time.Now().UnixMilli())
	if ids := tg.NotifyMonitoringEvents([]model.MonEvent{ev}); len(ids) != 0 {
		t.Errorf("settled %v with nowhere to announce them", ids)
	}
	tg.SendMsgToNotifyChannel("🔴 CPU Load 97.00% exceeds the threshold of 80%")
	if n := fake.sentMessages(); n != 0 {
		t.Errorf("sent %d messages:\n%s", n, fake.texts())
	}

	setSetting(t, "tgBotBackup", "true")
	tg.SendReport()
	for _, c := range fake.calls {
		if c.method == "sendMessage" && fmt.Sprint(c.params["chat_id"]) != fmt.Sprint(float64(testNotifyAdmin)) {
			t.Errorf("sent to %v: %v", c.params["chat_id"], c.params["text"])
		}
	}
	if admin := fake.sentTo(testNotifyAdmin); len(admin) != 1 || !strings.Contains(admin[0], "Backup Time") {
		t.Errorf("the admin got %q, want the backup only", admin)
	}

	if n := notifyUnsetWarnings() - before; n != 1 {
		t.Errorf("warned %d times, want once", n)
	}
}

// failingTelegram is a Bot API that refuses every message the way Telegram
// refuses a chat the bot is not in.
type failingTelegram struct{ calls int }

func (f *failingTelegram) Call(context.Context, string, *ta.RequestData) (*ta.Response, error) {
	f.calls++
	return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 400, Description: "Bad Request: chat not found"}}, nil
}

// TestNotifyTestSend: the settings tab's test message goes to the channel
// typed in the form, and Telegram's refusal comes back as the error.
func TestNotifyTestSend(t *testing.T) {
	tg, fake := notifyBotFixture(t, "")

	if err := tg.SendNotifyTest(" @typed_channel "); err != nil {
		t.Fatalf("SendNotifyTest: %v", err)
	}
	if got := fake.sentTo("@typed_channel"); len(got) != 1 || !strings.Contains(got[0], "notification channel") {
		t.Errorf("sent %q:\n%s", got, fake.texts())
	}

	for _, bad := range []string{"", "channel"} {
		if err := tg.SendNotifyTest(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}

	failing := &failingTelegram{}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(failing), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	bot = b
	err = tg.SendNotifyTest("-1001234567890")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("err = %v, want Telegram's refusal", err)
	}
	if failing.calls != 1 {
		t.Errorf("Telegram was called %d times, want once", failing.calls)
	}

	isRunning = false
	if err := tg.SendNotifyTest("@typed_channel"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("with the bot stopped: err = %v", err)
	}
}
