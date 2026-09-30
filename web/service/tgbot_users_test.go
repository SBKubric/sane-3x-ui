package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/global"
	"github.com/mymmrac/telego"
	"github.com/pelletier/go-toml/v2"
)

const usersTestChat = int64(777)

// usersBotFixture is opsFixture — vless "NL Amsterdam #1" (1) and trojan "de"
// (2) with a client of user "other-nl" each, the AmneziaWG row "awg" (5) —
// plus a disabled vmess inbound (3) and one inbound of every protocol a user
// cannot have a client in. vless, trojan and awg are enabled.
func usersBotFixture(t *testing.T) *Tgbot {
	t.Helper()
	opsFixture(t)
	usersInbound(t, 3, model.VMESS, "vm-off")
	usersInbound(t, 6, model.NativeWG, "wg")
	usersInbound(t, 7, model.MTProto, "mt")
	usersInbound(t, 8, model.Mixed, "mixed")
	usersInbound(t, 9, model.HTTP, "http")
	usersInbound(t, 10, model.Tunnel, "tun")
	if err := database.GetDB().Model(&model.Inbound{}).Where("id IN ?", []int{1, 2, 5}).
		Update("enable", true).Error; err != nil {
		t.Fatal(err)
	}
	setSetting(t, "subPort", "2096")
	setSetting(t, "subPath", "/sub/")
	initTestBotLocale(t, "en-US")
	prevHash := hashStorage
	hashStorage = global.NewHashStorage(time.Minute)
	t.Cleanup(func() {
		hashStorage = prevHash
		usersSessions.drop(usersTestChat)
		botScreens.drop(usersTestChat)
		userStates.clear(usersTestChat)
	})
	return &Tgbot{}
}

// press runs a button of the users flows and fails when nothing handles it.
func press(t *testing.T, bot *Tgbot, data string) usersReply {
	t.Helper()
	reply, ok := bot.usersCallback(usersTestChat, data)
	if !ok {
		t.Fatalf("callback %q not handled", data)
	}
	return reply
}

// buttons flattens a keyboard into its buttons, checking every callback on the
// way: Telegram refuses callback data over 64 bytes.
func buttons(t *testing.T, kb *telego.InlineKeyboardMarkup) []telego.InlineKeyboardButton {
	t.Helper()
	if kb == nil {
		return nil
	}
	var out []telego.InlineKeyboardButton
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if len(b.CallbackData) == 0 || len(b.CallbackData) > 64 {
				t.Errorf("button %q: callback data %q is %d bytes", b.Text, b.CallbackData, len(b.CallbackData))
			}
			out = append(out, b)
		}
	}
	return out
}

// button finds the button whose text contains label and returns its decoded
// callback data.
func button(t *testing.T, kb *telego.InlineKeyboardMarkup, label string) string {
	t.Helper()
	for _, b := range buttons(t, kb) {
		if strings.Contains(b.Text, label) {
			data, err := (&Tgbot{}).decodeQuery(b.CallbackData)
			if err != nil {
				t.Fatalf("decode %q: %v", b.CallbackData, err)
			}
			return data
		}
	}
	t.Fatalf("no button %q in %+v", label, kb)
	return ""
}

func buttonTexts(t *testing.T, kb *telego.InlineKeyboardMarkup) []string {
	t.Helper()
	var out []string
	for _, b := range buttons(t, kb) {
		out = append(out, b.Text)
	}
	return out
}

// typeText sends text to the users flow the chat is waiting in.
func typeText(t *testing.T, bot *Tgbot, text string) usersReply {
	t.Helper()
	state, waiting := userStates.get(usersTestChat)
	if !waiting {
		t.Fatalf("the chat waits for no text (typing %q)", text)
	}
	userStates.clear(usersTestChat) // as OnReceive's handlers do
	reply, ok := bot.usersText(usersTestChat, state, text)
	if !ok {
		t.Fatalf("state %q not handled", state)
	}
	return reply
}

func mustCreateUser(t *testing.T, req SubUserCreate) *SubUserView {
	t.Helper()
	v, err := (&SubUserService{}).Create(req)
	if err != nil {
		t.Fatalf("Create(%+v): %v", req, err)
	}
	return v
}

// TestUserCardButtons: the card offers what the service allows on the user.
func TestUserCardButtons(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 1, "legacy-awg", "") // robot's
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	full := mustCreateUser(t, SubUserCreate{Name: "full", InboundIds: []int{1, 2, 3, 5}})
	empty := mustCreateUser(t, SubUserCreate{Name: "empty"})
	long := mustCreateUser(t, SubUserCreate{Name: "long", SubId: strings.Repeat("s", 60), InboundIds: []int{2}})

	cases := []struct {
		name string
		key  string
		want []string
		text []string
	}{
		{"regular", ivan.SubId, []string{"🔗 Show subscription", "Trojan · ivan-de", "➕ Protocol", "➖ Protocol", "⏸ Suspend", "🗑 Delete"},
			[]string{"<b>ivan</b>", "Subscription: 🟢 active · until — · 0/∞", "<b>trojan</b>", "<code>ivan-de</code>"}},
		{"every inbound taken", full.SubId, []string{"🔗 Show subscription", "VLESS · full-NL-Amsterdam-1", "Trojan · full-de",
			"VMess · full-vm-off", "AWG · full-awg", "➖ Protocol", "⏸ Suspend", "🗑 Delete"}, nil},
		{"no clients", empty.SubId, []string{"🔗 Show subscription", "➕ Protocol", "🗑 Delete"}, []string{"No clients", "⏸ paused"}},
		{"long subId", long.SubId, []string{"🔗 Show subscription", "Trojan · long-de", "➕ Protocol", "➖ Protocol", "⏸ Suspend", "🗑 Delete"}, nil},
		{"robot", model.SubUserRobotKey, []string{"📋 Clients without a subscription"},
			[]string{"<b>robot</b>", "Technical user", "No subscription"}},
		{"monitoring", model.SubUserMonitoringKey, nil, []string{"Technical user"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reply := press(t, bot, "usr_c "+tc.key)
			if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("buttons:\n got %q\nwant %q", got, tc.want)
			}
			for _, want := range tc.text {
				if !strings.Contains(reply.text, want) {
					t.Errorf("card lacks %q:\n%s", want, reply.text)
				}
			}
			if reply.route != "usr_c "+tc.key {
				t.Errorf("route = %q", reply.route)
			}
			// Every button acts on this user, whatever the length of its key.
			if tc.key[0] != '@' {
				if del := button(t, reply.keyboard, "Delete"); del != "usr_del "+tc.key {
					t.Errorf("delete = %q", del)
				}
				if sub := button(t, reply.keyboard, "Show subscription"); sub != "usr_sub "+tc.key {
					t.Errorf("show subscription = %q", sub)
				}
			}
		})
	}

	// A client opens its own card.
	if open := button(t, press(t, bot, "usr_c "+full.SubId).keyboard, "AWG · full-awg"); open != "tun_c "+clientByName(t, full, "full-awg").Key {
		t.Errorf("AWG client button = %q", open)
	}
	if open := button(t, press(t, bot, "usr_c "+full.SubId).keyboard, "Trojan · full-de"); open != "client_get_usage full-de" {
		t.Errorf("xray client button = %q", open)
	}

	// A switched-off user offers to switch it back on.
	reply := press(t, bot, button(t, press(t, bot, "usr_c "+ivan.SubId).keyboard, "Suspend"))
	if got := buttonTexts(t, reply.keyboard); !strings.Contains(strings.Join(got, "|"), "▶ Resume") {
		t.Errorf("after Suspend: %q", got)
	}
	if v, _ := (&SubUserService{}).Get(ivan.SubId); v.Enable {
		t.Error("Disable left the user enabled")
	}
}

// TestUserAddProtocolFromTheCard: ➕ offers only the inbounds the user lacks,
// and the new client gets the limits of the user's xray client.
func TestUserAddProtocolFromTheCard(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}, SubUserParams: SubUserParams{TotalGB: 10 << 30, LimitIp: 3}})

	reply := press(t, bot, button(t, press(t, bot, "usr_c "+ivan.SubId).keyboard, "➕ Protocol"))
	want := []string{"NL Amsterdam #1 (vless)", "vm-off (vmess)", "awg (amneziawg)"}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("add menu:\n got %q\nwant %q", got, want)
	}
	reply = press(t, bot, button(t, reply.keyboard, "NL Amsterdam"))
	if reply.route != "usr_c "+ivan.SubId || !strings.Contains(reply.text, "ivan-NL-Amsterdam-1") {
		t.Errorf("after adding: %+v", reply)
	}
	v, _ := (&SubUserService{}).Get(ivan.SubId)
	if c := clientByName(t, v, "ivan-NL-Amsterdam-1"); c.TotalGB != 10<<30 || c.LimitIp != 3 {
		t.Errorf("new client: %+v", c)
	}
}

// TestUserRemoveProtocol: ➖ asks first; a client goes, and the service's
// refusal to empty an xray inbound is shown with the way back.
func TestUserRemoveProtocol(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2, 3}})

	menu := press(t, bot, button(t, press(t, bot, "usr_c "+ivan.SubId).keyboard, "➖ Protocol"))
	confirm := press(t, bot, button(t, menu.keyboard, "de (trojan)"))
	if !strings.Contains(confirm.text, "<code>ivan-de</code>") {
		t.Errorf("confirmation: %+v", confirm)
	}
	if v, _ := (&SubUserService{}).Get(ivan.SubId); len(v.Clients) != 2 {
		t.Fatal("asking removed the client already")
	}
	reply := press(t, bot, button(t, confirm.keyboard, "Confirm"))
	if strings.Contains(reply.text, "ivan-de") || !strings.Contains(reply.text, "ivan-vm-off") {
		t.Errorf("card after removing: %s", reply.text)
	}

	// ivan's vmess client is the only client of vm-off.
	menu = press(t, bot, button(t, reply.keyboard, "➖ Protocol"))
	confirm = press(t, bot, button(t, menu.keyboard, "vm-off"))
	reply = press(t, bot, button(t, confirm.keyboard, "Confirm"))
	if !strings.Contains(reply.text, "would be left without clients") || !strings.Contains(reply.text, "<b>ivan</b>") {
		t.Errorf("refusal, on top of the card: %+v", reply)
	}
	if reply.route != "usr_c "+ivan.SubId {
		t.Errorf("the refusal shows the card: route %q", reply.route)
	}
}

// TestUserDelete: Delete asks first, then the user and its clients go.
func TestUserDelete(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	confirm := press(t, bot, button(t, press(t, bot, "usr_c "+ivan.SubId).keyboard, "🗑 Delete"))
	if _, err := (&SubUserService{}).Get(ivan.SubId); err != nil {
		t.Fatal("asking deleted the user already")
	}
	reply := press(t, bot, button(t, confirm.keyboard, "Confirm"))
	if !strings.Contains(reply.text, "deleted") || reply.route != "usr_l 0" || !reply.root {
		t.Errorf("after delete, the list with no way back to the card: %+v", reply)
	}
	if _, err := (&SubUserService{}).Get(ivan.SubId); err == nil {
		t.Error("the user survived")
	}
}

// TestUserSearch: the Users menu finds a user by name, subId or client name.
func TestUserSearch(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	for _, q := range []string{"Ivan", ivan.SubId, "ivan-de"} {
		press(t, bot, "usr_menu")
		reply := typeText(t, bot, q)
		if !strings.Contains(reply.text, "<b>ivan</b>") {
			t.Errorf("search %q: %s", q, reply.text)
		}
	}
	press(t, bot, "usr_menu")
	reply := typeText(t, bot, "nobody")
	if !strings.Contains(reply.text, "Nobody found for «nobody»") || stateOf(usersTestChat) != usersStateSearch {
		t.Errorf("search for nobody: %+v, state %q", reply, stateOf(usersTestChat))
	}
}

// TestUserAssignFromRobot: robot's clients are listed, and one is given to
// the user the operator names.
func TestUserAssignFromRobot(t *testing.T) {
	bot := usersBotFixture(t)
	awgPeer(t, 1, "legacy-awg", "")
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	list := press(t, bot, button(t, press(t, bot, "usr_c "+model.SubUserRobotKey).keyboard, "Clients without a subscription"))
	press(t, bot, button(t, list.keyboard, "legacy-awg · Assign to a user"))
	reply := typeText(t, bot, "ivan")
	if !strings.Contains(reply.text, "<code>legacy-awg</code>") {
		t.Errorf("ivan's card after the assign: %s", reply.text)
	}
	v, _ := (&SubUserService{}).Get(ivan.SubId)
	clientByName(t, v, "legacy-awg")
}

// TestUserFlowsSpeakRussian: the users texts exist in Russian, the link
// question worded as the owner asked for it.
func TestUserFlowsSpeakRussian(t *testing.T) {
	bot := usersBotFixture(t)
	initTestBotLocale(t, "ru-RU")
	awgPeer(t, 1, "ivan-awg", "")

	press(t, bot, "add_client")
	typeText(t, bot, "ivan")
	for _, data := range []string{"nu_skip", "nu_go limits", "nu_go tg", "nu_go review"} {
		press(t, bot, data)
	}
	reply := press(t, bot, "nu_ok")
	if !strings.Contains(reply.text, "Привязать существующего AWG-клиента <code>ivan-awg</code> к подписке?") {
		t.Errorf("link question: %s", reply.text)
	}
	reply = press(t, bot, button(t, reply.keyboard, "Привязать"))
	for _, want := range []string{"Подписка: 🟢 активна", "➕ Протокол", "➖ Протокол", "⏸ Приостановить", "🗑 Удалить"} {
		if !strings.Contains(reply.text+strings.Join(buttonTexts(t, reply.keyboard), "|"), want) {
			t.Errorf("card lacks %q:\n%s\n%q", want, reply.text, buttonTexts(t, reply.keyboard))
		}
	}
}

// TestUserTextsInEveryLanguage: every translation file carries the users
// keys, so no language renders an empty button.
func TestUserTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Users map[string]string `toml:"users"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Users {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.users] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.users] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
