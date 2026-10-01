package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

// linkTelegram stands in for the Bot API in the link broadcast's tests: it
// reads JSON and multipart calls alike (a photo or a file comes multipart),
// records each with its chat, text or caption, buttons and file, and answers
// a chat with the refusals queued for it, in order, before it lets messages
// through.
type linkTelegram struct {
	mu      sync.Mutex
	calls   []linkCall
	refuse  map[int64][]*ta.Error
	nextMsg int
}

type linkCall struct {
	method string
	chat   int64
	msgID  int // the message an edit or deletion names
	text   string
	labels []string
	data   []string
	file   string
	ok     bool
}

func (f *linkTelegram) Call(_ context.Context, url string, req *ta.RequestData) (*ta.Response, error) {
	call := linkCall{method: url[strings.LastIndex(url, "/")+1:]}
	fields := map[string]string{}
	if req.BodyRaw != nil {
		var p map[string]any
		_ = json.Unmarshal(req.BodyRaw, &p)
		for k, v := range p {
			if s, ok := v.(string); ok {
				fields[k] = s
			} else {
				raw, _ := json.Marshal(v)
				fields[k] = string(raw)
			}
		}
	} else if req.BodyStream != nil {
		_, params, _ := mime.ParseMediaType(req.ContentType)
		r := multipart.NewReader(req.BodyStream, params["boundary"])
		for {
			part, err := r.NextPart()
			if err != nil {
				break
			}
			value, _ := io.ReadAll(part)
			if part.FileName() != "" {
				call.file = part.FileName()
				continue
			}
			fields[part.FormName()] = string(value)
		}
	}
	call.chat, _ = strconv.ParseInt(strings.Trim(fields["chat_id"], `"`), 10, 64)
	call.msgID, _ = strconv.Atoi(fields["message_id"])
	call.text = fields["text"] + fields["caption"]
	if markup := fields["reply_markup"]; markup != "" {
		var m struct {
			InlineKeyboard [][]struct {
				Text         string `json:"text"`
				CallbackData string `json:"callback_data"`
			} `json:"inline_keyboard"`
		}
		_ = json.Unmarshal([]byte(markup), &m)
		for _, row := range m.InlineKeyboard {
			for _, b := range row {
				call.labels = append(call.labels, b.Text)
				call.data = append(call.data, b.CallbackData)
			}
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if queue := f.refuse[call.chat]; len(queue) > 0 && strings.HasPrefix(call.method, "send") {
		f.refuse[call.chat] = queue[1:]
		f.calls = append(f.calls, call)
		return &ta.Response{Ok: false, Error: queue[0]}, nil
	}
	call.ok = true
	f.calls = append(f.calls, call)
	switch {
	case strings.HasPrefix(call.method, "answer"), strings.HasPrefix(call.method, "delete"):
		return &ta.Response{Ok: true, Result: json.RawMessage("true")}, nil
	case call.method == "getMe":
		return &ta.Response{Ok: true, Result: json.RawMessage(`{"id":4242,"is_bot":true,"first_name":"Bot","username":"test_bot"}`)}, nil
	}
	id := call.msgID
	if id == 0 {
		f.nextMsg++
		id = f.nextMsg
	}
	return &ta.Response{Ok: true, Result: json.RawMessage(
		fmt.Sprintf(`{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}`, id, call.chat))}, nil
}

// to is what went through to chat (refusals left out).
func (f *linkTelegram) to(chat int64) []linkCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []linkCall
	for _, c := range f.calls {
		if c.chat == chat && c.ok && c.method != "answerCallbackQuery" {
			out = append(out, c)
		}
	}
	return out
}

// toasts are the answers to button presses.
func (f *linkTelegram) toasts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.method == "answerCallbackQuery" {
			out = append(out, c.text)
		}
	}
	return out
}

// lastTo is the last message sent or edited in chat; it fails when none was.
func (f *linkTelegram) lastTo(t *testing.T, chat int64) linkCall {
	t.Helper()
	calls := f.to(chat)
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].method == "sendMessage" || calls[i].method == "editMessageText" {
			return calls[i]
		}
	}
	t.Fatalf("nothing was said in chat %d: %+v", chat, f.calls)
	return linkCall{}
}

// button is the data of the button labelled label; it fails when there is none.
func (c linkCall) button(t *testing.T, label string) string {
	t.Helper()
	for i, l := range c.labels {
		if strings.Contains(l, label) {
			return c.data[i]
		}
	}
	t.Fatalf("no button %q among %q in %q", label, c.labels, c.text)
	return ""
}

// The admins of these tests and their private chats with the bot.
const (
	linkAdmin  = int64(1)
	linkAdmin2 = int64(2)
)

// withLinkTelegram points the bot at a linkTelegram, with two admins; the
// broadcast runs at once, and its pauses are recorded instead of slept.
func withLinkTelegram(t *testing.T) (*linkTelegram, *[]time.Duration) {
	t.Helper()
	fake := &linkTelegram{refuse: map[int64][]*ta.Error{}, nextMsg: 100}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(fake), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	var sleptMu sync.Mutex
	prevBot, prevRunning, prevAdmins := bot, isRunning, adminIds
	prevGo, prevSleep := subLinkGo, subLinkSleep
	bot, isRunning, adminIds = b, true, []int64{linkAdmin, linkAdmin2}
	subLinkGo = func(f func()) { f() }
	subLinkSleep = func(d time.Duration) {
		sleptMu.Lock()
		defer sleptMu.Unlock()
		slept = append(slept, d)
	}
	t.Cleanup(func() {
		bot, isRunning, adminIds = prevBot, prevRunning, prevAdmins
		subLinkGo, subLinkSleep = prevGo, prevSleep
		subLinkWatch.reset()
	})
	return fake, &slept
}

// linkPress is a tap on a button of message msgID in chat, by from, as
// Telegram delivers it.
func linkPress(tg *Tgbot, from, chat int64, msgID int, data string) {
	tg.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: from}, Data: data,
		Message: &telego.Message{MessageID: msgID, Chat: telego.Chat{ID: chat}}}, checkAdmin(from))
}

// linkClock sets the watcher's clock to *now for the test.
func linkClock(t *testing.T, now *time.Time) {
	t.Helper()
	prev := subLinkNow
	subLinkNow = func() time.Time { return *now }
	t.Cleanup(func() { subLinkNow = prev })
}
