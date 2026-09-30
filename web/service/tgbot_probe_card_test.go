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
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/pelletier/go-toml/v2"
)

// probeBotFixture is usersBotFixture plus monitoring's probe accounts: the
// xray probe "probe-4" in vless inbound "probe-home" (4), online now, with
// traffic and an IP log, and the AmneziaWG probe peer
// "probe-awg-ams-1-direct", last seen 2026-09-01 10:00 from 203.0.113.7. Both
// carry the probe subId.
func probeBotFixture(t *testing.T) *Tgbot {
	t.Helper()
	bot := usersBotFixture(t)
	setSetting(t, "monProbeSubId", "s-probe")
	usersInbound(t, 4, model.VLESS, "probe-home",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000004", Email: "probe-4", SubID: "s-probe", Enable: true, Comment: ProbeComment})
	db := database.GetDB()
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", "probe-4").
		Updates(map[string]any{"up": 3 << 20, "down": 5 << 20, "last_online": time.Now().UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.InboundClientIps{ClientEmail: "probe-4",
		Ips: `[{"ip":"198.51.100.1","timestamp":1700000000},{"ip":"198.51.100.2","timestamp":1700000500}]`}).Error; err != nil {
		t.Fatal(err)
	}
	peer := awgPeer(t, 40, "probe-awg-ams-1-direct", "s-probe")
	seen := time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local)
	if err := db.Model(peer).Updates(map[string]any{"upload": 7 << 20, "download": 11 << 20,
		"last_online": seen.UnixMilli(), "last_ip": "203.0.113.7"}).Error; err != nil {
		t.Fatal(err)
	}
	return bot
}

// probePress runs a button of the probe card and fails when nothing
// handles it.
func probePress(t *testing.T, bot *Tgbot, data string) usersReply {
	t.Helper()
	reply, ok := bot.probeCallback(data)
	if !ok {
		t.Fatalf("callback %q not handled", data)
	}
	return reply
}

// TestProbeCardIsReadOnly: an xray probe's card shows its traffic, that it is
// online, its last IP, its inbound and the user monitoring, and offers
// nothing but refresh and the way back to monitoring.
func TestProbeCardIsReadOnly(t *testing.T) {
	bot := probeBotFixture(t)

	reply := probePress(t, bot, "prb_c probe-4")
	for _, want := range []string{"<b>probe-4</b>", "read only", "probe-home (vless)", "User: monitoring",
		"🟢 Online", "Last IP: 198.51.100.2", "↑3.00MB", "↓5.00MB", "↑↓8.00MB"} {
		if !strings.Contains(reply.text, want) {
			t.Errorf("card lacks %q:\n%s", want, reply.text)
		}
	}
	want := []string{"🔄 Refresh"}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("buttons:\n got %q\nwant %q", got, want)
	}
	if refresh := button(t, reply.keyboard, "Refresh"); refresh != "prb_c probe-4" || reply.route != refresh {
		t.Errorf("refresh = %q, route %q", refresh, reply.route)
	}
}

// TestProbeCardOfAnAwgPeer: an AmneziaWG probe peer's card is read off the
// tunnel table: offline since its last handshake, its endpoint as the last
// IP, the AmneziaWG inbound, and the same two buttons.
func TestProbeCardOfAnAwgPeer(t *testing.T) {
	bot := probeBotFixture(t)

	reply := probePress(t, bot, "prb_c probe-awg-ams-1-direct")
	for _, want := range []string{"<b>probe-awg-ams-1-direct</b>", "awg (amneziawg)", "User: monitoring",
		"🔴 Offline", "Last online: 2026-09-01 10:00:00", "Last IP: 203.0.113.7", "↑7.00MB", "↓11.00MB"} {
		if !strings.Contains(reply.text, want) {
			t.Errorf("card lacks %q:\n%s", want, reply.text)
		}
	}
	want := []string{"🔄 Refresh"}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("buttons:\n got %q\nwant %q", got, want)
	}
}

// TestProbeCardOfNoProbe: a name that is no probe account gets no card.
func TestProbeCardOfNoProbe(t *testing.T) {
	bot := probeBotFixture(t)
	for _, email := range []string{"probe-99", "other-nl"} {
		reply := probePress(t, bot, "prb_c "+email)
		if reply.keyboard != nil || !strings.Contains(reply.text, email) {
			t.Errorf("%s: %+v", email, reply)
		}
	}
}

// probeMutatingCallbacks are the buttons of the upstream client card and of
// the users flows that change a client, and the screens that lead to them,
// as a crafted callback would carry them for a probe.
var probeMutatingCallbacks = []string{
	"reset_traffic %s", "reset_traffic_c %s",
	"limit_traffic %s", "limit_traffic_c %s 5", "limit_traffic_in %s 0 1",
	"reset_exp %s", "reset_exp_c %s 7", "reset_exp_in %s 0 1",
	"ip_limit %s", "ip_limit_c %s 2", "ip_limit_in %s 0 1",
	"ip_log %s", "ips_refresh %s", "ips_cancel %s", "clear_ips %s", "clear_ips_c %s",
	"tg_user %s", "tgid_refresh %s", "tgid_cancel %s", "tgid_remove %s", "tgid_remove_c %s",
	"toggle_enable %s", "toggle_enable_c %s",
	"client_links %s", "usr_as %s",
}

// TestProbeCardRefusesMutatingCallbacks: a crafted button that would change a
// probe — or open a screen that would — is refused with the read-only card,
// whatever the case of the name, for xray probes and AmneziaWG peers alike.
func TestProbeCardRefusesMutatingCallbacks(t *testing.T) {
	bot := probeBotFixture(t)
	for _, email := range []string{"probe-4", "Probe-4", "probe-awg-ams-1-direct"} {
		for _, format := range probeMutatingCallbacks {
			data := fmt.Sprintf(format, email)
			reply, ok := bot.probeCallback(data)
			if !ok {
				t.Errorf("%q: not refused", data)
				continue
			}
			if !strings.Contains(reply.toast, "read only") || !strings.Contains(reply.text, "Probe account: read only") {
				t.Errorf("%q: %+v", data, reply)
			}
			if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != "🔄 Refresh" {
				t.Errorf("%q: buttons %q", data, got)
			}
		}
	}
}

// TestProbeCardTakesOverTheClientCard: the upstream client card's own
// buttons — open, refresh, cancel — show the probe's card instead; its links
// stay upstream's, and other clients are none of the probe card's business.
func TestProbeCardTakesOverTheClientCard(t *testing.T) {
	bot := probeBotFixture(t)
	for _, data := range []string{"client_get_usage probe-4", "client_refresh probe-4", "client_cancel probe-4"} {
		reply := probePress(t, bot, data)
		if reply.route != "prb_c probe-4" || !strings.Contains(reply.text, "<b>probe-4</b>") {
			t.Errorf("%q: %+v", data, reply)
		}
	}
	for _, data := range []string{"client_sub_links probe-4", "client_qr_links probe-4", "client_individual_links probe-4",
		"toggle_enable_c other-nl", "usr_c " + model.SubUserMonitoringKey, "get_clients 4"} {
		if _, ok := bot.probeCallback(data); ok {
			t.Errorf("%q: taken by the probe card", data)
		}
	}
}

// fakeTelegram stands in for the Bot API: it records every call and answers
// it as Telegram would, true for answers and deletions, a message otherwise.
type fakeTelegram struct {
	calls []fakeTelegramCall
}

type fakeTelegramCall struct {
	method string
	params map[string]any
}

func (f *fakeTelegram) Call(_ context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
	call := fakeTelegramCall{method: url[strings.LastIndex(url, "/")+1:]}
	_ = json.Unmarshal(data.BodyRaw, &call.params)
	f.calls = append(f.calls, call)
	if strings.HasPrefix(call.method, "answer") || strings.HasPrefix(call.method, "delete") {
		return &ta.Response{Ok: true, Result: json.RawMessage("true")}, nil
	}
	if call.method == "getMe" { // the bot's own account: its @username makes invite links (#219)
		return &ta.Response{Ok: true, Result: json.RawMessage(`{"id":4242,"is_bot":true,"first_name":"Bot","username":"test_bot"}`)}, nil
	}
	return &ta.Response{Ok: true, Result: json.RawMessage(
		fmt.Sprintf(`{"message_id":9,"date":0,"chat":{"id":%d,"type":"private"}}`, usersTestChat))}, nil
}

// texts are the texts the bot sent, edited or answered with.
func (f *fakeTelegram) texts() string {
	var b strings.Builder
	for _, c := range f.calls {
		fmt.Fprintf(&b, "%s: %v\n", c.method, c.params["text"])
	}
	return b.String()
}

// withFakeTelegram points the bot at a fakeTelegram for the test.
func withFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	fake := &fakeTelegram{}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(fake), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	prevBot, prevRunning := bot, isRunning
	bot, isRunning = b, true
	t.Cleanup(func() { bot, isRunning = prevBot, prevRunning })
	return fake
}

// adminPress is an admin's tap on a button carrying data, as Telegram
// delivers it to the bot: on the chat's screen, message 9, which shows the
// main menu the first time.
func adminPress(bot *Tgbot, data string) {
	if sc := botScreens.of(usersTestChat); sc.msgID == 0 {
		sc.msgID, sc.route = 9, screenMenuRoute
	}
	bot.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: 1}, Data: data,
		Message: &telego.Message{MessageID: 9, Chat: telego.Chat{ID: usersTestChat}}}, true)
}

// probeState is everything a mutating button could change about probe-4.
func probeState(t *testing.T) string {
	t.Helper()
	var c model.Client
	for _, cl := range mustClients(t, &InboundService{}, 4) {
		if cl.Email == "probe-4" {
			c = cl
		}
	}
	var tr xray.ClientTraffic
	var ips model.InboundClientIps
	db := database.GetDB()
	db.Where("email = ?", "probe-4").First(&tr)
	db.Where("client_email = ?", "probe-4").First(&ips)
	return fmt.Sprintf("enable=%v total=%d expiry=%d limitIp=%d tgId=%d up=%d down=%d trafficEnable=%v ips=%s",
		c.Enable, c.TotalGB, c.ExpiryTime, c.LimitIP, c.TgID, tr.Up, tr.Down, tr.Enable, ips.Ips)
}

// TestProbeCardRefusalReachesTheBot: the bot itself refuses a crafted
// confirmation on a probe — those the inbound guard already stops, and reset
// traffic and clear IPs, which it does not — and the probe stays as it was.
func TestProbeCardRefusalReachesTheBot(t *testing.T) {
	tg := probeBotFixture(t)
	fake := withFakeTelegram(t)
	before := probeState(t)

	for _, data := range []string{"toggle_enable_c probe-4", "reset_traffic_c probe-4", "limit_traffic_c probe-4 5",
		"reset_exp_c probe-4 7", "ip_limit_c probe-4 2", "clear_ips_c probe-4", "tgid_remove_c probe-4"} {
		fake.calls = nil
		adminPress(tg, data)
		if got := fake.texts(); !strings.Contains(got, "answerCallbackQuery: 🔒 probe-4 is a probe account: read only.") ||
			!strings.Contains(got, "editMessageText: 📡 <b>probe-4</b>") {
			t.Errorf("%q: the bot said\n%s", data, got)
		}
		if after := probeState(t); after != before {
			t.Errorf("%q changed the probe:\n before %s\n after  %s", data, before, after)
		}
	}
}

// lastSent is the last message the bot sent: its text and button labels.
func (f *fakeTelegram) lastSent(t *testing.T) (string, []string) {
	t.Helper()
	for i := len(f.calls) - 1; i >= 0; i-- {
		c := f.calls[i]
		if c.method != "sendMessage" {
			continue
		}
		var labels []string
		markup, _ := c.params["reply_markup"].(map[string]any)
		rows, _ := markup["inline_keyboard"].([]any)
		for _, row := range rows {
			for _, b := range row.([]any) {
				labels = append(labels, fmt.Sprint(b.(map[string]any)["text"]))
			}
		}
		text, _ := c.params["text"].(string)
		return text, labels
	}
	t.Fatalf("nothing sent:\n%s", f.texts())
	return "", nil
}

// TestProbeCardFromUsageSearch: /usage with a probe's name — the admin's
// search by client name — shows the read-only card instead of «No result».
func TestProbeCardFromUsageSearch(t *testing.T) {
	tg := probeBotFixture(t)
	fake := withFakeTelegram(t)
	for _, email := range []string{"probe-4", "probe-awg-ams-1-direct"} {
		fake.calls = nil
		tg.answerCommand(&telego.Message{Text: "/usage " + email, Chat: telego.Chat{ID: usersTestChat},
			From: &telego.User{ID: 1}}, usersTestChat, true)
		text, labels := fake.lastSent(t)
		if !strings.Contains(text, "<b>"+email+"</b>") || strings.Join(labels, "|") != "🔄 Refresh|⬅️ Back|🏠 Menu" {
			t.Errorf("/usage %s: %q %q", email, text, labels)
		}
	}
}

// lastKeyboard maps the labels of the last keyboard the bot sent or edited
// in to their callback data.
func (f *fakeTelegram) lastKeyboard(t *testing.T) (text string, labels []string, data map[string]string) {
	t.Helper()
	for i := len(f.calls) - 1; i >= 0; i-- {
		c := f.calls[i]
		markup, ok := c.params["reply_markup"].(map[string]any)
		if !ok {
			continue
		}
		data = map[string]string{}
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
	t.Fatalf("no keyboard:\n%s", f.texts())
	return "", nil, nil
}

// TestProbeCardFromTheMonitoringCard: monitoring's card lists its probe
// accounts, each opens its read-only card, and the screen's Back leads to the
// list and on to monitoring.
func TestProbeCardFromTheMonitoringCard(t *testing.T) {
	tg := probeBotFixture(t)
	fake := withFakeTelegram(t)

	adminPress(tg, "usr_c "+model.SubUserMonitoringKey)
	_, labels, data := fake.lastKeyboard(t)
	if strings.Join(labels, "|") != "📡 Probe accounts|⬅️ Back|🏠 Menu" {
		t.Fatalf("monitoring's card: %q", labels)
	}

	adminPress(tg, data["📡 Probe accounts"])
	text, labels, list := fake.lastKeyboard(t)
	want := []string{"probe-4 · probe-home", "probe-awg-ams-1-direct · awg", "⬅️ Back", "🏠 Menu"}
	if strings.Join(labels, "|") != strings.Join(want, "|") || !strings.Contains(text, "1–2 of 2") {
		t.Fatalf("probe list: %q\n got %q\nwant %q", text, labels, want)
	}

	for _, label := range want[:2] {
		fake.calls = nil
		adminPress(tg, list[label])
		text, labels, data := fake.lastKeyboard(t)
		email, _, _ := strings.Cut(label, " · ")
		if !strings.Contains(text, "<b>"+email+"</b>") || strings.Join(labels, "|") != "🔄 Refresh|⬅️ Back|🏠 Menu" {
			t.Errorf("%s: %q %q", label, text, labels)
		}
		adminPress(tg, data["⬅️ Back"])
		if text, _, _ := fake.lastKeyboard(t); !strings.Contains(text, "1–2 of 2") {
			t.Errorf("back from %s: %s", label, text)
		}
	}

	_, _, data = fake.lastKeyboard(t)
	adminPress(tg, data["⬅️ Back"])
	text, _, _ = fake.lastKeyboard(t)
	if !strings.Contains(text, "<b>monitoring</b>") {
		t.Errorf("back from the list: %s", text)
	}
}

// TestProbeListPages: a long list of probes goes page by page.
func TestProbeListPages(t *testing.T) {
	bot := probeBotFixture(t)
	for i := range 25 {
		awgPeer(t, 50+i, fmt.Sprintf("probe-awg-c%02d-direct", i), "s-probe")
	}
	reply := probePress(t, bot, "prb_l 0")
	if !strings.Contains(reply.text, "1–20 of 27") || !slices.Contains(buttonTexts(t, reply.keyboard), "➡️") {
		t.Fatalf("first page: %q %q", reply.text, buttonTexts(t, reply.keyboard))
	}
	reply = probePress(t, bot, button(t, reply.keyboard, "➡️"))
	labels := buttonTexts(t, reply.keyboard)
	if !strings.Contains(reply.text, "21–27 of 27") || len(labels) != 7+1 || labels[7] != "⬅️" || reply.route != "prb_l 1" {
		t.Errorf("second page: %q %q", reply.text, labels)
	}
}

// TestProbeCardFromUsersSearch: the Users search by client name opens a
// probe's own read-only card rather than monitoring's.
func TestProbeCardFromUsersSearch(t *testing.T) {
	bot := probeBotFixture(t)
	for _, q := range []string{"probe-4", "PROBE-awg-ams-1-direct"} {
		press(t, bot, "usr_menu")
		reply := typeText(t, bot, q)
		if !strings.Contains(strings.ToLower(reply.text), "<b>"+strings.ToLower(q)+"</b>") ||
			strings.Join(buttonTexts(t, reply.keyboard), "|") != "🔄 Refresh" {
			t.Errorf("search %q: %q %q", q, reply.text, buttonTexts(t, reply.keyboard))
		}
	}
}

// TestProbeCardFromTheInboundClientList: the inbound's client list still
// lists its probe, and the probe opens read-only.
func TestProbeCardFromTheInboundClientList(t *testing.T) {
	tg := probeBotFixture(t)
	fake := withFakeTelegram(t)

	adminPress(tg, "s_ib 4 0")
	_, labels, data := fake.lastKeyboard(t)
	if strings.Join(labels, "|") != "🟢 probe-4|⬅️ Back|🏠 Menu" {
		t.Fatalf("clients of probe-home: %q", labels)
	}
	fake.calls = nil
	adminPress(tg, data["🟢 probe-4"])
	text, labels, _ := fake.lastKeyboard(t)
	if !strings.Contains(text, "<b>probe-4</b>") || strings.Join(labels, "|") != "🔄 Refresh|⬅️ Back|🏠 Menu" {
		t.Errorf("probe-4 from the list: %q %q", text, labels)
	}
}

// TestProbeTextsInEveryLanguage: every translation file carries the probe
// card's keys, so no language renders an empty line or button.
func TestProbeTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Probe map[string]string `toml:"probe"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Probe {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.probe] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.probe] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
