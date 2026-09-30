package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/pelletier/go-toml/v2"
)

// «⚙️ Server» → «📣 Notification channel» (#202): the admin sets, tests and
// disables the notification channel from the bot.

// channelTelegram is the screen's fake Bot API with the channel beside it:
// a message to any chat but the admin's is a post to the channel, which it
// records, or refuses as Telegram would when refuse is set.
type channelTelegram struct {
	*screenTelegram
	posts  []string // "<chat id> <text>"
	refuse string   // Telegram's description of the refusal; "" = the post goes
}

func (f *channelTelegram) Call(ctx context.Context, url string, req *ta.RequestData) (*ta.Response, error) {
	if strings.HasSuffix(url, "/sendMessage") {
		var p struct {
			ChatID any    `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal(req.BodyRaw, &p)
		if chat := fmt.Sprint(p.ChatID); chat != fmt.Sprint(float64(usersTestChat)) {
			if f.refuse != "" {
				return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 400, Description: f.refuse}}, nil
			}
			f.posts = append(f.posts, chat+" "+p.Text)
			return &ta.Response{Ok: true, Result: json.RawMessage(`{"message_id":1,"date":0,"chat":{"id":-1,"type":"channel"}}`)}, nil
		}
	}
	return f.screenTelegram.Call(ctx, url, req)
}

// notifyScreenFixture opens «📣 Notification channel» from the server
// screen on /start's screen (#1), with channel as the stored setting.
func notifyScreenFixture(t *testing.T, channel string) (*Tgbot, *channelTelegram) {
	t.Helper()
	tg, screen := serverScreenFixture(t)
	setSetting(t, "tgNotifyChatId", channel)
	fake := &channelTelegram{screenTelegram: screen}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(fake), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	bot = b // withScreenTelegram puts the previous bot back
	resetNotifyLastTest()
	t.Cleanup(resetNotifyLastTest)
	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "⚙️ Server")
	fake.press(t, tg, 1, "📣 Notification channel")
	return tg, fake
}

// adminSends is a message of the admin's while the chat waits for the
// channel, as the bot's message handler passes it on.
func adminSends(t *testing.T, tg *Tgbot, message telego.Message) {
	t.Helper()
	state, waiting := userStates.get(usersTestChat)
	if !waiting || state != notifyStateChannel {
		t.Fatalf("the chat does not wait for the channel (state %q)", state)
	}
	message.Chat = telego.Chat{ID: usersTestChat}
	if message.From == nil {
		message.From = &telego.User{ID: 1}
	}
	tg.answerChatState(&message)
}

func storedChannel(t *testing.T) string {
	t.Helper()
	value, err := (&SettingService{}).GetTgNotifyChatId()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func notifyScreen(fake *channelTelegram) *screenMessage { return fake.messages[1] }

func wantNotifyScreen(t *testing.T, fake *channelTelegram, want ...string) {
	t.Helper()
	m := notifyScreen(fake)
	got := m.text + "\n" + strings.Join(m.labels, "|")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("want %q on the screen:\n%s", w, got)
		}
	}
}

// TestNotifyScreenShowsTheChannel: the screen names the channel, or says
// none is set, tells how to set one, and offers Test and Disable only for a
// channel that is set.
func TestNotifyScreenShowsTheChannel(t *testing.T) {
	_, fake := notifyScreenFixture(t, "")
	wantNotifyScreen(t, fake, "Notification channel", "not set", "forward", "⬅️ Back")
	if labels := strings.Join(notifyScreen(fake).labels, "|"); strings.Contains(labels, "Test") || strings.Contains(labels, "Disable") {
		t.Errorf("no channel, yet the buttons %q", labels)
	}

	_, fake = notifyScreenFixture(t, "-1001234567890")
	wantNotifyScreen(t, fake, "<code>-1001234567890</code>", "no test yet", "🧪 Test", "✖ Disable")
	if len(fake.posts) != 0 {
		t.Errorf("opening the screen posted %q", fake.posts)
	}
}

// TestNotifyScreenSetsTheChannel: an @username, a numeric chat id and a post
// forwarded from the channel each become the channel, which gets a test
// message at once; the screen says so and the admin's message goes.
func TestNotifyScreenSetsTheChannel(t *testing.T) {
	for name, tc := range map[string]struct {
		message telego.Message
		want    string
	}{
		"username": {telego.Message{MessageID: 100, Text: " @my_channel "}, "@my_channel"},
		"chat id":  {telego.Message{MessageID: 100, Text: "-1009876543210"}, "-1009876543210"},
		"forwarded post": {telego.Message{MessageID: 100, Text: "a post",
			ForwardOrigin: &telego.MessageOriginChannel{Type: telego.OriginTypeChannel,
				Chat: telego.Chat{ID: -1005550001111, Type: telego.ChatTypeChannel, Title: "News", Username: "news_chan"}}},
			"-1005550001111"},
		"forwarded photo": {telego.Message{MessageID: 100, Caption: "a photo",
			ForwardOrigin: &telego.MessageOriginChannel{Type: telego.OriginTypeChannel,
				Chat: telego.Chat{ID: -1005550002222, Type: telego.ChatTypeChannel, Title: "Private"}}},
			"-1005550002222"},
	} {
		t.Run(name, func(t *testing.T) {
			tg, fake := notifyScreenFixture(t, "")
			adminSends(t, tg, tc.message)

			if got := storedChannel(t); got != tc.want {
				t.Errorf("stored %q, want %q", got, tc.want)
			}
			if len(fake.posts) != 1 || !strings.Contains(fake.posts[0], "notification channel works") {
				t.Errorf("the test message: %q", fake.posts)
			}
			wantNotifyScreen(t, fake, "Channel saved", "<code>"+tc.want+"</code>", "✅ ok", "🧪 Test", "✖ Disable")
			if last := fake.calls[len(fake.calls)-1]; last != "deleteMessage #100" {
				t.Errorf("the admin's message should be deleted: %q", fake.calls)
			}
			if len(fake.live()) != 1 {
				t.Errorf("the chat shows %v", fake.live())
			}
			if _, waiting := userStates.get(usersTestChat); !waiting {
				t.Error("the screen should keep waiting for another channel")
			}
		})
	}
}

// TestNotifyScreenRefusesWhatIsNoChannel: a forward from a user, a text that
// is no chat and a message without text change nothing; the screen says why
// and waits for the next try.
func TestNotifyScreenRefusesWhatIsNoChannel(t *testing.T) {
	for name, tc := range map[string]struct {
		message telego.Message
		want    string
	}{
		"forward from a user": {telego.Message{MessageID: 100, Text: "hi",
			ForwardOrigin: &telego.MessageOriginUser{Type: telego.OriginTypeUser, SenderUser: telego.User{ID: 42, FirstName: "Bob"}}},
			"not from a channel"},
		"forward from a hidden user": {telego.Message{MessageID: 100, Text: "hi",
			ForwardOrigin: &telego.MessageOriginHiddenUser{Type: telego.OriginTypeHiddenUser, SenderUserName: "Bob"}},
			"not from a channel"},
		"a word":    {telego.Message{MessageID: 100, Text: "channel"}, "not a channel"},
		"a sticker": {telego.Message{MessageID: 100, Sticker: &telego.Sticker{FileID: "x"}}, "not a channel"},
	} {
		t.Run(name, func(t *testing.T) {
			tg, fake := notifyScreenFixture(t, "@old_channel")
			adminSends(t, tg, tc.message)

			if got := storedChannel(t); got != "@old_channel" {
				t.Errorf("stored %q, want the old channel kept", got)
			}
			if len(fake.posts) != 0 {
				t.Errorf("posted %q", fake.posts)
			}
			wantNotifyScreen(t, fake, "⚠️", tc.want, "<code>@old_channel</code>")
			if last := fake.calls[len(fake.calls)-1]; last != "deleteMessage #100" {
				t.Errorf("the admin's message should be deleted: %q", fake.calls)
			}
			if _, waiting := userStates.get(usersTestChat); !waiting {
				t.Error("the screen should keep waiting for a channel")
			}
		})
	}
}

// TestNotifyScreenSavesDespiteAFailingTest: the channel is saved even when
// Telegram refuses the test message, and the screen shows the refusal; the
// Test button tries again.
func TestNotifyScreenSavesDespiteAFailingTest(t *testing.T) {
	tg, fake := notifyScreenFixture(t, "")
	fake.refuse = "Bad Request: need administrator rights in the channel chat"
	adminSends(t, tg, telego.Message{MessageID: 100, Text: "@my_channel"})

	if got := storedChannel(t); got != "@my_channel" {
		t.Errorf("stored %q", got)
	}
	wantNotifyScreen(t, fake, "Channel saved", "❌", "need administrator rights")

	fake.refuse = ""
	fake.press(t, tg, 1, "🧪 Test")
	if len(fake.posts) != 1 || !strings.HasPrefix(fake.posts[0], "@my_channel ") {
		t.Errorf("the test message: %q", fake.posts)
	}
	wantNotifyScreen(t, fake, "✅ ok")
	if strings.Contains(notifyScreen(fake).text, "need administrator rights") {
		t.Errorf("the old refusal is still shown: %q", notifyScreen(fake).text)
	}
}

// TestNotifyScreenShowsThePanelsTest: a test sent from the panel's settings
// for the stored channel is the screen's last test too; one for another,
// unsaved value is not.
func TestNotifyScreenShowsThePanelsTest(t *testing.T) {
	tg, fake := notifyScreenFixture(t, "@my_channel")
	fake.refuse = "Bad Request: chat not found"
	_ = tg.SendNotifyTest("@other_channel")
	fake.press(t, tg, 1, "🔄 Refresh")
	wantNotifyScreen(t, fake, "no test yet")

	_ = tg.SendNotifyTest("@my_channel")
	fake.press(t, tg, 1, "🔄 Refresh")
	wantNotifyScreen(t, fake, "❌", "chat not found")
}

// TestNotifyScreenDisables: «✖ Disable» asks first; Back keeps the channel,
// Confirm clears the setting and the screen says none is set.
func TestNotifyScreenDisables(t *testing.T) {
	tg, fake := notifyScreenFixture(t, "@my_channel")

	fake.press(t, tg, 1, "✖ Disable")
	wantNotifyScreen(t, fake, "Disable the notification channel?", "✅ Confirm")
	fake.press(t, tg, 1, "⬅️ Back")
	if got := storedChannel(t); got != "@my_channel" {
		t.Errorf("back from the question cleared the channel: %q", got)
	}
	wantNotifyScreen(t, fake, "<code>@my_channel</code>")

	fake.press(t, tg, 1, "✖ Disable")
	fake.press(t, tg, 1, "✅ Confirm")
	if got := storedChannel(t); got != "" {
		t.Errorf("stored %q after disabling", got)
	}
	wantNotifyScreen(t, fake, "disabled", "not set")
	if len(fake.posts) != 0 {
		t.Errorf("posted %q", fake.posts)
	}
	if _, waiting := userStates.get(usersTestChat); !waiting {
		t.Error("the screen should wait for a new channel")
	}
}

// TestNotifyScreenIsForAdmins: someone who is no admin gets «No result» for
// every button of the screen and nothing changes; their messages set no
// channel.
func TestNotifyScreenIsForAdmins(t *testing.T) {
	tg := accessBotFixture(t)
	setSetting(t, "tgNotifyChatId", "@my_channel")
	fake := withFakeTelegram(t)
	before := botState(t)

	for _, data := range []string{screenNotifyRoute, screenNotifyTestData, screenNotifyOffAsk, screenNotifyOffData} {
		fake.calls = nil
		nonAdminPress(tg, data)
		if len(fake.calls) != 1 || fake.calls[0].method != "answerCallbackQuery" ||
			fake.calls[0].params["text"] != "❗ No result!" {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
	}
	fake.calls = nil
	taken := tg.answerNotifyText(&telego.Message{MessageID: 100, Text: "@their_channel", Chat: telego.Chat{ID: usersTestChat},
		From: &telego.User{ID: usersTestChat}}, notifyStateChannel)
	if !taken || len(fake.calls) != 0 {
		t.Errorf("a non-admin's message: taken %v, calls\n%s", taken, fake.texts())
	}
	if after := botState(t); after != before {
		t.Errorf("the bot's state changed:\n before %s\n after  %s", before, after)
	}
}

// TestNotifyScreenCallbacksFitTelegram: the screen's buttons keep their
// callback data within Telegram's 64 bytes.
func TestNotifyScreenCallbacksFitTelegram(t *testing.T) {
	tg, _ := notifyScreenFixture(t, "-1001234567890")
	for _, data := range []string{screenNotifyRoute, screenNotifyOffAsk} {
		reply := screenPressData(t, tg, data)
		sc := &botScreen{route: "x", back: []string{screenMenuRoute}}
		buttons(t, tg.screenKeyboard(sc, reply.keyboard))
	}
}

// TestNotifyScreenSpeaksRussian: the screen renders in the bot's language.
func TestNotifyScreenSpeaksRussian(t *testing.T) {
	tg, _ := notifyScreenFixture(t, "")
	initTestBotLocale(t, "ru-RU")
	reply := screenPressData(t, tg, screenNotifyRoute)
	if got := reply.text; !strings.Contains(got, "Канал уведомлений") || !strings.Contains(got, "не задан") {
		t.Errorf("screen: %q", got)
	}
	if got := strings.Join(buttonTexts(t, tg.screenServer().keyboard), "|"); !strings.Contains(got, "📣 Канал уведомлений") {
		t.Errorf("server buttons: %q", got)
	}
}

// TestNotifyTextsInEveryLanguage: every translation file carries the keys
// of [tgbot.notify], so no language renders an empty screen.
func TestNotifyTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Notify map[string]string `toml:"notify"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Notify {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.notify] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.notify] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}

// resetNotifyLastTest forgets the last test message.
func resetNotifyLastTest() {
	notifyLastTest.mu.Lock()
	defer notifyLastTest.mu.Unlock()
	notifyLastTest.last = nil
}
