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

// screenTelegram stands in for the Bot API in the screen tests: it numbers
// the messages it is sent, remembers which are still in the chat, and keeps
// every message's latest text and buttons, as a Telegram client would show
// them.
type screenTelegram struct {
	next     int
	calls    []string // "method #id", in order
	messages map[int]*screenMessage
	// failEdit makes every edit fail as when the message is gone.
	failEdit bool
}

type screenMessage struct {
	text    string
	labels  []string
	data    []string // callback data, by label order
	urls    []string // a URL button's link, "web_app:<link>" for a Mini App's, by label order
	deleted bool
}

func (f *screenTelegram) Call(_ context.Context, url string, req *ta.RequestData) (*ta.Response, error) {
	method := url[strings.LastIndex(url, "/")+1:]
	var p struct {
		MessageID   int    `json:"message_id"`
		Text        string `json:"text"`
		ReplyMarkup *struct {
			InlineKeyboard [][]struct {
				Text         string `json:"text"`
				CallbackData string `json:"callback_data"`
				URL          string `json:"url"`
				WebApp       *struct {
					URL string `json:"url"`
				} `json:"web_app"`
			} `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	_ = json.Unmarshal(req.BodyRaw, &p)
	keyboard := func(m *screenMessage) {
		m.labels, m.data, m.urls = nil, nil, nil
		if p.ReplyMarkup == nil {
			return
		}
		for _, row := range p.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				m.labels = append(m.labels, b.Text)
				m.data = append(m.data, b.CallbackData)
				url := b.URL
				if b.WebApp != nil {
					url = "web_app:" + b.WebApp.URL
				}
				m.urls = append(m.urls, url)
			}
		}
	}
	ok := func(result string) (*ta.Response, error) {
		return &ta.Response{Ok: true, Result: json.RawMessage(result)}, nil
	}
	gone := func() (*ta.Response, error) {
		return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 400, Description: "Bad Request: message to edit not found"}}, nil
	}
	switch method {
	case "sendMessage":
		f.next++
		m := &screenMessage{text: p.Text}
		keyboard(m)
		f.messages[f.next] = m
		f.calls = append(f.calls, fmt.Sprintf("%s #%d", method, f.next))
		return ok(fmt.Sprintf(`{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}`, f.next, usersTestChat))
	case "editMessageText", "editMessageReplyMarkup":
		f.calls = append(f.calls, fmt.Sprintf("%s #%d", method, p.MessageID))
		m := f.messages[p.MessageID]
		if f.failEdit || m == nil || m.deleted {
			return gone()
		}
		if method == "editMessageText" {
			m.text = p.Text
		}
		keyboard(m)
		return ok(fmt.Sprintf(`{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}`, p.MessageID, usersTestChat))
	case "getMe": // the bot's own account: its @username makes invite links (#219)
		f.calls = append(f.calls, method)
		return ok(`{"id":4242,"is_bot":true,"first_name":"Bot","username":"test_bot"}`)
	case "sendDocument":
		// A file stays in the chat but is no screen: it is not numbered.
		f.calls = append(f.calls, method)
		return ok(fmt.Sprintf(`{"message_id":0,"date":0,"chat":{"id":%d,"type":"private"}}`, usersTestChat))
	case "deleteMessage":
		f.calls = append(f.calls, fmt.Sprintf("%s #%d", method, p.MessageID))
		if m := f.messages[p.MessageID]; m != nil {
			m.deleted = true
		}
		return ok("true")
	}
	f.calls = append(f.calls, method)
	return ok("true")
}

// live are the ids of the bot's messages still in the chat, oldest first.
func (f *screenTelegram) live() []int {
	var out []int
	for id := 1; id <= f.next; id++ {
		if m := f.messages[id]; m != nil && !m.deleted {
			out = append(out, id)
		}
	}
	return out
}

// press taps the button labelled label on message id, as Telegram delivers
// it to the bot. It fails when the message has no such button.
func (f *screenTelegram) press(t *testing.T, bot *Tgbot, id int, label string) {
	t.Helper()
	m := f.messages[id]
	if m == nil {
		t.Fatalf("no message #%d", id)
	}
	for i, l := range m.labels {
		if strings.Contains(l, label) {
			bot.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: 1}, Data: m.data[i],
				Message: &telego.Message{MessageID: id, Chat: telego.Chat{ID: usersTestChat}}}, true)
			return
		}
	}
	t.Fatalf("message #%d has no button %q: %q", id, label, m.labels)
}

// withScreenTelegram points the bot at a screenTelegram for the test, with
// Telegram user 1 as the admin.
func withScreenTelegram(t *testing.T) *screenTelegram {
	t.Helper()
	fake := &screenTelegram{messages: map[int]*screenMessage{}}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(fake), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	prevBot, prevRunning, prevAdmins := bot, isRunning, adminIds
	bot, isRunning, adminIds = b, true, []int64{1}
	t.Cleanup(func() { bot, isRunning, adminIds = prevBot, prevRunning, prevAdmins })
	return fake
}

// adminCommand is an admin's command, as Telegram delivers it to the bot.
func adminCommand(bot *Tgbot, text string) {
	bot.answerCommand(&telego.Message{Text: text, Chat: telego.Chat{ID: usersTestChat},
		From: &telego.User{ID: 1, FirstName: "Admin"}}, usersTestChat, true)
}

// TestScreenStartReplacesTheScreen: /start sends the main menu; another
// /start deletes that screen and sends a new one at the bottom.
func TestScreenStartReplacesTheScreen(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	if live := fake.live(); len(live) != 1 || !strings.Contains(strings.Join(fake.messages[live[0]].labels, "|"), "👥 Users") {
		t.Fatalf("after /start: %v %+v", live, fake.messages)
	}
	adminCommand(tg, "/start")
	if live := fake.live(); len(live) != 1 || live[0] != 2 {
		t.Fatalf("after the second /start the chat shows %v, want only the new screen #2 (calls %q)", live, fake.calls)
	}
}

// adminText is an admin's text message, as the bot's text handler takes it:
// the flow the chat waits in gets it.
func adminText(t *testing.T, bot *Tgbot, id int, text string) {
	t.Helper()
	state, waiting := userStates.get(usersTestChat)
	if !waiting {
		t.Fatalf("the chat waits for no text (sending %q)", text)
	}
	bot.answerUsersText(&telego.Message{MessageID: id, Text: text, Chat: telego.Chat{ID: usersTestChat},
		From: &telego.User{ID: 1}}, state)
}

// sent counts the messages the bot sent.
func (f *screenTelegram) sent() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "sendMessage") {
			n++
		}
	}
	return n
}

// TestScreenEditsInPlace: the menu's buttons change the one screen message;
// nothing new appears in the chat.
func TestScreenEditsInPlace(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "👥 Users")
	fake.press(t, tg, 1, "ivan")
	if fake.sent() != 1 || len(fake.live()) != 1 {
		t.Fatalf("the screen should be edited in place: %q", fake.calls)
	}
	if m := fake.messages[1]; !strings.Contains(m.text, "<b>ivan</b>") {
		t.Errorf("the screen shows %q", m.text)
	}
}

// TestScreenDeletesTheSearchText: a search typed on the users list is
// answered on the screen, and the text goes.
func TestScreenDeletesTheSearchText(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "👥 Users")
	adminText(t, tg, 100, "IVAN")
	if !strings.Contains(fake.calls[len(fake.calls)-1], "deleteMessage #100") {
		t.Errorf("the admin's text should be deleted: %q", fake.calls)
	}
	if m := fake.messages[1]; !strings.Contains(m.text, "<b>ivan</b>") || fake.sent() != 1 {
		t.Errorf("the screen shows %q after %q", m.text, fake.calls)
	}
}

// TestScreenBackStack: Back retraces the way, Menu starts it over; the main
// menu has neither.
func TestScreenBackStack(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	fake := withScreenTelegram(t)
	screen := func() *screenMessage { return fake.messages[1] }

	adminCommand(tg, "/start")
	if labels := strings.Join(screen().labels, "|"); strings.Contains(labels, "Back") || strings.Contains(labels, "🏠 Menu") {
		t.Errorf("main menu: %q", labels)
	}
	fake.press(t, tg, 1, "👥 Users")
	fake.press(t, tg, 1, "ivan")
	fake.press(t, tg, 1, "Show subscription")
	if !strings.Contains(screen().text, "Subscription of ivan") {
		t.Fatalf("subscription: %q", screen().text)
	}
	for _, want := range []string{"<b>ivan</b>", "<b>Users</b>", "Main menu"} {
		fake.press(t, tg, 1, "⬅️ Back")
		if !strings.Contains(screen().text, want) {
			t.Errorf("back: want %q, the screen shows %q", want, screen().text)
		}
	}

	fake.press(t, tg, 1, "👥 Users")
	fake.press(t, tg, 1, "ivan")
	fake.press(t, tg, 1, "🏠 Menu")
	if !strings.Contains(screen().text, "Main menu") {
		t.Errorf("menu: %q", screen().text)
	}
	fake.press(t, tg, 1, "👥 Users")
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(screen().text, "Main menu") {
		t.Errorf("Menu should leave nothing to go back to: %q", screen().text)
	}
}

// TestScreenBackFromAConfirmation: a confirmation's way back is the card it
// came from, and the card after a change goes back where the card did.
func TestScreenBackFromAConfirmation(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	fake := withScreenTelegram(t)
	screen := func() *screenMessage { return fake.messages[1] }

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "👥 Users")
	fake.press(t, tg, 1, "ivan")
	fake.press(t, tg, 1, "🗑 Delete")
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(screen().text, "<b>ivan</b>") {
		t.Fatalf("back from the confirmation: %q", screen().text)
	}
	fake.press(t, tg, 1, "⏸ Suspend")
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(screen().text, "<b>Users</b>") {
		t.Errorf("back from the card after a change: %q", screen().text)
	}

	// Deleting the user leaves the list, with no way back to its card.
	fake.press(t, tg, 1, "ivan")
	fake.press(t, tg, 1, "🗑 Delete")
	fake.press(t, tg, 1, "Confirm")
	if !strings.Contains(screen().text, "deleted") {
		t.Fatalf("after the delete: %q", screen().text)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(screen().text, "Main menu") {
		t.Errorf("back after the delete: %q", screen().text)
	}
}

// TestScreenIgnoresOldMessages: a press on a message that is not the chat's
// screen changes nothing.
func TestScreenIgnoresOldMessages(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	adminCommand(tg, "/start")
	fake.calls = nil
	fake.press(t, tg, 1, "👥 Users")
	if len(fake.calls) != 1 || fake.calls[0] != "answerCallbackQuery" {
		t.Errorf("a press on the old screen did %q", fake.calls)
	}
	if !strings.Contains(fake.messages[2].text, "Main menu") {
		t.Errorf("the screen changed: %q", fake.messages[2].text)
	}
}

// TestScreenAfterRestart: with no screen known (the panel restarted), the
// first press shows the main menu on the message pressed, which becomes the
// screen.
func TestScreenAfterRestart(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "👥 Users")
	botScreens.drop(usersTestChat) // the restart
	fake.press(t, tg, 1, "robot")
	if m := fake.messages[1]; !strings.Contains(m.text, "Main menu") || fake.sent() != 1 {
		t.Fatalf("first press after the restart: %q, calls %q", m.text, fake.calls)
	}
	fake.press(t, tg, 1, "👥 Users")
	if m := fake.messages[1]; !strings.Contains(m.text, "<b>Users</b>") {
		t.Errorf("the adopted screen works: %q", m.text)
	}
}

// TestScreenLostMessage: when the screen message is gone, the next view comes
// in a new one.
func TestScreenLostMessage(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "👥 Users")
	fake.failEdit = true
	adminText(t, tg, 100, "nobody")
	if fake.sent() != 2 || !strings.Contains(fake.messages[2].text, "Nobody found") {
		t.Fatalf("calls %q", fake.calls)
	}
	fake.failEdit = false
	fake.press(t, tg, 2, "⬅️ Back")
	if !strings.Contains(fake.messages[2].text, "<b>Users</b>") {
		t.Errorf("the new message is the screen: %q", fake.messages[2].text)
	}
}

// TestScreenMainMenu: the admin's main menu as the prototype lays it out,
// with the incoming requests counted (#221).
func TestScreenMainMenu(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)

	adminCommand(tg, "/help")
	want := []string{"👥 Users", "➕ New user", "📥 Incoming requests (0)", "📋 Inbounds and clients", "🟢 Online", "📊 Reports", "📡 Monitoring",
		"⚙️ Server", "🔧 Admin panel"}
	if got := fake.messages[1].labels; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("main menu:\n got %q\nwant %q", got, want)
	}
	fake.press(t, tg, 1, "Inbounds and clients")
	if m := fake.messages[1]; !strings.Contains(m.text, "Inbounds") || !strings.Contains(strings.Join(m.labels, "|"), "✅ Trojan · de") {
		t.Errorf("inbounds: %q %q", m.text, m.labels)
	}
	fake.press(t, tg, 1, "Trojan · de")
	fake.press(t, tg, 1, "other-de")
	if m := fake.messages[1]; !strings.Contains(m.text, "other-de") || !strings.Contains(strings.Join(m.labels, "|"), "IP Limit") {
		t.Errorf("client card: %q %q", m.text, m.labels)
	}

	initTestBotLocale(t, "ru-RU")
	adminCommand(tg, "/start")
	wantRu := []string{"👥 Пользователи", "➕ Новый пользователь", "📥 Входящие заявки (0)", "📋 Inbounds и клиенты", "🟢 Онлайн", "📊 Отчёты", "📡 Мониторинг",
		"⚙️ Сервер", "🔧 Админка"}
	if got := fake.messages[2].labels; strings.Join(got, "|") != strings.Join(wantRu, "|") {
		t.Errorf("main menu in Russian:\n got %q\nwant %q", got, wantRu)
	}
}

func labelIndex(t *testing.T, m *screenMessage, label string) int {
	t.Helper()
	for i, l := range m.labels {
		if strings.Contains(l, label) {
			return i
		}
	}
	t.Fatalf("no button %q in %q", label, m.labels)
	return 0
}

// TestScreenCommands: /usage, /inbound and /proxy open their view in a new
// screen at the bottom, with the way back to the main menu; /id answers
// directly.
func TestScreenCommands(t *testing.T) {
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	fake := withScreenTelegram(t)
	screen := func() *screenMessage { live := fake.live(); return fake.messages[live[len(live)-1]] }

	cases := []struct{ command, want string }{
		{"/usage ivan-de", "xray email: ivan-de"},
		{"/usage IVAN", "<b>ivan</b>"},
		{"/usage " + ivan.SubId, "<b>ivan</b>"},
		{"/usage nobody", "Nobody found for «nobody»"},
		{"/inbound de", "<b>de (Trojan)</b>"},
		{"/inbound nothing-like-it", "No inbound found"},
	}
	for _, tc := range cases {
		adminCommand(tg, tc.command)
		if live := fake.live(); len(live) != 1 {
			t.Errorf("%s: the chat shows %v", tc.command, live)
		}
		if m := screen(); !strings.Contains(m.text, tc.want) || !strings.Contains(strings.Join(m.labels, "|"), "⬅️ Back|🏠 Menu") {
			t.Errorf("%s: %q %q", tc.command, m.text, m.labels)
		}
	}
	fake.press(t, tg, fake.next, "⬅️ Back")
	if !strings.Contains(screen().text, "Main menu") {
		t.Errorf("back from a command's view: %q", screen().text)
	}

	adminCommand(tg, "/proxy")
	if m := screen(); !strings.Contains(strings.Join(m.labels, "|"), "🏠 Menu") {
		t.Errorf("/proxy: %q %q", m.text, m.labels)
	}

	before := fake.next
	adminCommand(tg, "/id")
	if m := fake.messages[fake.next]; fake.next != before+1 || !strings.Contains(m.text, "1") || len(m.labels) != 0 {
		t.Errorf("/id: %+v", m)
	}
}

// TestScreenCallbacksFitTelegram: every button of the screens keeps its
// callback data within Telegram's 64 bytes, for a user and a client with
// long names too.
func TestScreenCallbacksFitTelegram(t *testing.T) {
	tg := usersBotFixture(t)
	long := mustCreateUser(t, SubUserCreate{Name: strings.Repeat("l", 60), SubId: strings.Repeat("s", 60), InboundIds: []int{1, 2, 5}})
	email := clientByName(t, long, strings.Repeat("l", 60)+"-de").Name

	for _, data := range []string{screenMenuRoute, "usr_l 0", "usr_c " + long.SubId, "usr_sub " + long.SubId,
		"usr_apm " + long.SubId, "usr_rpm " + long.SubId, "usr_rp " + long.SubId + " 2", "usr_del " + long.SubId,
		"usr_f " + long.Name, "s_ibs", "s_ib 2 0", "s_ib 5 0", "s_onl", "client_get_usage " + email, "limit_traffic " + email,
		"limit_traffic_in " + email + " 12 3", "reset_exp " + email, "reset_exp_in " + email + " 0", "ip_limit " + email,
		"ip_limit_in " + email + " 0", "ip_log " + email, "clear_ips " + email, "tg_user " + email, "tgid_remove " + email,
		"reset_traffic " + email, "toggle_enable " + email} {
		reply := screenPressData(t, tg, data)
		if reply.text == "" && reply.keyboard == nil {
			t.Errorf("%q shows nothing: %+v", data, reply)
		}
		sc := &botScreen{route: "x", back: []string{screenMenuRoute}}
		buttons(t, tg.screenKeyboard(sc, reply.keyboard)) // fails on data over 64 bytes
	}
}

// TestScreenTextsInEveryLanguage: every translation file carries the screen
// keys, so no language renders an empty button.
func TestScreenTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Screen map[string]string `toml:"screen"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Screen {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.screen] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.screen] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
