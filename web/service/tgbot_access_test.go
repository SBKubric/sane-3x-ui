package service

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
)

// accessAdmin is the admin of the access tests; usersTestChat is a client
// of the bot who is no admin, chatting with it in private.
const accessAdmin = int64(1)

// accessBotFixture is usersBotFixture plus the client "mine-1" (vless inbound
// "mine", 11) that carries the Telegram ID of usersTestChat, with the bot
// answering to accessAdmin only. The users are synced, as at start-up.
func accessBotFixture(t *testing.T) *Tgbot {
	t.Helper()
	tg := usersBotFixture(t)
	ib := usersInbound(t, 11, model.VLESS, "mine",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000011", Email: "mine-1", SubID: "s-mine", TgID: usersTestChat, Enable: true})
	// The panel stores the settings indented, which is what the lookup by
	// Telegram ID matches.
	var settings map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(settings, "", "  ")
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", 11).Update("settings", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&SubUserService{}).Sync(); err != nil {
		t.Fatal(err)
	}
	prevAdmins := adminIds
	adminIds = []int64{accessAdmin}
	t.Cleanup(func() { adminIds = prevAdmins })
	return tg
}

// nonAdminPress is a tap of usersTestChat, no admin, on a button carrying
// data: on the chat's screen, message 9, which shows the main menu the first
// time.
func nonAdminPress(tg *Tgbot, data string) {
	if sc := botScreens.of(usersTestChat); sc.msgID == 0 {
		sc.msgID, sc.route = 9, screenMenuRoute
	}
	tg.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: usersTestChat}, Data: data,
		Message: &telego.Message{MessageID: 9, Chat: telego.Chat{ID: usersTestChat}}}, checkAdmin(usersTestChat))
}

// clientCommand is a command usersTestChat, no admin, sends the bot.
func clientCommand(tg *Tgbot, text string) {
	tg.answerCommand(&telego.Message{Text: text, Chat: telego.Chat{ID: usersTestChat},
		From: &telego.User{ID: usersTestChat, FirstName: "Client"}}, usersTestChat, checkAdmin(usersTestChat))
}

// botState is everything the bot could change: every table of the database,
// the add-client draft, and the chat's state and session.
func botState(t *testing.T) string {
	t.Helper()
	db := database.GetDB()
	var tables []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, table := range tables {
		rows, err := db.Raw(`SELECT * FROM "` + table + `"`).Rows()
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %v\n", table, vals)
		}
		rows.Close()
	}
	state, hasState := userStates[usersTestChat]
	usersSessions.mu.Lock()
	session := usersSessions.m[usersTestChat]
	usersSessions.mu.Unlock()
	fmt.Fprintf(&b, "draft id=%s email=%s sub=%s inbound=%d total=%d expiry=%d ip=%d\n",
		client_Id, client_Email, client_SubID, receiver_inbound_ID, client_TotalGB, client_ExpiryTime, client_LimitIP)
	fmt.Fprintf(&b, "state=%q/%v session=%v\n", state, hasState, session != nil)
	return b.String()
}

// botCallbackData is every string a case of the bot's switches names, plus
// the callback actions kept in constants: all the callback data the bot
// could act on.
func botCallbackData(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("tgbot*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no bot sources: %v", err)
	}
	seen := map[string]bool{chainSwitchCallback: true, probeCardAction: true, probeListAction: true}
	// The admin screen's own callbacks (#191).
	for _, action := range []string{screenBackData, screenMenuData, screenMenuRoute, screenInboundsRoute, screenInboundAction,
		screenOnlineRoute, screenServerRoute, screenBackupData, screenBanLogsData, screenChainRoute, screenSoonData,
		screenClientAction, usersListAction, usersFoundAction, usersSubAction, usersSubQRAction,
		// The client's screens (#194).
		mysubUserRoute, mysubSubRoute, mysubConfigsRoute, mysubLinksAction, mysubTunnelAction} {
		seen[action] = true
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil && s != "" {
							seen[s] = true
						}
					}
				}
			}
			return true
		})
	}
	// The old admin menu's buttons: their handlers are gone (#194), but the
	// buttons are still in the chats.
	for _, old := range []string{"get_usage", "usage_refresh", "inbounds", "deplete_soon", "get_backup", "get_banlogs",
		"onlines", "onlines_refresh", "commands", "add_client", "add_client_ch_default_email", "add_client_ch_default_id",
		"add_client_ch_default_pass_tr", "add_client_ch_default_pass_sh", "add_client_ch_default_comment",
		"add_client_ch_default_traffic", "add_client_ch_default_exp", "add_client_ch_default_ip_limit",
		"add_client_default_info", "add_client_cancel", "add_client_default_traffic_exp", "add_client_default_ip_limit",
		"add_client_submit_disable", "add_client_submit_enable", "reset_all_traffics_cancel", "reset_all_traffics",
		"reset_all_traffics_c", "get_sorted_traffic_usage_report"} {
		seen[old] = true
	}
	for _, must := range []string{"usr_menu", "tun_rtc", "reset_traffic_c"} {
		if !seen[must] {
			t.Fatalf("case %q not found: the scan misses the bot's callbacks", must)
		}
	}
	var data []string
	for s := range seen {
		data = append(data, s)
	}
	slices.Sort(data)
	return data
}

// TestNonAdminCallbacksAreLimitedToClientActions: someone who is no admin
// gets nothing but a «No result» toast for any button outside the client
// menu — with or without arguments, and for the link buttons of a client
// that is not theirs — and nothing changes.
func TestNonAdminCallbacksAreLimitedToClientActions(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)
	before := botState(t)

	for _, action := range botCallbackData(t) {
		for _, data := range []string{action, action + " 1", action + " other-nl", action + " other-nl 1"} {
			if clientCallbacks[data] {
				continue
			}
			fake.calls = nil
			nonAdminPress(tg, data)
			if len(fake.calls) != 1 || fake.calls[0].method != "answerCallbackQuery" ||
				fake.calls[0].params["text"] != "❗ No result!" {
				t.Errorf("%q: the bot said\n%s", data, fake.texts())
			}
			if after := botState(t); after != before {
				t.Fatalf("%q changed the bot's state:\n before %s\n after  %s", data, before, after)
			}
		}
	}
}

// TestNonAdminOldClientButtonsOpenTheirScreens: the buttons of the old
// client menu, still in the chats, open the client's own screens: usage and
// commands «My subscription», the subscription links «Show subscription»,
// the individual and QR links and a client's usage «My configs».
func TestNonAdminOldClientButtonsOpenTheirScreens(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)

	for data, want := range map[string]string{
		"client_traffic":                 "My subscription</b> · mine-1",
		"client_commands":                "My subscription</b> · mine-1",
		"client_sub_links":               "/sub/s-mine",
		"client_individual_links":        "My configs</b> · mine-1",
		"client_qr_links":                "My configs</b> · mine-1",
		"client_sub_links mine-1":        "/sub/s-mine",
		"client_individual_links mine-1": "My configs</b> · mine-1",
		"client_qr_links mine-1":         "My configs</b> · mine-1",
		"client_get_usage mine-1":        "My configs</b> · mine-1",
	} {
		fake.calls = nil
		nonAdminPress(tg, data)
		if text, _ := fake.lastSent(t); !strings.Contains(text, want) || strings.Contains(fake.texts(), "other-") {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
	}
}

// TestNonAdminRoutesNameOnlyTheirOwn: each route of the client's screens
// (#194) opens for the sender's own user and clients — their other clients
// too, not only the one that carries their Telegram ID — while the same
// route crafted with another user's subId, a technical user's key or another
// user's client gets nothing but «No result» and changes nothing. The
// handlers refuse such a route on their own as well.
func TestNonAdminRoutesNameOnlyTheirOwn(t *testing.T) {
	tg := accessBotFixture(t)
	mine := awgPeer(t, 21, "mine-awg", "s-mine")
	other := awgPeer(t, 22, "other-awg", "s-other")
	fake := withFakeTelegram(t)
	query := func(data string) *telego.CallbackQuery {
		return &telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: data}
	}

	for data, want := range map[string]string{
		screenMenuRoute:                     "My subscription</b> · mine-1",
		screenMenuData:                      "My subscription</b> · mine-1",
		mysubUserRoute + " s-mine":          "My subscription</b> · mine-1",
		mysubSubRoute + " s-mine":           "/sub/s-mine",
		mysubConfigsRoute + " s-mine":       "mine-awg",
		mysubTunnelAction + " " + mine.UUID: "",
	} {
		if !tg.clientMayPress(query(data)) {
			t.Errorf("%q refused", data)
		}
		fake.calls = nil
		nonAdminPress(tg, data)
		if got := fake.texts(); !strings.Contains(got, want) || strings.Contains(got, "other-") || strings.Contains(got, "No result") {
			t.Errorf("%q: the bot said\n%s", data, got)
		}
	}
	// The links button fetches the subscription: the access list alone.
	if !tg.clientMayPress(query(mysubLinksAction + " s-mine")) {
		t.Errorf("%s s-mine refused", mysubLinksAction)
	}

	before := botState(t)
	for _, data := range []string{
		mysubUserRoute + " s-other", mysubSubRoute + " s-other", mysubConfigsRoute + " s-other", mysubLinksAction + " s-other",
		mysubSubRoute + " " + model.SubUserRobotKey, mysubConfigsRoute + " " + model.SubUserMonitoringKey,
		mysubSubRoute + " S-MINE", mysubSubRoute + " s-mine s-other",
		mysubTunnelAction + " " + other.UUID, mysubTunnelAction + " aaaaaaaa-0000-0000-0000-000000000011",
		mysubTunnelAction + " aaaaaaaa-0000-0000-0000-000000000001",
		"client_sub_links other-nl", "client_individual_links other-de", "client_qr_links other-awg", "client_get_usage other-nl",
		"client_sub_links mine-awg-x", "client_traffic mine-1",
	} {
		if tg.clientMayPress(query(data)) {
			t.Errorf("%q let through", data)
		}
		fake.calls = nil
		nonAdminPress(tg, data)
		if len(fake.calls) != 1 || fake.calls[0].method != "answerCallbackQuery" || fake.calls[0].params["text"] != "❗ No result!" {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
		if strings.HasPrefix(data, "my_") {
			reply := tg.mysubRoute(usersTestChat, data)
			if reply.toast != "❗ No result!" || !strings.Contains(reply.text, "mine-1") || reply.files != nil || reply.after != nil {
				t.Errorf("the handler of %q: %+v", data, reply)
			}
		}
	}
	if after := botState(t); after != before {
		t.Fatalf("crafted routes changed the bot's state:\n before %s\n after  %s", before, after)
	}
}

// TestAdminCallbacksStillServed: an admin's buttons are served on the
// admin's screen (#191): the view comes as an edit of the screen or a new
// message.
func TestAdminCallbacksStillServed(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)

	for data, want := range map[string]string{
		screenInboundsRoute:         "NL Amsterdam #1",
		"s_ib 1 0":                  "other-nl",
		"client_get_usage other-nl": "other-nl",
		"usr_sub s-other":           "s-other",
	} {
		fake.calls = nil
		adminPress(tg, data)
		text, labels, _ := fake.lastKeyboard(t)
		if shown := text + "|" + strings.Join(labels, "|"); !strings.Contains(shown, want) {
			t.Errorf("%q: %q lacks %q\n%s", data, shown, want, fake.texts())
		}
	}
}

// TestNonAdminCommandsAreLimitedToClientCommands: the admin commands answer
// someone who is no admin as unknown ones and change nothing, /usage shows
// them their own clients only, and /start and /help their own screen (#194).
func TestNonAdminCommandsAreLimitedToClientCommands(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)
	before := botState(t)

	for _, cmd := range []string{"/inbound NL", "/inbound", "/restart", "/restart now", "/proxy", "/proxy off", "/proxy edge"} {
		fake.calls = nil
		clientCommand(tg, cmd)
		if len(fake.calls) != 1 || fake.calls[0].method != "sendMessage" ||
			fake.calls[0].params["text"] != "❗ Unknown command." {
			t.Errorf("%s: the bot said\n%s", cmd, fake.texts())
		}
		if after := botState(t); after != before {
			t.Fatalf("%s changed the bot's state:\n before %s\n after  %s", cmd, before, after)
		}
	}

	fake.calls = nil
	clientCommand(tg, "/usage other-nl")
	if got := fake.texts(); got != "sendMessage: ❗ No result!\n" {
		t.Errorf("/usage other-nl: the bot said\n%s", got)
	}
	fake.calls = nil
	clientCommand(tg, "/usage mine-1")
	if text, _ := fake.lastSent(t); !strings.Contains(text, "mine-1") {
		t.Errorf("/usage mine-1: %q", text)
	}

	for _, cmd := range []string{"/start", "/help"} {
		fake.calls = nil
		clientCommand(tg, cmd)
		text, labels := fake.lastSent(t)
		if !strings.Contains(text, "My subscription</b> · mine-1") ||
			strings.Join(labels, "|") != "🔗 Show subscription|📄 My configs|🔄 Refresh" {
			t.Errorf("%s: %q %q", cmd, text, labels)
		}
	}
	if after := botState(t); after != before {
		t.Fatalf("client commands changed the bot's state:\n before %s\n after  %s", before, after)
	}
}

// TestAdminCommandsStillServed: an admin's commands work as before.
func TestAdminCommandsStillServed(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)

	for cmd, want := range map[string]string{"/inbound NL": "NL Amsterdam #1", "/usage other-nl": "other-nl"} {
		fake.calls = nil
		tg.answerCommand(&telego.Message{Text: cmd, Chat: telego.Chat{ID: usersTestChat},
			From: &telego.User{ID: accessAdmin}}, usersTestChat, checkAdmin(accessAdmin))
		if got := fake.texts(); !strings.Contains(got, want) {
			t.Errorf("%s: the bot said\n%s", cmd, got)
		}
	}
}

// TestOnlyAnAdminAnswersAChatState: the chat states all belong to admin
// flows, so only an admin's text answers one.
func TestOnlyAnAdminAnswersAChatState(t *testing.T) {
	accessBotFixture(t)
	for _, c := range []struct {
		from *telego.User
		want bool
	}{{&telego.User{ID: accessAdmin}, true}, {&telego.User{ID: usersTestChat}, false}, {nil, false}} {
		if got := fromAdmin(&telego.Message{From: c.from, Chat: telego.Chat{ID: usersTestChat}}); got != c.want {
			t.Errorf("from %+v: %v, want %v", c.from, got, c.want)
		}
	}
}
