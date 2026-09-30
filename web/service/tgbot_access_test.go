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
// answering to accessAdmin only.
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
	prevAdmins := adminIds
	adminIds = []int64{accessAdmin}
	t.Cleanup(func() { adminIds = prevAdmins })
	return tg
}

// clientPress is a tap of usersTestChat, no admin, on a button carrying data.
func clientPress(tg *Tgbot, data string) {
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
	for _, must := range []string{"get_backup", "reset_all_traffics_c", "add_client_submit_enable", "usr_menu", "tun_rtc"} {
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
			clientPress(tg, data)
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

// TestNonAdminClientMenuStillWorks: the client menu shows its user their
// own clients, and their links.
func TestNonAdminClientMenuStillWorks(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)

	clientPress(tg, "client_traffic")
	if got := fake.texts(); !strings.Contains(got, "mine-1") || strings.Contains(got, "other-") {
		t.Errorf("client_traffic:\n%s", got)
	}

	fake.calls = nil
	clientPress(tg, "client_commands")
	if text, _ := fake.lastSent(t); !strings.Contains(text, "/usage [Email]") || strings.Contains(text, "/restart") {
		t.Errorf("client_commands: %q", text)
	}

	for _, action := range []string{"client_sub_links", "client_individual_links", "client_qr_links"} {
		fake.calls = nil
		clientPress(tg, action)
		_, labels, data := fake.lastKeyboard(t)
		if strings.Join(labels, "|") != "mine-1" || data["mine-1"] != action+" mine-1" {
			t.Errorf("%s: %q %v", action, labels, data)
		}
		if !tg.clientMayPress(&telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: action + " mine-1"}) {
			t.Errorf("%s mine-1 refused", action)
		}
	}

	fake.calls = nil
	clientPress(tg, "client_sub_links mine-1")
	if text, _ := fake.lastSent(t); !strings.Contains(text, "Subscription URL") || !strings.Contains(text, "s-mine") {
		t.Errorf("client_sub_links mine-1: %q", text)
	}
}

// TestAdminCallbacksStillServed: an admin's buttons work as before.
func TestAdminCallbacksStillServed(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)

	for data, want := range map[string]string{
		"commands":                  "/restart",
		"inbounds":                  "NL Amsterdam #1",
		"client_sub_links other-nl": "s-other",
	} {
		fake.calls = nil
		adminPress(tg, data)
		if text, _ := fake.lastSent(t); !strings.Contains(text, want) {
			t.Errorf("%q: %q lacks %q", data, text, want)
		}
	}
}

// TestNonAdminCommandsAreLimitedToClientCommands: the admin commands answer
// someone who is no admin as unknown ones and change nothing, /usage shows
// them their own clients only, and /start the client menu.
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

	fake.calls = nil
	clientCommand(tg, "/start")
	text, labels := fake.lastSent(t)
	if strings.Contains(text, "Welcome to") || slices.Contains(labels, "Get DB Backup") ||
		!slices.Contains(labels, "Get Usage") || len(labels) != 5 {
		t.Errorf("/start: %q %q", text, labels)
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
