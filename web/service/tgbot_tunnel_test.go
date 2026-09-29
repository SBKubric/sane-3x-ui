package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/pelletier/go-toml/v2"
)

// tunnelPress runs a button of the tunnel clients flows and fails when
// nothing handles it.
func tunnelPress(t *testing.T, bot *Tgbot, data string) screenReply {
	t.Helper()
	reply, ok := bot.tunnelCallback(data)
	if !ok {
		t.Fatalf("callback %q not handled", data)
	}
	return reply
}

// screenPressData runs a button of the admin's screen, as the screen does,
// and returns what the screen would show.
func screenPressData(t *testing.T, bot *Tgbot, data string) screenReply {
	t.Helper()
	return bot.screenRoute(usersTestChat, data)
}

// TestTunnelInboundListsItsClients: Inbounds and clients → the AmneziaWG
// inbound lists its tunnel clients, probe accounts left out, each button
// opening the client's card; the WireGuard inbound lists its own.
func TestTunnelInboundListsItsClients(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 1, "anna-awg", "")
	awgPeer(t, 2, ProbeTunnelEmail("mc1", "direct"), "")
	awgPeer(t, 3, "boris-awg", "")
	wgPeer(t, 4, "vera-wg", "")

	reply := screenPressData(t, bot, "s_ib 5 0")
	if got := strings.Join(buttonTexts(t, reply.keyboard), "|"); got != "🟢 anna-awg|🟢 boris-awg" {
		t.Fatalf("awg list: %q", got)
	}
	if !strings.Contains(reply.text, "awg (AWG)") || !strings.Contains(reply.text, "Clients: 2") || reply.route != "s_ib 5 0" {
		t.Errorf("awg list message: %+v", reply.usersReply)
	}
	if open := button(t, reply.keyboard, "boris-awg"); open != "tun_c "+uuidN(3) {
		t.Errorf("boris-awg opens %q", open)
	}

	reply = screenPressData(t, bot, "s_ib 6 0")
	if got := strings.Join(buttonTexts(t, reply.keyboard), "|"); got != "🟢 vera-wg" {
		t.Fatalf("wg list: %q", got)
	}
}

// TestTunnelInboundWithoutClients says there are none.
func TestTunnelInboundWithoutClients(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 2, ProbeTunnelEmail("mc1", "direct"), "")

	reply := screenPressData(t, bot, "s_ib 5 0")
	if reply.keyboard != nil || !strings.Contains(reply.text, "Clients: 0") {
		t.Errorf("empty awg list: %+v", reply.usersReply)
	}
}

// TestTunnelFlowsLeaveOtherButtons: the tunnel flows leave the other buttons
// alone.
func TestTunnelFlowsLeaveOtherButtons(t *testing.T) {
	bot := usersBotFixture(t)
	for _, data := range []string{"s_ib 1 0", "client_get_usage other-nl", "usr_c x", "tun_x 1"} {
		if _, ok := bot.tunnelCallback(data); ok {
			t.Errorf("%q taken by the tunnel flows", data)
		}
	}
}

// TestTunnelClientCard: a tunnel client's card shows its state, its user and
// the user's subscription, with the buttons that make sense for a tunnel
// client — no IP limit, IP log or Telegram user of the xray card.
func TestTunnelClientCard(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{5}})
	ivanAwg := clientByName(t, ivan, "ivan-awg").Key
	awgPeer(t, 1, "legacy-awg", "")
	wgPeer(t, 2, "vera-wg", "")

	cases := []struct {
		name string
		uuid string
		want []string
		text []string
	}{
		{"linked", ivanAwg, []string{"🔄 Refresh", "📈 Reset Traffic", "🔴 Disable", "📄 Config and QR", "👤 ivan"},
			[]string{"<code>ivan-awg</code>", "awg (amneziawg)", "Enabled: ✅ Yes", "Offline", "Traffic: ↑↓0.00B / ♾ Unlimited",
				"User: ivan", "http://localhost:2096/sub/" + ivan.SubId}},
		{"without a subscription", uuidN(1), []string{"🔄 Refresh", "📈 Reset Traffic", "🔴 Disable", "📄 Config and QR", "👤 robot"},
			[]string{"<code>legacy-awg</code>", "User: robot", "No subscription"}},
		{"wireguard", uuidN(2), []string{"🔄 Refresh", "📈 Reset Traffic", "🔴 Disable", "📄 Config and QR", "👤 robot"},
			[]string{"<code>vera-wg</code>", "wg (nativewg)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reply := tunnelPress(t, bot, "tun_c "+tc.uuid)
			if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("buttons:\n got %q\nwant %q", got, tc.want)
			}
			for _, want := range tc.text {
				if !strings.Contains(reply.text, want) {
					t.Errorf("card lacks %q:\n%s", want, reply.text)
				}
			}
			if refresh := button(t, reply.keyboard, "Refresh"); refresh != "tun_r "+tc.uuid {
				t.Errorf("refresh = %q", refresh)
			}
		})
	}

	// The user button opens the #169 user card.
	card := tunnelPress(t, bot, "tun_c "+ivanAwg)
	if open := button(t, card.keyboard, "👤 ivan"); open != "usr_c "+ivan.SubId {
		t.Errorf("user button = %q", open)
	}
	if user := press(t, bot, button(t, card.keyboard, "👤 ivan")); !strings.Contains(user.text, "<b>ivan</b>") {
		t.Errorf("user card: %s", user.text)
	}
	robot := tunnelPress(t, bot, "tun_c "+uuidN(1))
	if open := button(t, robot.keyboard, "👤 robot"); open != "usr_c "+model.SubUserRobotKey {
		t.Errorf("robot button = %q", open)
	}
}

// TestTunnelProbeHasNoCard: a probe account's card is not the operator's
// (its buttons would edit it); #183 gives it a read-only one.
func TestTunnelProbeHasNoCard(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 1, ProbeTunnelEmail("mc1", "direct"), "")
	probe := uuidN(1)
	for _, data := range []string{"tun_c " + probe, "tun_r " + probe, "tun_en " + probe + " 0", "tun_rt " + probe,
		"tun_rtc " + probe, "tun_cf " + probe} {
		reply := tunnelPress(t, bot, data)
		if reply.keyboard != nil || len(reply.files) != 0 || !strings.Contains(reply.text, "No result") {
			t.Errorf("%s on a probe: %+v", data, reply)
		}
	}
	if c, _ := (&AwgService{}).GetClientByUUID(uuidN(1)); !c.Enable {
		t.Error("the probe was switched off")
	}
}

// TestTunnelClientEnableDisable: the card's toggle switches the client and
// shows the card again in place.
func TestTunnelClientEnableDisable(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 1, "anna-awg", "")

	reply := tunnelPress(t, bot, button(t, tunnelPress(t, bot, "tun_c "+uuidN(1)).keyboard, "Disable"))
	if reply.route != "tun_c "+uuidN(1) || reply.toast != "✅ anna-awg: Disabled successfully." || !strings.Contains(reply.text, "Enabled: ❌ No") {
		t.Errorf("after Disable: %+v", reply.usersReply)
	}
	if c, _ := (&AwgService{}).GetClientByUUID(uuidN(1)); c.Enable {
		t.Fatal("Disable left the client enabled")
	}
	reply = tunnelPress(t, bot, button(t, reply.keyboard, "Enable"))
	if reply.toast != "✅ anna-awg: Enabled successfully." {
		t.Errorf("after Enable: %+v", reply.usersReply)
	}
	if c, _ := (&AwgService{}).GetClientByUUID(uuidN(1)); !c.Enable {
		t.Fatal("Enable left the client disabled")
	}
}

// TestTunnelClientResetTraffic: the reset asks first, then zeroes the
// client's counters; cancelling keeps them.
func TestTunnelClientResetTraffic(t *testing.T) {
	bot := usersBotFixture(t)
	c := awgPeer(t, 1, "anna-awg", "")
	if err := database.GetDB().Model(c).Updates(map[string]any{"upload": 1 << 20, "download": 2 << 20}).Error; err != nil {
		t.Fatal(err)
	}

	ask := tunnelPress(t, bot, button(t, tunnelPress(t, bot, "tun_c "+uuidN(1)).keyboard, "Reset Traffic"))
	if ask.route != "" || ask.text != "" {
		t.Errorf("the question replaces the card's keyboard only: %+v", ask.usersReply)
	}
	if cancel := button(t, ask.keyboard, "Cancel Reset"); cancel != "tun_r "+uuidN(1) {
		t.Errorf("cancel = %q", cancel)
	}
	if got, _ := (&AwgService{}).GetClientByUUID(uuidN(1)); got.Upload+got.Download == 0 {
		t.Fatal("asking reset the traffic already")
	}
	reply := tunnelPress(t, bot, button(t, ask.keyboard, "Confirm Reset Traffic"))
	if reply.route != "tun_c "+uuidN(1) || reply.toast != "✅ anna-awg: Traffic reset successfully." || !strings.Contains(reply.text, "Traffic: ↑↓0.00B") {
		t.Errorf("after the reset: %+v", reply.usersReply)
	}
	if got, _ := (&AwgService{}).GetClientByUUID(uuidN(1)); got.Upload+got.Download != 0 {
		t.Errorf("traffic after the reset: ↑%d ↓%d", got.Upload, got.Download)
	}
}

// TestTunnelClientConfig: the card sends the admin the client's .conf and its
// QR, as SendAwgConfigsToClients sends them to the client.
func TestTunnelClientConfig(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 1, "anna-awg", "")

	reply := tunnelPress(t, bot, button(t, tunnelPress(t, bot, "tun_c "+uuidN(1)).keyboard, "Config and QR"))
	if len(reply.files) != 2 {
		t.Fatalf("files: %+v", reply.files)
	}
	if conf := reply.files[0]; conf.name != "anna-awg.conf" || !strings.Contains(string(conf.data), "[Interface]") {
		t.Errorf("conf: %s\n%s", conf.name, conf.data)
	}
	if qr := reply.files[1]; qr.name != "anna-awg.conf.png" || !strings.HasPrefix(string(qr.data), "\x89PNG") {
		t.Errorf("qr: %s", qr.name)
	}
	if reply.text != "" || reply.keyboard != nil {
		t.Errorf("the card stays as it is: %+v", reply.usersReply)
	}
}

// TestTunnelCallbacksFitTelegram: every button of the tunnel flows keeps its
// callback data within Telegram's 64 bytes, a user with a long subId too.
func TestTunnelCallbacksFitTelegram(t *testing.T) {
	bot := usersBotFixture(t)
	long := mustCreateUser(t, SubUserCreate{Name: "long", SubId: strings.Repeat("s", 60), InboundIds: []int{5}})
	uuid := clientByName(t, long, "long-awg").Key

	for _, data := range []string{"s_ib 5 0", "tun_c " + uuid, "tun_rt " + uuid} {
		buttons(t, screenPressData(t, bot, data).keyboard) // fails on data over 64 bytes
	}
	if open := button(t, tunnelPress(t, bot, "tun_c "+uuid).keyboard, "👤 long"); open != "usr_c "+long.SubId {
		t.Errorf("user button = %q", open)
	}
}

// TestTunnelCardSpeaksRussian: the tunnel texts exist in Russian.
func TestTunnelCardSpeaksRussian(t *testing.T) {
	bot := usersBotFixture(t)
	initTestBotLocale(t, "ru-RU")
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{5}})

	reply := tunnelPress(t, bot, "tun_c "+clientByName(t, ivan, "ivan-awg").Key)
	for _, want := range []string{"Пользователь: ivan", "Подписка:", "Конфиг и QR", "Выключить"} {
		if !strings.Contains(reply.text+strings.Join(buttonTexts(t, reply.keyboard), "|"), want) {
			t.Errorf("card lacks %q:\n%s\n%q", want, reply.text, buttonTexts(t, reply.keyboard))
		}
	}
}

// TestTunnelTextsInEveryLanguage: every translation file carries the tunnel
// keys, so no language renders an empty button.
func TestTunnelTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Tunnel map[string]string `toml:"tunnel"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Tunnel {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.tunnel] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.tunnel] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
