package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestUsersListLine: a user's line in a list, as the prototype has it —
// on or paused, its protocols, the traffic used of the sum of its limits,
// its expiry.
func TestUsersListLine(t *testing.T) {
	bot := usersBotFixture(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	gb := func(tenths int64) int64 { return tenths * (1 << 30) / 10 }
	client := func(protocol string) SubUserClient { return SubUserClient{Protocol: protocol} }

	cases := []struct {
		name string
		view SubUserView
		want string
	}{
		{"on, limited", SubUserView{SubUser: model.SubUser{Name: "ivan"}, Enable: true,
			Clients: []SubUserClient{client("vless"), client("amneziawg"), client("vless")},
			Up:      gb(100), Down: gb(55), Total: gb(1000), ExpiryTime: nov1},
			"🟢 ivan · VLESS+AWG · 15.5/100 GB · until 01.11"},
		{"paused, unlimited, no expiry", SubUserView{SubUser: model.SubUser{Name: "oleg"},
			Clients: []SubUserClient{client("trojan")}, Down: gb(2)},
			"⏸ oleg · Trojan · 0.2/∞ · until —"},
		{"after first use", SubUserView{SubUser: model.SubUser{Name: "anna"}, Enable: true,
			Clients: []SubUserClient{client("vmess"), client("shadowsocks")}, Total: gb(480), ExpiryTime: -30 * 86400000},
			"🟢 anna · VMess+SS · 0/48 GB · until +30 d"},
		{"next year", SubUserView{SubUser: model.SubUser{Name: "petr"}, Enable: true,
			Clients: []SubUserClient{client("nativewg")}, ExpiryTime: time.Date(2027, 1, 3, 0, 0, 0, 0, time.Local).UnixMilli()},
			"🟢 petr · WG · 0/∞ · until 03.01.2027"},
		{"no clients", SubUserView{SubUser: model.SubUser{Name: "empty"}}, "⏸ empty · — · 0/∞ · until —"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bot.usersListLine(&tc.view, now); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}

	initTestBotLocale(t, "ru-RU")
	if got, want := bot.usersListLine(&cases[0].view, now), "🟢 ivan · VLESS+AWG · 15.5/100 ГБ · до 01.11"; got != want {
		t.Errorf("in Russian:\n got %q\nwant %q", got, want)
	}
}

// TestUsersListPages: the list shows ten regular users a page, by name, with
// ◀ page ▶ between the pages and robot and monitoring below; the chat waits
// for a search.
func TestUsersListPages(t *testing.T) {
	bot := usersBotFixture(t)
	// With the fixture's user of subId s-other, 23 users, "other…" first.
	for i := range 22 {
		mustCreateUser(t, SubUserCreate{Name: fmt.Sprintf("u%02d", 21-i)})
	}
	names := func(r usersReply) []string {
		var out []string
		for _, label := range buttonTexts(t, r.keyboard) {
			if strings.HasPrefix(label, "⏸ u") {
				out = append(out, strings.Fields(label)[1])
			}
		}
		return out
	}
	tail := func(r usersReply, n int) string {
		labels := buttonTexts(t, r.keyboard)
		return strings.Join(labels[len(labels)-n:], "|")
	}

	first := press(t, bot, "usr_l 0")
	if got := names(first); strings.Join(got, ",") != "u00,u01,u02,u03,u04,u05,u06,u07,u08" {
		t.Errorf("first page: %q", got)
	}
	if got := tail(first, 4); got != "1/3|▶|🤖 robot|📡 monitoring" {
		t.Errorf("first page's end: %q", got)
	}
	if !strings.Contains(first.text, "(23)") || first.route != "usr_l 0" || stateOf(usersTestChat) != usersStateSearch {
		t.Errorf("first page: %+v, state %q", first, stateOf(usersTestChat))
	}

	last := press(t, bot, button(t, press(t, bot, button(t, first.keyboard, "▶")).keyboard, "▶"))
	if got := names(last); strings.Join(got, ",") != "u19,u20,u21" || last.route != "usr_l 2" {
		t.Errorf("last page: %q, route %q", got, last.route)
	}
	if got := tail(last, 4); got != "◀|3/3|🤖 robot|📡 monitoring" {
		t.Errorf("last page's end: %q", got)
	}
	if beyond := press(t, bot, "usr_l 99"); beyond.route != "usr_l 2" {
		t.Errorf("a page past the end shows the last: %q", beyond.route)
	}
	if open := button(t, last.keyboard, "u21"); !strings.HasPrefix(open, "usr_c ") {
		t.Errorf("a line opens %q", open)
	}
}

// TestUsersListSinglePage has no pager.
func TestUsersListSinglePage(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	reply := press(t, bot, "usr_l 0")
	// other-nl's and other-de's subId s-other is a user of its own.
	if got := strings.Join(buttonTexts(t, reply.keyboard), "|"); !strings.HasPrefix(got, "🟢 ivan · Trojan · 0/∞ · until —|") ||
		!strings.HasSuffix(got, "|🤖 robot|📡 monitoring") || strings.Contains(got, "1/1") {
		t.Errorf("list: %q", got)
	}
	if open := button(t, reply.keyboard, "ivan"); open != "usr_c "+ivan.SubId {
		t.Errorf("ivan opens %q", open)
	}
}

// TestUsersSearch: one field finds a user exactly, ignoring case, by name,
// subId, xray email or Telegram id; several are listed, none is said so,
// and the chat keeps waiting for a search until a card opens.
func TestUsersSearch(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 424242, InboundIds: []int{2}})
	mustCreateUser(t, SubUserCreate{Name: "alpha"})
	mustCreateUser(t, SubUserCreate{Name: "beta", SubId: "alpha"})

	for _, q := range []string{"IVAN", " ivan ", strings.ToUpper(ivan.SubId), "IVAN-DE", "424242"} {
		press(t, bot, "usr_menu")
		reply := typeText(t, bot, q)
		if !strings.Contains(reply.text, "<b>ivan</b>") || reply.route != "usr_c "+ivan.SubId {
			t.Errorf("search %q: %+v", q, reply)
		}
		if _, waiting := userStates.get(usersTestChat); waiting {
			t.Errorf("search %q: the card still waits for a search", q)
		}
	}

	press(t, bot, "usr_menu")
	reply := typeText(t, bot, "Alpha")
	if got := strings.Join(buttonTexts(t, reply.keyboard), "|"); got != "⏸ alpha · — · 0/∞ · until —|⏸ beta · — · 0/∞ · until —" {
		t.Errorf("two found: %q", got)
	}
	if !strings.Contains(reply.text, "«Alpha»: 2") || reply.route != "usr_f Alpha" || stateOf(usersTestChat) != usersStateSearch {
		t.Errorf("two found: %+v", reply)
	}
	if again := press(t, bot, reply.route); again.text != reply.text {
		t.Errorf("the found list shows again: %q", again.text)
	}

	for _, q := range []string{"iva", "ivan-d", "4242", "@ivan"} {
		press(t, bot, "usr_menu")
		reply := typeText(t, bot, q)
		if !strings.Contains(reply.text, "Nobody found") || reply.route != "" || stateOf(usersTestChat) != usersStateSearch {
			t.Errorf("search %q: %+v", q, reply)
		}
	}
}

// TestUsersSearchService: Search is exact and ignores case, and finds every
// user a query names.
func TestUsersSearchService(t *testing.T) {
	usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "Ivan", InboundIds: []int{2}})
	mustCreateUser(t, SubUserCreate{Name: "oleg", SubId: "ivan"})
	names := func(q string) string {
		users, err := (&SubUserService{}).Search(q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, u := range users {
			out = append(out, u.Name)
		}
		return strings.Join(out, ",")
	}
	for q, want := range map[string]string{"ivan": "Ivan,oleg", "ivan-de": "Ivan", "robot": "robot", "": "", "i": ""} {
		if got := names(q); got != want {
			t.Errorf("Search(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestUserSubscriptionScreen: «Show subscription» gives the link and the
// status, and a button that sends the link's QR as a file.
func TestUserSubscriptionScreen(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	reply := press(t, bot, button(t, press(t, bot, "usr_c "+ivan.SubId).keyboard, "Show subscription"))
	for _, want := range []string{"Subscription of ivan", "<code>http://localhost:2096/sub/" + ivan.SubId + "</code>", "🟢 active"} {
		if !strings.Contains(reply.text, want) {
			t.Errorf("subscription lacks %q:\n%s", want, reply.text)
		}
	}
	if reply.route != "usr_sub "+ivan.SubId {
		t.Errorf("route %q", reply.route)
	}
	qr := screenPressData(t, bot, button(t, reply.keyboard, "QR"))
	if len(qr.files) != 1 || qr.files[0].name != "ivan.png" || !strings.HasPrefix(string(qr.files[0].data), "\x89PNG") || qr.text != "" {
		t.Errorf("QR: %+v", qr)
	}
}
