package service

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	"github.com/pelletier/go-toml/v2"
)

// The admin's side of requests (#188 points 4–7, #221): «📥 Incoming
// requests (N)» in the main menu, the pending requests, a request's card with
// «✅ Approve», «⚙️ Approve with changes», «❌ Reject» and «🚫 Block», and
// what the applicant is told.

// testApplicant is the account that asks for a subscription in these tests.
const testApplicant = int64(555)

// requestsAdminFixture is the users fixture with the bot on a fakeTelegram
// and Telegram user 1 its admin, the requests' clock at 30.09.2026 12:00, the admin's chat (usersTestChat)
// known as @admin, and testApplicant, @petrov «Petr Petrov», with a request
// «from work».
func requestsAdminFixture(t *testing.T) (*Tgbot, *fakeTelegram, *time.Time, *model.SubRequest) {
	t.Helper()
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	prevAdmins := adminIds
	adminIds = []int64{1} // the admin who presses and types (adminPress, adminText)
	t.Cleanup(func() { adminIds = prevAdmins })
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	requestClock(t, &now)
	for _, a := range []model.TgAccount{{TgId: usersTestChat, Username: "admin"},
		{TgId: testApplicant, Username: "petrov", FirstName: "Petr", LastName: "Petrov"}} {
		if _, err := writeTgAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	return tg, fake, &now, mustRequest(t, testApplicant, "from work")
}

// screenOf is the last view the bot showed in chat — sent or edited in —
// with its buttons' labels and their data.
func (f *fakeTelegram) screenOf(t *testing.T, chat int64) (text string, labels []string, data map[string]string) {
	t.Helper()
	for i := len(f.calls) - 1; i >= 0; i-- {
		c := f.calls[i]
		if c.method != "sendMessage" && c.method != "editMessageText" || fmt.Sprint(c.params["chat_id"]) != fmt.Sprint(float64(chat)) {
			continue
		}
		data = map[string]string{}
		markup, _ := c.params["reply_markup"].(map[string]any)
		rows, _ := markup["inline_keyboard"].([]any)
		for _, row := range rows {
			for _, b := range row.([]any) {
				label := fmt.Sprint(b.(map[string]any)["text"])
				labels = append(labels, label)
				data[label] = fmt.Sprint(b.(map[string]any)["callback_data"])
			}
		}
		text, _ = c.params["text"].(string)
		return text, labels, data
	}
	t.Fatalf("nothing shown in %d:\n%s", chat, f.texts())
	return "", nil, nil
}

// adminTap presses the button of the admin's screen whose label has label.
func adminTap(t *testing.T, tg *Tgbot, fake *fakeTelegram, label string) {
	t.Helper()
	_, labels, data := fake.screenOf(t, usersTestChat)
	for _, l := range labels {
		if strings.Contains(l, label) {
			adminPress(tg, data[l])
			return
		}
	}
	t.Fatalf("the admin's screen has no %q: %q", label, labels)
}

// adminScreen is the text and the labels of the admin's screen.
func adminScreen(t *testing.T, fake *fakeTelegram) (string, string) {
	t.Helper()
	text, labels, _ := fake.screenOf(t, usersTestChat)
	return text, strings.Join(labels, "|")
}

// TestIncomingRequestsInTheMainMenu: the main menu counts the pending
// requests; the list names each by the account and the date, oldest first,
// and opens its card: the account, the name, the tg_id, the comment, the
// date and the past rejections.
func TestIncomingRequestsInTheMainMenu(t *testing.T) {
	tg, fake, now, first := requestsAdminFixture(t)
	requests := &SubRequestService{}
	if err := requests.Reject(first.Id, "@admin", "no places yet"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(8 * 24 * time.Hour)
	mustRequest(t, testApplicant, "<b>me again</b>")
	*now = now.Add(time.Hour)
	mustRequest(t, 601, "")

	adminPress(tg, screenMenuRoute)
	if _, labels := adminScreen(t, fake); !strings.Contains(labels, "📥 Incoming requests (2)") {
		t.Fatalf("main menu: %q", labels)
	}
	adminTap(t, tg, fake, "Incoming requests")
	text, labels := adminScreen(t, fake)
	if !strings.Contains(text, "Incoming requests</b>: 2") || !strings.HasPrefix(labels, "@petrov · 08.10 12:00|601 · 08.10 13:00|") {
		t.Fatalf("the list: %q %q", text, labels)
	}

	adminTap(t, tg, fake, "@petrov")
	text, labels = adminScreen(t, fake)
	for _, want := range []string{"@petrov", "Petr Petrov", "<code>555</code>", "&lt;b&gt;me again&lt;/b&gt;", "08.10.2026 12:00",
		"Past rejections: 1", "30.09.2026 12:00: no places yet"} {
		if !strings.Contains(text, want) {
			t.Errorf("the card lacks %q:\n%s", want, text)
		}
	}
	if labels != "✅ Approve|⚙️ Approve with changes|❌ Reject|🚫 Block|⬅️ Back|🏠 Menu" {
		t.Errorf("the card's buttons: %q", labels)
	}

	// With nothing pending the list says so.
	for _, p := range mustPending(t) {
		if err := requests.Reject(p.Id, "@admin", "no"); err != nil {
			t.Fatal(err)
		}
	}
	adminPress(tg, screenMenuRoute)
	if _, labels := adminScreen(t, fake); !strings.Contains(labels, "📥 Incoming requests (0)") {
		t.Errorf("main menu with none: %q", labels)
	}
	adminTap(t, tg, fake, "Incoming requests")
	if text, _ := adminScreen(t, fake); !strings.Contains(text, "No requests are waiting") {
		t.Errorf("an empty list: %q", text)
	}
}

func mustPending(t *testing.T) []model.SubRequest {
	t.Helper()
	pending, err := (&SubRequestService{}).Pending()
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

// TestApproveFromTheBot: «✅ Approve» makes the user with the request
// defaults — the @nick taken, so petrov-2 — shows its card, marks the
// request approved by the admin, and tells the applicant «Your subscription
// is ready» on a new screen that is «My subscription» now.
func TestApproveFromTheBot(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "petrov"})

	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	adminTap(t, tg, fake, "✅ Approve")
	text, labels := adminScreen(t, fake)
	if !strings.Contains(text, "Request approved: user petrov-2") || !strings.Contains(labels, "Show subscription") {
		t.Fatalf("after the approval: %q %q", text, labels)
	}
	got, _ := (&SubRequestService{}).Get(r.Id)
	if got.Status != model.SubRequestApproved || got.DecidedBy != "@admin" {
		t.Errorf("the request: %+v", got)
	}
	v, err := (&SubUserService{}).Find("petrov-2")
	if err != nil || v.TgId != testApplicant || len(v.Clients) != 3 {
		t.Fatalf("the user: %+v, %v", v, err)
	}

	told := fake.sentTo(testApplicant)
	if len(told) != 1 || !strings.Contains(told[0], "Your subscription is ready") || !strings.Contains(told[0], "My subscription") {
		t.Fatalf("the applicant was told %q", told)
	}
	if _, labels, _ := fake.screenOf(t, testApplicant); !strings.Contains(strings.Join(labels, "|"), "Show subscription") {
		t.Errorf("the applicant's screen: %q", labels)
	}

	// The request is decided: its card says so and offers no decision.
	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	text, labels = adminScreen(t, fake)
	if !strings.Contains(text, "Approved 30.09.2026 12:00 by @admin") || strings.Contains(labels, "Approve") {
		t.Errorf("the decided card: %q %q", text, labels)
	}
	// A stale approve button approves nothing twice.
	adminPress(tg, fmt.Sprintf("%s %d", requestApproveAction, r.Id))
	if text, _ := adminScreen(t, fake); !strings.Contains(text, "no longer waiting") {
		t.Errorf("a second approval: %q", text)
	}
	if len(fake.sentTo(testApplicant)) != 1 {
		t.Error("the applicant was told twice")
	}
}

// TestApproveWithChangesFromTheBot: «⚙️ Approve with changes» opens the
// «New user» review with the name, the request defaults and the applicant's
// Telegram; the admin renames the user and trims the protocols — the
// Telegram step is skipped, the Telegram stays — and «Create» approves.
func TestApproveWithChangesFromTheBot(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)

	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	adminTap(t, tg, fake, "Approve with changes")
	text, labels := adminScreen(t, fake)
	for _, want := range []string{"Request of @petrov", "Check the new user", "Name: petrov", "50 GB", "30 d", "Telegram: 555"} {
		if !strings.Contains(text, want) {
			t.Errorf("the review lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(labels, "✏️ Name") {
		t.Fatalf("the review's buttons: %q", labels)
	}

	adminTap(t, tg, fake, "✏️ Name")
	adminText(t, tg, 100, "petya")
	if text, _ := adminScreen(t, fake); !strings.Contains(text, "Check the new user") || !strings.Contains(text, "Name: petya") {
		t.Fatalf("after the name: %q", text)
	}
	adminTap(t, tg, fake, "Change")
	adminTap(t, tg, fake, "Trojan · de")
	adminTap(t, tg, fake, "AWG · awg")
	adminTap(t, tg, fake, "Next")
	adminTap(t, tg, fake, "Next")
	text, _ = adminScreen(t, fake)
	if !strings.Contains(text, "Check the new user") || !strings.Contains(text, "Telegram: 555") {
		t.Fatalf("after the limits the Telegram step was not skipped: %q", text)
	}
	adminTap(t, tg, fake, "Create")

	v, err := (&SubUserService{}).Find("petya")
	if err != nil || v.TgId != testApplicant || len(v.Clients) != 1 || v.Clients[0].InboundId != 1 {
		t.Fatalf("the user: %+v, %v", v, err)
	}
	if got, _ := (&SubRequestService{}).Get(r.Id); got.Status != model.SubRequestApproved || got.DecidedBy != "@admin" {
		t.Errorf("the request: %+v", got)
	}
	if told := fake.sentTo(testApplicant); len(told) != 1 || !strings.Contains(told[0], "Your subscription is ready") {
		t.Errorf("the applicant was told %q", told)
	}
}

// TestRejectWithAPresetReason: «❌ Reject» offers the preset reasons and
// one's own; a preset rejects the request with it, and the applicant is told
// the reason and the date a new request becomes possible.
func TestRejectWithAPresetReason(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)

	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	adminTap(t, tg, fake, "❌ Reject")
	text, labels := adminScreen(t, fake)
	if !strings.Contains(text, "Reject the request") || !strings.HasPrefix(labels, "No places left|Unknown sender|✏️ Other reason|") {
		t.Fatalf("the reasons: %q %q", text, labels)
	}
	adminTap(t, tg, fake, "Unknown sender")
	if text, _ := adminScreen(t, fake); !strings.Contains(text, "Incoming requests</b>: 0") {
		t.Errorf("after the rejection: %q", text)
	}
	got, _ := (&SubRequestService{}).Get(r.Id)
	if got.Status != model.SubRequestRejected || got.Reason != "Unknown sender" || got.DecidedBy != "@admin" {
		t.Errorf("the request: %+v", got)
	}
	told := fake.sentTo(testApplicant)
	if len(told) != 1 || !strings.Contains(told[0], "Reason: Unknown sender") || !strings.Contains(told[0], "from 07.10.2026 12:00") {
		t.Errorf("the applicant was told %q", told)
	}
}

// TestRejectWithATypedReason: «✏️ Other reason» waits for the admin's
// text: over 200 characters it asks again, then the typed reason goes to the
// applicant.
func TestRejectWithATypedReason(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)

	adminPress(tg, fmt.Sprintf("%s %d", requestRejectRoute, r.Id))
	adminTap(t, tg, fake, "Other reason")
	adminText(t, tg, 100, strings.Repeat("x", 201))
	if text, _ := adminScreen(t, fake); !strings.Contains(text, "longer than 200 characters (201)") {
		t.Fatalf("a long reason: %q", text)
	}
	if got, _ := (&SubRequestService{}).Get(r.Id); got.Status != model.SubRequestPending {
		t.Fatalf("rejected with a long reason: %+v", got)
	}
	adminText(t, tg, 101, "  we are full until spring  ")
	got, _ := (&SubRequestService{}).Get(r.Id)
	if got.Status != model.SubRequestRejected || got.Reason != "we are full until spring" {
		t.Errorf("the request: %+v", got)
	}
	if told := fake.sentTo(testApplicant); len(told) != 1 || !strings.Contains(told[0], "Reason: we are full until spring") {
		t.Errorf("the applicant was told %q", told)
	}
	if _, waiting := userStates.get(usersTestChat); waiting {
		t.Error("the chat still waits for a text")
	}
}

// TestBlockAndUnblockFromTheBot: «🚫 Block» on the card turns the request
// down and blocks the account; the card offers «✅ Unblock». The blocked
// accounts are listed under the requests, and an account's card unblocks it
// as well.
func TestBlockAndUnblockFromTheBot(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)
	requests := &SubRequestService{}

	adminPress(tg, fmt.Sprintf("%s %d", requestCardRoute, r.Id))
	adminTap(t, tg, fake, "🚫 Block")
	text, labels := adminScreen(t, fake)
	if !strings.Contains(text, "account is blocked") || !strings.Contains(labels, "✅ Unblock") || strings.Contains(labels, "Approve") {
		t.Fatalf("the card after the block: %q %q", text, labels)
	}
	if st, _ := requests.Status(testApplicant); !st.Blocked || st.Pending != nil {
		t.Fatalf("status: %+v", st)
	}
	adminTap(t, tg, fake, "✅ Unblock")
	if st, _ := requests.Status(testApplicant); st.Blocked {
		t.Fatal("still blocked")
	}
	if _, labels := adminScreen(t, fake); !strings.Contains(labels, "🚫 Block") {
		t.Errorf("the card after the unblock: %q", labels)
	}

	adminTap(t, tg, fake, "🚫 Block")
	adminPress(tg, requestsListRoute+" 0")
	adminTap(t, tg, fake, "🚫 Blocked (1)")
	adminTap(t, tg, fake, "@petrov")
	text, labels = adminScreen(t, fake)
	if !strings.Contains(text, "<code>555</code>") || !strings.Contains(labels, "✅ Unblock") {
		t.Fatalf("the account's card: %q %q", text, labels)
	}
	adminTap(t, tg, fake, "✅ Unblock")
	if st, _ := requests.Status(testApplicant); st.Blocked {
		t.Error("the account's card did not unblock")
	}
	if _, labels := adminScreen(t, fake); !strings.Contains(labels, "🚫 Block") {
		t.Errorf("the account's card after the unblock: %q", labels)
	}
}

// TestRequestAdminRoutesAreAdminsOnly: the applicant pressing the admin's
// request routes — on their own request, their own account — gets «No
// result», and nothing changes: no approval, no rejection, no unblock.
func TestRequestAdminRoutesAreAdminsOnly(t *testing.T) {
	tg := accessBotFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	requestClock(t, &now)
	mine := mustRequest(t, 601, "")
	if err := (&SubRequestService{}).SetBlocked(usersTestChat, true); err != nil {
		t.Fatal(err)
	}
	fake := withFakeTelegram(t)
	before := botState(t)

	id := fmt.Sprint(mine.Id)
	for _, data := range []string{requestsListRoute + " 0", requestCardRoute + " " + id, requestApproveAction + " " + id,
		requestEditAction + " " + id, requestRejectRoute + " " + id, requestRejectAction + " " + id + " 0",
		requestReasonAction + " " + id, requestBlockAction + " 0 777", requestBlockAction + " 1 601 " + id,
		requestBlockedRoute + " 0", requestAccountRoute + " 777"} {
		if tg.clientMayPress(&telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: data,
			Message: &telego.Message{MessageID: 9, Chat: telego.Chat{ID: usersTestChat}}}) {
			t.Errorf("%q let through", data)
		}
		fake.calls = nil
		nonAdminPress(tg, data)
		if len(fake.calls) != 1 || fake.calls[0].method != "answerCallbackQuery" || fake.calls[0].params["text"] != "❗ No result!" {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
	}
	if after := botState(t); after != before {
		t.Fatalf("the applicant changed the bot's state:\n before %s\n after  %s", before, after)
	}
}

// TestRequestAdminTextsInEveryLanguage: every translation file carries the
// [tgbot.requests] keys and the settings' request defaults, and the Russian
// ones read as the decision's.
func TestRequestAdminTextsInEveryLanguage(t *testing.T) {
	read := func(file string) (bot, settings map[string]string) {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Requests map[string]string `toml:"requests"`
			} `toml:"tgbot"`
			Pages struct {
				Settings map[string]any `toml:"settings"`
			} `toml:"pages"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		settings = map[string]string{}
		for k, v := range doc.Pages.Settings {
			if s, ok := v.(string); ok && strings.HasPrefix(k, "subRequest") {
				settings[k] = s
			}
		}
		return doc.Tgbot.Requests, settings
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
	wantBot, wantSettings := read("translate.en_US.toml")
	if len(wantBot) == 0 || len(wantSettings) == 0 {
		t.Fatal("no [tgbot.requests] or request settings in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		bot, settings := read(e.Name())
		if !slices.Equal(names(bot), names(wantBot)) || !slices.Equal(names(settings), names(wantSettings)) {
			t.Errorf("%s: keys\n got %q %q\nwant %q %q", e.Name(), names(bot), names(settings), names(wantBot), names(wantSettings))
		}
	}
	ru, _ := read("translate.ru_RU.toml")
	for key, part := range map[string]string{"menu": "📥 Входящие заявки", "approve": "✅ Одобрить",
		"approveEdit": "⚙️ Одобрить с изменениями", "reject": "❌ Отклонить", "block": "🚫 Заблокировать",
		"reasonFull": "Нет мест", "reasonUnknown": "Неизвестный отправитель", "ready": "Подписка готова"} {
		if !strings.Contains(ru[key], part) {
			t.Errorf("ru %s = %q, want %q in it", key, ru[key], part)
		}
	}
}

// TestApproveWithChangesOfARequestTakenBack: the applicant cancels while the
// admin edits: «Create» makes no user and says the request no longer waits.
func TestApproveWithChangesOfARequestTakenBack(t *testing.T) {
	tg, fake, _, r := requestsAdminFixture(t)

	adminPress(tg, fmt.Sprintf("%s %d", requestEditAction, r.Id))
	if _, err := (&SubRequestService{}).Cancel(testApplicant); err != nil {
		t.Fatal(err)
	}
	adminTap(t, tg, fake, "Create")
	if text, _ := adminScreen(t, fake); !strings.Contains(text, "⚠️ The request is no longer waiting") || strings.Contains(text, "⚠️ ⚠️") {
		t.Errorf("after the cancel: %q", text)
	}
	if _, err := (&SubUserService{}).Find("petrov"); err == nil {
		t.Error("a user was made for a cancelled request")
	}
	if told := fake.sentTo(testApplicant); len(told) != 0 {
		t.Errorf("the applicant was told %q", told)
	}
}
