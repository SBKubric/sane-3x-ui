package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	"github.com/pelletier/go-toml/v2"
)

// The applicant's screens (#188 points 1–3, 8, 12, #220): «No subscription»
// → «📝 Leave a request» → the captcha in a Mini App → a comment or «Skip»
// → «Request sent <date>, awaiting a decision» with «Cancel the request»; a
// rejection with its reason and the date a new request becomes possible; an
// account with a user sees its subscription, and a paused or expired one
// «Write to the admin».

// requestScreenFixture is the users fixture seen by usersTestChat, no admin,
// with no user: the requests' clock at 30.09.2026 12:00, the bot's path
// /third-party/s3cr3t/, and an active edge whose front is up, so the captcha
// is at https://edge.example.com/third-party/s3cr3t/captcha.
func requestScreenFixture(t *testing.T) (*Tgbot, *screenTelegram, *time.Time) {
	t.Helper()
	tg := usersBotFixture(t)
	setSetting(t, tgThirdPartySecretKey, "s3cr3t")
	activeEdge(t, model.ChainHop{Host: "edge.example.com", FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	requestClock(t, &now)
	fake := withScreenTelegram(t)
	return tg, fake, &now
}

// activeEdge puts hop in the registry as the chain's active edge.
func activeEdge(t *testing.T, hop model.ChainHop) {
	t.Helper()
	hop.Name, hop.Role, hop.State, hop.IsActive = "edge-a", chain.RoleEdge, chain.StateJoined, true
	if err := database.GetDB().Create(&hop).Error; err != nil {
		t.Fatal(err)
	}
}

// clientText is a text usersTestChat sends in their private chat, as the
// bot's text handler takes it.
func clientText(tg *Tgbot, id int, text string) bool {
	return tg.answerChatState(&telego.Message{MessageID: id, Text: text, Chat: telego.Chat{ID: usersTestChat, Type: "private"},
		From: &telego.User{ID: usersTestChat, FirstName: "Petr", Username: "petrov"}})
}

func labels(m *screenMessage) string { return strings.Join(m.labels, "|") }

// TestRequestScreensLeadThroughTheCaptcha: the whole way of a request, on
// one screen: «Leave a request» asks for the captcha in a Mini App on the
// edge; its pass brings the comment step to the chat; the comment sends the
// request, the text is deleted, and the screen says it waits.
func TestRequestScreensLeadThroughTheCaptcha(t *testing.T) {
	tg, fake, _ := requestScreenFixture(t)

	clientCommand(tg, "/start")
	m := fake.clientScreen(t)
	if !strings.Contains(m.text, "no subscription") || labels(m) != "📝 Leave a request|🔄 Refresh" {
		t.Fatalf("no subscription: %q %q", m.text, m.labels)
	}

	fake.clientPress(t, tg, 1, "Leave a request")
	m = fake.messages[1]
	if !strings.Contains(m.text, "Check") || labels(m) != "🧩 Pass the check|🔄 I passed the check|⬅️ Back|🏠 Menu" {
		t.Fatalf("the captcha step: %q %q", m.text, m.labels)
	}
	if url := m.urls[labelIndex(t, m, "Pass the check")]; url != "web_app:https://edge.example.com/third-party/s3cr3t/captcha" {
		t.Errorf("the Mini App opens %q", url)
	}
	// Pressed too early, the button shows the same step.
	fake.clientPress(t, tg, 1, "I passed the check")
	if !strings.Contains(fake.messages[1].text, "Check") {
		t.Fatalf("before the captcha: %q", fake.messages[1].text)
	}

	// The captcha passed: the panel brings the comment step to the chat.
	(&TgCaptchaService{}).Pass(usersTestChat)
	tg.requestCaptchaPassed(usersTestChat)
	m = fake.messages[1]
	if !strings.Contains(m.text, "Comment") || labels(m) != "Skip ▶|⬅️ Back|🏠 Menu" || fake.sent() != 1 {
		t.Fatalf("the comment step: %q %q, calls %q", m.text, m.labels, fake.calls)
	}
	if state, _ := userStates.get(usersTestChat); state != requestCommentState {
		t.Fatalf("the chat waits in %q", state)
	}

	if !clientText(tg, 50, "  Petr from work  ") {
		t.Fatal("the comment was not taken")
	}
	m = fake.messages[1]
	if !strings.Contains(m.text, "Request sent 30.09.2026 12:00, awaiting a decision") || labels(m) != "✖ Cancel the request|🔄 Refresh" {
		t.Errorf("after the comment: %q %q", m.text, m.labels)
	}
	if !slices.Contains(fake.calls, "deleteMessage #50") {
		t.Errorf("the comment was not deleted: %q", fake.calls)
	}
	st, err := (&SubRequestService{}).Status(usersTestChat)
	if err != nil || st.Pending == nil || st.Pending.Comment != "Petr from work" {
		t.Fatalf("the request: %+v, %v", st, err)
	}
	if _, waiting := userStates.get(usersTestChat); waiting {
		t.Error("the chat still waits for a text")
	}

	// /start shows the pending request; «Cancel the request» takes it back.
	clientCommand(tg, "/start")
	m = fake.clientScreen(t)
	if !strings.Contains(m.text, "Request sent 30.09.2026 12:00") {
		t.Fatalf("/start while pending: %q", m.text)
	}
	fake.clientPress(t, tg, fake.next, "Cancel the request")
	m = fake.clientScreen(t)
	if !strings.Contains(m.text, "no subscription") || labels(m) != "📝 Leave a request|🔄 Refresh" {
		t.Errorf("after the cancel: %q %q", m.text, m.labels)
	}
	if st, _ := (&SubRequestService{}).Status(usersTestChat); st.Pending != nil {
		t.Errorf("still pending: %+v", st.Pending)
	}
}

// TestRequestScreenSkipAndLimits: «Skip» sends the request without a
// comment; a comment over 200 characters or a non-text message is refused
// on the comment step, which keeps waiting; after an admin reset the
// captcha, the comment leads back to it.
func TestRequestScreenSkipAndLimits(t *testing.T) {
	tg, fake, now := requestScreenFixture(t)
	clientCommand(tg, "/start")
	fake.clientPress(t, tg, 1, "Leave a request")
	(&TgCaptchaService{}).Pass(usersTestChat)
	tg.requestCaptchaPassed(usersTestChat)

	clientText(tg, 50, strings.Repeat("я", 201))
	if m := fake.messages[1]; !strings.Contains(m.text, "longer than 200 characters (201)") || labels(m) != "Skip ▶|⬅️ Back|🏠 Menu" {
		t.Fatalf("201 characters: %q %q", m.text, m.labels)
	}
	clientText(tg, 51, "")
	if m := fake.messages[1]; !strings.Contains(m.text, "Send the comment as text") {
		t.Fatalf("a sticker: %q", m.text)
	}
	if state, _ := userStates.get(usersTestChat); state != requestCommentState {
		t.Fatalf("after the refusals the chat waits in %q", state)
	}

	*now = now.Add(31 * time.Minute)
	if err := (&TgCaptchaService{}).Reset(usersTestChat); err != nil {
		t.Fatal(err)
	}
	clientText(tg, 52, "late")
	if m := fake.messages[1]; !strings.Contains(m.text, "Pass the check again") || !strings.Contains(labels(m), "Pass the check") {
		t.Fatalf("after a reset: %q %q", m.text, m.labels)
	}

	(&TgCaptchaService{}).Pass(usersTestChat)
	tg.requestCaptchaPassed(usersTestChat)
	fake.clientPress(t, tg, 1, "Skip")
	st, _ := (&SubRequestService{}).Status(usersTestChat)
	if m := fake.messages[1]; !strings.Contains(m.text, "Request sent") || st.Pending == nil || st.Pending.Comment != "" {
		t.Errorf("skip: %q, %+v", m.text, st.Pending)
	}
}

// TestRequestScreenRejectedAndBlocked: a rejected request shows its reason
// and when a new one becomes possible, with no request button until then; a
// blocked account is told, with no button either.
func TestRequestScreenRejectedAndBlocked(t *testing.T) {
	tg, fake, now := requestScreenFixture(t)
	requests := &SubRequestService{}
	r := mustRequest(t, usersTestChat, "")
	*now = now.Add(time.Hour)
	if err := requests.Reject(r.Id, "admin", "we do not know you"); err != nil {
		t.Fatal(err)
	}

	clientCommand(tg, "/start")
	m := fake.clientScreen(t)
	for _, want := range []string{"request of 30.09.2026 12:00 was rejected", "we do not know you", "from 07.10.2026 13:00"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("the rejection lacks %q: %q", want, m.text)
		}
	}
	if labels(m) != "🔄 Refresh" {
		t.Errorf("rejection buttons: %q", m.labels)
	}
	*now = now.Add(7 * 24 * time.Hour)
	fake.clientPress(t, tg, fake.next, "Refresh")
	if m := fake.clientScreen(t); labels(m) != "📝 Leave a request|🔄 Refresh" {
		t.Errorf("7 days later: %q %q", m.text, m.labels)
	}

	if err := requests.SetBlocked(usersTestChat, true); err != nil {
		t.Fatal(err)
	}
	fake.clientPress(t, tg, fake.next, "Refresh")
	if m := fake.clientScreen(t); !strings.Contains(m.text, "Requests from this account are not accepted") || labels(m) != "🔄 Refresh" {
		t.Errorf("blocked: %q %q", m.text, m.labels)
	}
	// A request route pressed anyway leads home and makes nothing.
	nonAdminPress(tg, requestNewRoute)
	(&TgCaptchaService{}).Pass(usersTestChat)
	tg.requestCaptchaPassed(usersTestChat)
	nonAdminPress(tg, requestSkipAction)
	if st, _ := requests.Status(usersTestChat); st.Pending != nil {
		t.Errorf("a blocked account made a request: %+v", st.Pending)
	}
}

// TestRequestScreenCaptchaNeedsHTTPS: Telegram opens a Mini App on https
// only. The address is the active edge's — its front, or its own port when
// that is https — never the subscription URL; with no https there, the step
// says the check is unavailable instead of a button Telegram would refuse.
func TestRequestScreenCaptchaNeedsHTTPS(t *testing.T) {
	tg, fake, _ := requestScreenFixture(t)
	setSetting(t, "subURI", "https://subs.example.com/sub/") // not where the captcha is
	for _, c := range []struct {
		name string
		hop  model.ChainHop
		want string
	}{
		{"front", model.ChainHop{FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"},
			"web_app:https://edge.example.com/third-party/s3cr3t/captcha"},
		{"https sub port", model.ChainHop{FrontMode: chain.FrontOff, SubPort: 2096, SubScheme: "https"},
			"web_app:https://edge.example.com:2096/third-party/s3cr3t/captcha"},
		{"http sub port", model.ChainHop{FrontMode: chain.FrontOff, SubPort: 2096, SubScheme: "http"}, ""},
	} {
		if err := database.GetDB().Where("1 = 1").Delete(&model.ChainHop{}).Error; err != nil {
			t.Fatal(err)
		}
		c.hop.Host = "edge.example.com"
		activeEdge(t, c.hop)
		clientCommand(tg, "/start")
		fake.clientPress(t, tg, fake.next, "Leave a request")
		m := fake.clientScreen(t)
		if c.want == "" {
			if !strings.Contains(m.text, "check is unavailable") || strings.Contains(labels(m), "Pass the check") {
				t.Errorf("%s: %q %q", c.name, m.text, m.labels)
			}
			continue
		}
		if url := m.urls[labelIndex(t, m, "Pass the check")]; url != c.want {
			t.Errorf("%s: the Mini App opens %q, want %q", c.name, url, c.want)
		}
	}
}

// TestRequestScreenForAccountWithAUser (#188 point 8): an account that has a
// user never sees the request button — its subscription instead — and a
// paused or expired subscription shows its state with «Write to the admin»,
// the Support-Url of the settings.
func TestRequestScreenForAccountWithAUser(t *testing.T) {
	tg, fake, _ := requestScreenFixture(t)
	setSetting(t, "subSupportUrl", "https://t.me/e2e_admin")
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1}})

	clientCommand(tg, "/start")
	m := fake.clientScreen(t)
	if labels(m) != "🔗 Show subscription|📄 My configs|🔄 Refresh" {
		t.Fatalf("active: %q %q", m.text, m.labels)
	}
	nonAdminPress(tg, requestNewRoute)
	if m := fake.clientScreen(t); !strings.Contains(m.text, "My subscription") {
		t.Errorf("the request route for an account with a user: %q", m.text)
	}

	if _, err := (&SubUserService{}).SetEnable(ivan.SubId, false); err != nil {
		t.Fatal(err)
	}
	clientCommand(tg, "/start")
	m = fake.clientScreen(t)
	if !strings.Contains(m.text, "⏸ paused") || labels(m) != "🔗 Show subscription|📄 My configs|✉️ Write to the admin|🔄 Refresh" ||
		m.urls[labelIndex(t, m, "Write to the admin")] != "https://t.me/e2e_admin" {
		t.Errorf("paused: %q %q %q", m.text, m.labels, m.urls)
	}

	if err := (&SubUserService{}).Delete(ivan.SubId); err != nil {
		t.Fatal(err)
	}
	mustCreateUser(t, SubUserCreate{Name: "anna", TgId: usersTestChat, InboundIds: []int{1},
		SubUserParams: SubUserParams{ExpiryTime: time.Now().Add(-24 * time.Hour).UnixMilli()}})
	clientCommand(tg, "/start")
	m = fake.clientScreen(t)
	if !strings.Contains(m.text, "⌛ expired") || !strings.Contains(labels(m), "Write to the admin") {
		t.Errorf("expired: %q %q", m.text, m.labels)
	}

	// Without a Support-Url there is nobody to write to: no button.
	setSetting(t, "subSupportUrl", "")
	clientCommand(tg, "/start")
	if m := fake.clientScreen(t); strings.Contains(labels(m), "Write to the admin") {
		t.Errorf("no Support-Url: %q", m.labels)
	}
}

// TestRequestNotifiesTheChannel: a new request is told to the notification
// channel as text — who and the comment — and no buttons (#221 reviews it
// in the bot).
func TestRequestNotifiesTheChannel(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	requestClock(t, &now)
	if _, err := writeTgAccount(model.TgAccount{TgId: usersTestChat, Username: "petrov", FirstName: "Petr"}); err != nil {
		t.Fatal(err)
	}
	(&TgCaptchaService{}).Pass(usersTestChat)
	tg.requestCaptchaPassed(usersTestChat)
	clientText(tg, 50, "<b>hi</b>")

	got := fake.sentTo(testNotifyChannel)
	if len(got) != 1 || !strings.Contains(got[0], "📥 New subscription request from @petrov") ||
		!strings.Contains(got[0], "&lt;b&gt;hi&lt;/b&gt;") {
		t.Fatalf("the channel got %q", got)
	}
	for _, c := range fake.calls {
		if c.method == "sendMessage" && c.params["chat_id"] == testNotifyChannel && c.params["reply_markup"] != nil {
			t.Error("the channel's message has buttons")
		}
	}
}

// TestRequestExpiryTellsThePerson: the job expires a request nobody decided
// on in 14 days and tells the person, once.
func TestRequestExpiryTellsThePerson(t *testing.T) {
	tg, fake := notifyBotFixture(t, "")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	requestClock(t, &now)
	mustRequest(t, usersTestChat, "")
	now = now.Add(14*24*time.Hour + time.Minute)

	tg.ExpireSubRequests()
	tg.ExpireSubRequests()
	got := fake.sentTo(usersTestChat)
	if len(got) != 1 || !strings.Contains(got[0], "request of 30.09.2026 12:00 expired") {
		t.Fatalf("the person got %q", got)
	}
}

// TestRequestTextsInEveryLanguage: every translation file carries the
// [tgbot.request] keys, and the Russian ones read as the decision's.
func TestRequestTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) map[string]string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Request map[string]string `toml:"request"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return doc.Tgbot.Request
	}
	names := func(m map[string]string) []string {
		var out []string
		for k, v := range m {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := names(keys("translate.en_US.toml"))
	if len(want) == 0 {
		t.Fatal("no [tgbot.request] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := names(keys(e.Name())); !slices.Equal(got, want) {
			t.Errorf("%s: keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
	ru := keys("translate.ru_RU.toml")
	for key, part := range map[string]string{"leave": "📝 Оставить заявку", "captchaButton": "Пройти проверку",
		"pending": "Заявка отправлена {{ .Date }}, ждёт решения", "cancel": "Отменить заявку", "skip": "Пропустить",
		"writeAdmin": "Написать админу"} {
		if !strings.Contains(ru[key], part) {
			t.Errorf("ru %s = %q, want %q in it", key, ru[key], part)
		}
	}
}
