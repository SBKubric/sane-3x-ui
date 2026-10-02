package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The total limit in the bot (#247): «📄 My configs» and the admin's user
// card say how the sum of the clients' limits is made; «My subscription»
// has no traffic line; «➕ New user» starts from the request defaults.

// limitClient sets the limit of the user's client name to gb GB, behind the
// service's back.
func limitClient(t *testing.T, v *SubUserView, name string, gb int64) {
	t.Helper()
	c := clientByName(t, v, name)
	db := database.GetDB()
	if c.Kind != SubUserClientXray {
		if err := db.Model(&model.TunnelClient{}).Where("uuid = ?", c.Key).Update("total_gb", gb<<30).Error; err != nil {
			t.Fatal(err)
		}
		return
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
		if client := raw.(map[string]any); client["email"] == name {
			client["totalGB"] = gb << 30
		}
	}
	out, _ := json.Marshal(settings)
	if err := db.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("settings", string(out)).Error; err != nil {
		t.Fatal(err)
	}
}

// TestMyConfigsTotalLimit: «📄 My configs» opens with the total limit —
// «N × k» for one limit, «A + B» for different ones, a protocol two clients
// share named by the inbound, «unlimited» with any client unlimited — and
// names each client by its protocol.
func TestMyConfigsTotalLimit(t *testing.T) {
	tg := usersBotFixture(t)
	usersInbound(t, 11, model.VLESS, "xhttp")
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1, 5},
		SubUserParams: SubUserParams{TotalGB: 50 << 30}})
	configs := func() string { return tg.mysubRoute(usersTestChat, mysubConfigsRoute+" "+ivan.SubId).text }

	text := configs()
	title, rest, _ := strings.Cut(text, "\r\n")
	if !strings.Contains(title, "My configs") || !strings.HasPrefix(rest, "📊 Total limit: 100 GB (50 GB × 2 protocols) · used 0 GB\r\n") {
		t.Errorf("equal limits:\n%s", text)
	}
	for _, want := range []string{"🟢 VLESS · ivan-NL-Amsterdam-1 · 0/50 GB", "🟢 AmneziaWG · ivan-awg · 0/50 GB"} {
		if !strings.Contains(text, want) {
			t.Errorf("configs lack %q:\n%s", want, text)
		}
	}

	limitClient(t, ivan, "ivan-awg", 20)
	if text := configs(); !strings.Contains(text, "📊 Total limit: 70 GB (VLESS 50 GB + AmneziaWG 20 GB) · used 0 GB") {
		t.Errorf("different limits:\n%s", text)
	}

	if _, err := (&SubUserService{}).AddProtocol(ivan.SubId, 11, false); err != nil {
		t.Fatal(err)
	}
	ivan = mustGetUser(t, ivan.SubId)
	limitClient(t, ivan, clientOf(t, ivan, 11).Name, 30)
	text = configs()
	for _, want := range []string{"📊 Total limit: 100 GB (VLESS (NL Amsterdam #1) 50 GB + VLESS (xhttp) 30 GB + AmneziaWG 20 GB)",
		"🟢 VLESS (NL Amsterdam #1) · ivan-NL-Amsterdam-1", "🟢 VLESS (xhttp) · "} {
		if !strings.Contains(text, want) {
			t.Errorf("one protocol twice lacks %q:\n%s", want, text)
		}
	}

	limitClient(t, ivan, "ivan-awg", 0)
	if text := configs(); !strings.Contains(text, "📊 Total limit: unlimited · used 0 GB") {
		t.Errorf("an unlimited client:\n%s", text)
	}

	initTestBotLocale(t, "ru-RU")
	limitClient(t, ivan, "ivan-awg", 50)
	limitClient(t, ivan, clientOf(t, ivan, 11).Name, 50)
	if text := configs(); !strings.Contains(text, "📊 Общий лимит: 150 ГБ (50 ГБ × 3 протокола) · израсходовано 0 ГБ") {
		t.Errorf("ru:\n%s", text)
	}
	limitClient(t, ivan, "ivan-awg", 0)
	if text := configs(); !strings.Contains(text, "📊 Общий лимит: без ограничения · израсходовано 0 ГБ") {
		t.Errorf("ru unlimited:\n%s", text)
	}
}

// clientOf is the user's client in the inbound id.
func clientOf(t *testing.T, v *SubUserView, id int) SubUserClient {
	t.Helper()
	for _, c := range v.Clients {
		if c.InboundId == id {
			return c
		}
	}
	t.Fatalf("%s has no client in inbound %d: %+v", v.Name, id, v.Clients)
	return SubUserClient{}
}

// TestMySubscriptionHasNoTrafficLine: «My subscription» leaves the traffic
// to «📄 My configs».
func TestMySubscriptionHasNoTrafficLine(t *testing.T) {
	tg := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: usersTestChat, InboundIds: []int{1, 5},
		SubUserParams: SubUserParams{TotalGB: 50 << 30}})
	for _, lang := range []string{"en-US", "ru-RU"} {
		initTestBotLocale(t, lang)
		text := tg.mysubHome(usersTestChat).text
		for _, gone := range []string{"📊", "Traffic", "Трафик", "GB", "ГБ"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s: «My subscription» has %q:\n%s", lang, gone, text)
			}
		}
	}
}

// TestUserCardTotalLimit: an admin's user card has the total limit in place
// of the sum, the clients' rows below it; the lists keep the sum.
func TestUserCardTotalLimit(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 5}, SubUserParams: SubUserParams{TotalGB: 50 << 30}})
	limitClient(t, ivan, "ivan-awg", 20)

	text := press(t, bot, "usr_c "+ivan.SubId).text
	for _, want := range []string{"🔗 Subscription: 🟢 active · until —\r\n📊 Total limit: 70 GB (VLESS 50 GB + AmneziaWG 20 GB) · used 0 GB\r\n",
		"<code>ivan-NL-Amsterdam-1</code> · NL Amsterdam #1 · ↑↓0.00B / 50.00GB", "<code>ivan-awg</code> · awg · ↑↓0.00B / 20.00GB"} {
		if !strings.Contains(text, want) {
			t.Errorf("card lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "0/70") {
		t.Errorf("card still has the sum:\n%s", text)
	}
	if line := bot.usersListLine(mustGetUser(t, ivan.SubId), time.Now()); !strings.Contains(line, "0/70 GB") {
		t.Errorf("list line %q lost the sum", line)
	}

	initTestBotLocale(t, "ru-RU")
	if text := press(t, bot, "usr_c "+ivan.SubId).text; !strings.Contains(text, "🔗 Подписка: 🟢 активна · до —\r\n📊 Общий лимит: 70 ГБ (VLESS 50 ГБ + AmneziaWG 20 ГБ) · израсходовано 0 ГБ") {
		t.Errorf("ru card:\n%s", text)
	}
}

// newUserLimits opens «➕ New user» with the request defaults gb and days
// and runs it to the limits step.
func newUserLimits(t *testing.T, gb, days string) *screenTelegram {
	t.Helper()
	tg := usersBotFixture(t)
	setSetting(t, "subRequestTrafficGB", gb)
	setSetting(t, "subRequestExpiryDays", days)
	fake := withScreenTelegram(t)
	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "➕ New user")
	adminText(t, tg, 100, "petr")
	fake.press(t, tg, 1, "Skip")
	fake.press(t, tg, 1, "Next")
	return fake
}

// TestNewUserLimitsFromTheSettings: a new user starts with the request
// defaults; their value is the first preset, then 100 GB and ∞ (90 d and ∞),
// none twice.
func TestNewUserLimitsFromTheSettings(t *testing.T) {
	for _, c := range []struct {
		gb, days string
		want     []string
	}{
		{"70", "60", []string{"Traffic: 70 GB per protocol", "Expiry: 60 d", "● 70 GB|100 GB|∞|✏️ Custom value", "● 60 d|90 d|∞|✏️ Custom value"}},
		{"100", "90", []string{"Traffic: 100 GB per protocol", "Expiry: 90 d", "● 100 GB|∞|✏️ Custom value", "● 90 d|∞|✏️ Custom value"}},
		{"0", "0", []string{"Traffic: ∞ per protocol", "Expiry: ∞", "● ∞|100 GB|✏️ Custom value", "● ∞|90 d|✏️ Custom value"}},
	} {
		t.Run(c.gb+"/"+c.days, func(t *testing.T) {
			m := newUserScreen(newUserLimits(t, c.gb, c.days))
			got := m.text + "\n" + strings.Join(m.labels, "|")
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("limits step lacks %q:\n%s", want, got)
				}
			}
		})
	}
}
