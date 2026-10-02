package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	"github.com/pelletier/go-toml/v2"
)

// The client's side of the bot (#194): the screens of someone who is no
// admin — «My subscription», «My configs», the pick among several users, «No
// subscription» — and the old client buttons that now open them.

// mysubExpiry is the expiry of the test users: 1 November 2031.
var mysubExpiry = time.Date(2031, 11, 1, 12, 0, 0, 0, time.Local).UnixMilli()

// clientPress taps, as usersTestChat — no admin — the button labelled label
// on message id.
func (f *screenTelegram) clientPress(t *testing.T, bot *Tgbot, id int, label string) {
	t.Helper()
	m := f.messages[id]
	if m == nil {
		t.Fatalf("no message #%d", id)
	}
	for i, l := range m.labels {
		if strings.Contains(l, label) {
			bot.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: usersTestChat}, Data: m.data[i],
				Message: &telego.Message{MessageID: id, Chat: telego.Chat{ID: usersTestChat}}}, checkAdmin(usersTestChat))
			return
		}
	}
	t.Fatalf("message #%d has no button %q: %q", id, label, m.labels)
}

// clientScreen is the chat's newest message still there: the client's screen.
func (f *screenTelegram) clientScreen(t *testing.T) *screenMessage {
	t.Helper()
	live := f.live()
	if len(live) == 0 {
		t.Fatal("the chat is empty")
	}
	return f.messages[live[len(live)-1]]
}

// legacyUserOf creates the user of req and then writes tgId onto its xray
// clients behind the service's back: the legacy state where one Telegram ID
// is on clients of several users, which Create now refuses (1:1, #178).
func legacyUserOf(t *testing.T, req SubUserCreate, tgId int64) *SubUserView {
	t.Helper()
	v := mustCreateUser(t, req)
	db := database.GetDB()
	for _, c := range v.Clients {
		if c.Kind != SubUserClientXray {
			continue
		}
		var ib model.Inbound
		if err := db.First(&ib, c.InboundId).Error; err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
			t.Fatal(err)
		}
		for _, raw := range settings["clients"].([]any) {
			if client := raw.(map[string]any); client["email"] == c.Name {
				client["tgId"] = tgId
			}
		}
		out, _ := json.MarshalIndent(settings, "", "  ")
		if err := db.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("settings", string(out)).Error; err != nil {
			t.Fatal(err)
		}
	}
	return v
}

// TestMySubscriptionScreen: /start from someone whose clients carry their
// Telegram ID shows «My subscription»: on or paused, the expiry, the
// protocols (the traffic is on «📄 My configs», #247) — and only the
// client's buttons.
func TestMySubscriptionScreen(t *testing.T) {
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1, 5},
		SubUserParams: SubUserParams{TotalGB: 50 << 30, ExpiryTime: mysubExpiry}})
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	m := fake.clientScreen(t)
	for _, want := range []string{"My subscription", "ivan", "🟢 active", "until 01.11.2031", "VLESS+AWG"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("the screen lacks %q:\n%s", want, m.text)
		}
	}
	if got := strings.Join(m.labels, "|"); got != "🔗 Show subscription|📄 My configs|🔄 Refresh" {
		t.Errorf("buttons: %q", got)
	}

	if _, err := (&SubUserService{}).SetEnable(ivan.SubId, false); err != nil {
		t.Fatal(err)
	}
	fake.clientPress(t, tg, 1, "Refresh")
	if m := fake.clientScreen(t); !strings.Contains(m.text, "⏸ paused") || fake.sent() != 1 {
		t.Errorf("after the refresh: %q, calls %q", m.text, fake.calls)
	}
}

// documents counts the files the bot sent.
func (f *screenTelegram) documents() int {
	n := 0
	for _, c := range f.calls {
		if c == "sendDocument" {
			n++
		}
	}
	return n
}

// TestMySubscriptionShowAndConfigs: «Show subscription» shows the link on the
// screen and sends its QR as a file; «My configs» lists the user's clients,
// and a tunnel client's button sends its .conf and QR; Back comes home.
func TestMySubscriptionShowAndConfigs(t *testing.T) {
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1, 5}})
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	fake.clientPress(t, tg, 1, "Show subscription")
	m := fake.messages[1]
	if !strings.Contains(m.text, "/sub/"+ivan.SubId) || !strings.Contains(m.text, "Hiddify") || fake.documents() != 1 {
		t.Errorf("subscription: %q, calls %q", m.text, fake.calls)
	}
	if got := strings.Join(m.labels, "|"); got != "⬅️ Back|🏠 Menu" {
		t.Errorf("subscription buttons: %q", got)
	}
	fake.clientPress(t, tg, 1, "Back")
	if !strings.Contains(fake.messages[1].text, "My subscription") {
		t.Fatalf("back: %q", fake.messages[1].text)
	}

	awg := clientByName(t, ivan, "ivan-awg")
	fake.clientPress(t, tg, 1, "My configs")
	m = fake.messages[1]
	for _, want := range []string{"My configs", "Total limit: unlimited", "VLESS · ivan-NL-Amsterdam-1", "AmneziaWG · ivan-awg"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("configs lack %q:\n%s", want, m.text)
		}
	}
	if got := strings.Join(m.labels, "|"); got != "📄 AWG · ivan-awg: .conf and QR|🔗 Links and QR codes|⬅️ Back|🏠 Menu" {
		t.Errorf("configs buttons: %q", got)
	}
	if data := m.data[labelIndex(t, m, ".conf")]; data != mysubTunnelAction+" "+awg.Key {
		t.Errorf("the .conf button carries %q", data)
	}
	before := fake.documents()
	fake.clientPress(t, tg, 1, ".conf and QR")
	if got := fake.documents() - before; got != 2 || !strings.Contains(fake.messages[1].text, "My configs") || fake.sent() != 1 {
		t.Errorf("the .conf button sent %d files, calls %q", got, fake.calls)
	}

	// The links button sends the xray links and their QRs, as an xray
	// client's card does.
	reply := tg.mysubRoute(usersTestChat, mysubLinksAction+" "+ivan.SubId)
	if reply.after == nil || reply.text != "" {
		t.Errorf("links: %+v", reply)
	}
}

// sharedTelegram gives the user under key the Telegram ID tgId behind the
// service's back, on a database where the unique index on sub_users.tg_id
// could not be made (its post-migrate step failed): the one way two users
// still share an account.
func sharedTelegram(t *testing.T, key string, tgId int64) {
	t.Helper()
	db := database.GetDB()
	if err := db.Exec("DROP INDEX IF EXISTS idx_sub_users_tg_id").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SubUser{}).Where("sub_id = ?", key).Update("tg_id", tgId).Error; err != nil {
		t.Fatal(err)
	}
}

// TestMySubscriptionReadsTheUserOnly (#186): the account's subscription is
// the user whose Telegram ID it is. Clients of another user that carry the
// ID (legacy data, a conflict for the admin) do not open that user.
func TestMySubscriptionReadsTheUserOnly(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1}})
	petr := legacyUserOf(t, SubUserCreate{Name: "petr", InboundIds: []int{2}}, usersTestChat)
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	if m := fake.messages[1]; !strings.Contains(m.text, "My subscription</b> · ivan") || strings.Contains(m.text, "petr") {
		t.Fatalf("home: %q %q", m.text, m.labels)
	}
	if tg.clientMayPress(&telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: mysubSubRoute + " " + petr.SubId}) {
		t.Error("petr's subscription opens for the account of ivan")
	}
	if v := mustGetUser(t, petr.SubId); v.TgId != 0 || !v.TgConflict {
		t.Errorf("petr: tgId %d conflict %v", v.TgId, v.TgConflict)
	}
}

// TestMySubscriptionSeveralUsers: an account that is the Telegram ID of
// several users (a database without the unique index) gets the pick among
// them; each opens its own «My subscription», and Back leads to the pick.
func TestMySubscriptionSeveralUsers(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1}})
	petr := mustCreateUser(t, SubUserCreate{Name: "petr", InboundIds: []int{2}})
	sharedTelegram(t, petr.SubId, usersTestChat)
	mustCreateUser(t, SubUserCreate{Name: "anna", TgId: 555, InboundIds: []int{1}})
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	m := fake.messages[1]
	if !strings.Contains(m.text, "Your subscriptions") || len(m.labels) != 2 ||
		!strings.HasPrefix(m.labels[0], "🟢 ivan · VLESS") || !strings.HasPrefix(m.labels[1], "🟢 petr · Trojan") {
		t.Fatalf("the pick: %q %q", m.text, m.labels)
	}
	fake.clientPress(t, tg, 1, "petr")
	m = fake.messages[1]
	if !strings.Contains(m.text, "My subscription</b> · petr") || !strings.Contains(m.text, "Trojan") {
		t.Errorf("petr: %q", m.text)
	}
	if data := m.data[labelIndex(t, m, "Refresh")]; data != mysubUserRoute+" "+petr.SubId {
		t.Errorf("petr's refresh carries %q", data)
	}
	fake.clientPress(t, tg, 1, "Back")
	if !strings.Contains(fake.messages[1].text, "Your subscriptions") {
		t.Errorf("back: %q", fake.messages[1].text)
	}
}

// TestMySubscriptionNone: someone whose Telegram ID no client carries gets
// «No subscription» with their ID and the request button (#220).
func TestMySubscriptionNone(t *testing.T) {
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)

	clientCommand(tg, "/start")
	m := fake.messages[1]
	if !strings.Contains(m.text, "no subscription") || !strings.Contains(m.text, "<code>777</code>") {
		t.Errorf("no subscription: %q", m.text)
	}
	if got := strings.Join(m.labels, "|"); got != "📝 Leave a request|🔄 Refresh" {
		t.Errorf("buttons: %q", got)
	}

	// Once the admin links them, Refresh shows the subscription.
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1}})
	fake.clientPress(t, tg, 1, "Refresh")
	if !strings.Contains(fake.messages[1].text, "My subscription") {
		t.Errorf("after the refresh: %q", fake.messages[1].text)
	}
}

// TestMySubscriptionOldButtons: the buttons of the old client menu, still in
// the chats, open the new screens in a new screen at the bottom, the old
// screen gone: usage and commands the main menu, the subscription links
// «Show subscription», the individual and QR links «My configs» — with a
// client's email, that client's user's.
func TestMySubscriptionOldButtons(t *testing.T) {
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1}})
	email := ivan.Clients[0].Name
	fake := withScreenTelegram(t)

	// The old menu, as SendAnswer sent it, and the old link list.
	fake.next = 1
	fake.messages[1] = &screenMessage{labels: []string{"Get Usage", "Commands", "Subscription", "Individual links", "QR Code", email},
		data: []string{"client_traffic", "client_commands", "client_sub_links", "client_individual_links", "client_qr_links",
			"client_qr_links " + email}}

	for _, c := range []struct{ label, want string }{
		{"Get Usage", "My subscription"},
		{"Commands", "My subscription"},
		{"Subscription", "/sub/" + ivan.SubId},
		{"Individual links", "My configs"},
		{"QR Code", "My configs"},
		{email, "My configs"},
	} {
		screens := fake.live()
		fake.clientPress(t, tg, 1, c.label)
		live := fake.live()
		m := fake.messages[live[len(live)-1]]
		if !strings.Contains(m.text, c.want) {
			t.Errorf("%s: %q", c.label, m.text)
		}
		if len(screens) > 1 && slices.Contains(live, screens[len(screens)-1]) {
			t.Errorf("%s: the old screen #%d is still there: %v", c.label, screens[len(screens)-1], live)
		}
		if !slices.Contains(live, 1) {
			t.Fatalf("%s: the old menu is gone", c.label)
		}
	}
	fake.clientPress(t, tg, fake.next, "Back")
	if m := fake.messages[fake.next]; !strings.Contains(m.text, "My subscription") {
		t.Errorf("back from an old button's screen: %q", m.text)
	}
}

// TestMySubscriptionCallbacksFitTelegram: the client's buttons keep their
// callback data within Telegram's 64 bytes for a long subId too, and the
// access list lets every one of them through.
func TestMySubscriptionCallbacksFitTelegram(t *testing.T) {
	tg := usersBotFixture(t)
	long := mustCreateUser(t, SubUserCreate{Name: strings.Repeat("l", 60), SubId: strings.Repeat("s", 60), TgId: usersTestChat,
		InboundIds: []int{1, 5}})
	legacyUserOf(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}}, usersTestChat)

	for _, reply := range []screenReply{tg.mysubHome(usersTestChat), tg.mysubRoute(usersTestChat, mysubUserRoute+" "+long.SubId),
		tg.mysubRoute(usersTestChat, mysubConfigsRoute+" "+long.SubId)} {
		bs := buttons(t, reply.keyboard) // fails on data over 64 bytes
		if len(bs) == 0 {
			t.Errorf("no buttons: %+v", reply)
		}
		for _, b := range bs {
			q := &telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: b.CallbackData}
			if !tg.clientMayPress(q) {
				t.Errorf("%q (%s) refused", b.Text, b.CallbackData)
			}
		}
	}
}

// TestMySubscriptionTextsInEveryLanguage: every translation file carries the
// client screens' keys, and the Russian ones read as the prototype.
func TestMySubscriptionTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Mysub map[string]string `toml:"mysub"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Mysub {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.mysub] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.mysub] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}

	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1, 5},
		SubUserParams: SubUserParams{TotalGB: 50 << 30, ExpiryTime: mysubExpiry}})
	initTestBotLocale(t, "ru-RU")
	home := tg.mysubHome(usersTestChat)
	for _, want := range []string{"Моя подписка", "Подписка: 🟢 активна", "Срок: до 01.11.2031", "Протоколы: VLESS+AWG"} {
		if !strings.Contains(home.text, want) {
			t.Errorf("ru lacks %q:\n%s", want, home.text)
		}
	}
	var labels []string
	for _, b := range buttons(t, home.keyboard) {
		labels = append(labels, b.Text)
	}
	if got := strings.Join(labels, "|"); got != "🔗 Show subscription|📄 Мои конфиги|🔄 Обновить" {
		t.Errorf("ru buttons: %q", got)
	}
	if none := tg.mysubNone(1); !strings.Contains(none.text, "У вас пока нет подписки") {
		t.Errorf("ru none: %q", none.text)
	}
}
