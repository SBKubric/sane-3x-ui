package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

// The chat state (#200): every chat's screen, draft and awaited text are
// its own, and the bot's worker goroutines may serve several chats at once.

// stateOf is the state the chat waits in, "" for none.
func stateOf(chatId int64) string {
	state, _ := userStates.get(chatId)
	return state
}

// chatsTelegram stands in for the Bot API when several chats talk to the
// bot at once: like screenTelegram, but the messages are the chats' own and
// the calls may come from many goroutines.
type chatsTelegram struct {
	mu       sync.Mutex
	next     int
	messages map[int]*chatMessage
}

type chatMessage struct {
	chat    int64
	text    string
	labels  []string
	data    []string
	deleted bool
}

func (f *chatsTelegram) Call(_ context.Context, url string, req *ta.RequestData) (*ta.Response, error) {
	method := url[strings.LastIndex(url, "/")+1:]
	var p struct {
		ChatID      int64  `json:"chat_id"`
		MessageID   int    `json:"message_id"`
		Text        string `json:"text"`
		ReplyMarkup *struct {
			InlineKeyboard [][]struct {
				Text         string `json:"text"`
				CallbackData string `json:"callback_data"`
			} `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	_ = json.Unmarshal(req.BodyRaw, &p)
	f.mu.Lock()
	defer f.mu.Unlock()
	keyboard := func(m *chatMessage) {
		m.labels, m.data = nil, nil
		if p.ReplyMarkup == nil {
			return
		}
		for _, row := range p.ReplyMarkup.InlineKeyboard {
			for _, b := range row {
				m.labels = append(m.labels, b.Text)
				m.data = append(m.data, b.CallbackData)
			}
		}
	}
	ok := func(result string) (*ta.Response, error) {
		return &ta.Response{Ok: true, Result: json.RawMessage(result)}, nil
	}
	message := func(id int) (*ta.Response, error) {
		return ok(fmt.Sprintf(`{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}`, id, p.ChatID))
	}
	switch method {
	case "sendMessage":
		f.next++
		m := &chatMessage{chat: p.ChatID, text: p.Text}
		keyboard(m)
		f.messages[f.next] = m
		return message(f.next)
	case "editMessageText", "editMessageReplyMarkup":
		m := f.messages[p.MessageID]
		if m == nil || m.deleted || m.chat != p.ChatID {
			return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 400, Description: "Bad Request: message to edit not found"}}, nil
		}
		if method == "editMessageText" {
			m.text = p.Text
		}
		keyboard(m)
		return message(p.MessageID)
	case "deleteMessage":
		if m := f.messages[p.MessageID]; m != nil && m.chat == p.ChatID {
			m.deleted = true
		}
	}
	return ok("true")
}

// screen is the chat's screen: its latest message still in the chat.
func (f *chatsTelegram) screen(chat int64) (id int, m chatMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := f.next; i > 0; i-- {
		if msg := f.messages[i]; msg != nil && msg.chat == chat && !msg.deleted {
			return i, *msg
		}
	}
	return 0, chatMessage{}
}

// withChatsTelegram points the bot at a chatsTelegram, with admins as the
// bot's admins.
func withChatsTelegram(t *testing.T, admins ...int64) *chatsTelegram {
	t.Helper()
	fake := &chatsTelegram{messages: map[int]*chatMessage{}}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(fake), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	prevBot, prevRunning, prevAdmins := bot, isRunning, adminIds
	bot, isRunning, adminIds = b, true, admins
	t.Cleanup(func() { bot, isRunning, adminIds = prevBot, prevRunning, prevAdmins })
	return fake
}

// chatAdmin is an admin in a private chat of their own, driving the bot the
// way OnReceive hands it the updates: commands, presses and texts.
type chatAdmin struct {
	t    *testing.T
	tg   *Tgbot
	fake *chatsTelegram
	id   int64 // the admin's Telegram id and their chat's
	text int   // the id of the admin's last text
}

func (a *chatAdmin) command(text string) {
	a.tg.answerCommand(&telego.Message{Text: text, Chat: telego.Chat{ID: a.id}, From: &telego.User{ID: a.id}}, a.id, true)
}

// press taps the button labelled label on the chat's screen; false, with the
// test failed, when the screen has none.
func (a *chatAdmin) press(label string) bool {
	id, m := a.fake.screen(a.id)
	for i, l := range m.labels {
		if strings.Contains(l, label) {
			a.tg.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: a.id}, Data: m.data[i],
				Message: &telego.Message{MessageID: id, Chat: telego.Chat{ID: a.id}}}, true)
			return true
		}
	}
	a.t.Errorf("chat %d: the screen has no button %q: %q\n%s", a.id, label, m.labels, m.text)
	return false
}

// say is the admin's text, as the bot's text handler takes it.
func (a *chatAdmin) say(text string) {
	a.text++
	a.tg.answerChatState(&telego.Message{MessageID: 1_000_000*int(a.id) + a.text, Text: text,
		Chat: telego.Chat{ID: a.id}, From: &telego.User{ID: a.id}})
}

// sees fails unless the chat's screen has every want and none of the others.
func (a *chatAdmin) sees(want []string, others ...string) bool {
	_, m := a.fake.screen(a.id)
	ok := true
	for _, w := range want {
		if !strings.Contains(m.text, w) {
			a.t.Errorf("chat %d: the screen lacks %q:\n%s", a.id, w, m.text)
			ok = false
		}
	}
	for _, o := range others {
		if strings.Contains(m.text, o) {
			a.t.Errorf("chat %d: the screen shows the other chat's %q:\n%s", a.id, o, m.text)
			ok = false
		}
	}
	return ok
}

// TestTwoAdminChatsAtOnce: two admins search and create users at the same
// time, as the bot's worker pool serves them; each chat sees only its own
// search, its own draft and its own new user, and the race detector finds
// nothing (#200).
func TestTwoAdminChatsAtOnce(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withChatsTelegram(t, 901, 902)
	mustCreateUser(t, SubUserCreate{Name: "alpha"})
	mustCreateUser(t, SubUserCreate{Name: "bravo"})
	for _, chat := range []int64{901, 902} {
		t.Cleanup(func() { botScreens.drop(chat); usersSessions.drop(chat); userStates.clear(chat) })
	}

	const rounds = 4
	run := func(a *chatAdmin, seeded, prefix, otherSeeded, otherPrefix string) {
		for i := 0; i < rounds; i++ {
			a.command("/start")
			if !a.press("👥 Users") {
				return
			}
			a.say(seeded)
			if !a.sees([]string{seeded}, otherSeeded) || !a.press("🏠 Menu") || !a.press("➕ New user") {
				return
			}
			name := fmt.Sprintf("%s-%d", prefix, i)
			a.say(name)
			for _, label := range []string{"Skip", "Next", "Next", "Later"} {
				if !a.press(label) {
					return
				}
			}
			if !a.sees([]string{"Check the new user", name}, otherPrefix) || !a.press("Create") {
				return
			}
			if !a.sees([]string{"User " + name + " created"}, otherPrefix) {
				return
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		run(&chatAdmin{t: t, tg: tg, fake: fake, id: 901}, "alpha", "anna", "bravo", "boris")
	}()
	go func() {
		defer wg.Done()
		run(&chatAdmin{t: t, tg: tg, fake: fake, id: 902}, "bravo", "boris", "alpha", "anna")
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		t.Fatal("the two chats did not finish")
	}

	for _, name := range []string{"anna-0", "anna-3", "boris-0", "boris-3"} {
		if _, err := (&SubUserService{}).Find(name); err != nil {
			t.Errorf("user %s: %v", name, err)
		}
	}
}

// TestAdminLinkExpiryKeepsTheDialog: the admin link going away five minutes
// later leaves alone the text the chat waits for by then, a new user's name
// here (#200).
func TestAdminLinkExpiryKeepsTheDialog(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)
	var expire []func()
	prev := tgbotDeleteAfter
	tgbotDeleteAfter = func(_ time.Duration, f func()) { expire = append(expire, f) }
	t.Cleanup(func() { tgbotDeleteAfter = prev })

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "🔧 Admin panel")
	fake.press(t, tg, 1, "➕ New user")
	if len(expire) != 1 {
		t.Fatalf("deletion timers: %d", len(expire))
	}
	expire[0]()
	if !fake.messages[2].deleted {
		t.Errorf("the admin link stays: %q", fake.calls)
	}
	adminText(t, tg, 100, "petr")
	wantScreen(t, fake, "step 2/5", "petr")
}
