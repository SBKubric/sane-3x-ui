package service

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	"github.com/pelletier/go-toml/v2"
)

// The «➕ New user» dialogue (#193) on the one screen, driven through the
// fake Bot API as an admin would drive it: presses on the screen, texts the
// bot deletes.

// newUserDialog opens the dialogue on a fresh screen; the screen is message
// #1.
func newUserDialog(t *testing.T) (*Tgbot, *screenTelegram) {
	t.Helper()
	tg := usersBotFixture(t)
	fake := withScreenTelegram(t)
	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "➕ New user")
	return tg, fake
}

// newUserScreen is the dialogue's screen.
func newUserScreen(fake *screenTelegram) *screenMessage { return fake.messages[1] }

// wantScreen fails unless the screen's text has every want.
func wantScreen(t *testing.T, fake *screenTelegram, want ...string) {
	t.Helper()
	m := newUserScreen(fake)
	for _, w := range want {
		if !strings.Contains(m.text, w) {
			t.Errorf("the screen lacks %q:\n%s\nbuttons %q", w, m.text, m.labels)
		}
	}
}

// TestNewUserName: the dialogue opens on the screen and asks for the name;
// a taken or reserved name is refused on the same screen, the admin's texts
// are deleted, and a free name leads on to the email.
func TestNewUserName(t *testing.T) {
	tg, fake := newUserDialog(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan"})
	wantScreen(t, fake, "New user", "step 1/5", "name")
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels, "✖ Cancel") {
		t.Errorf("buttons: %q", labels)
	}

	for i, name := range []string{"IVAN", "robot", "probe-x"} {
		adminText(t, tg, 100+i, name)
		wantScreen(t, fake, "step 1/5", "⚠️")
		if last := fake.calls[len(fake.calls)-1]; last != "deleteMessage #"+strconv.Itoa(100+i) {
			t.Errorf("the text %q stays: %q", name, fake.calls)
		}
	}
	wantScreen(t, fake, "reserved")
	adminText(t, tg, 110, "IVAN")
	wantScreen(t, fake, "already exists")

	adminText(t, tg, 111, " petr ")
	wantScreen(t, fake, "step 2/5", "petr", "email")
	if fake.sent() != 1 || len(fake.live()) != 1 {
		t.Errorf("the dialogue should stay on one screen: %q", fake.calls)
	}
}

// newUserNamed runs the dialogue past the name step.
func newUserNamed(t *testing.T, name string) (*Tgbot, *screenTelegram) {
	t.Helper()
	tg, fake := newUserDialog(t)
	adminText(t, tg, 100, name)
	return tg, fake
}

// TestNewUserEmail: the contact email is optional — a malformed one is
// refused on the step, «Skip» goes on without one, a good one is kept.
func TestNewUserEmail(t *testing.T) {
	tg, fake := newUserNamed(t, "petr")
	adminText(t, tg, 101, "petr at example")
	wantScreen(t, fake, "step 2/5", "⚠️", "not an email address")
	adminText(t, tg, 102, "petr@example.org")
	wantScreen(t, fake, "step 3/5", "Choose the protocols")

	tg, fake = newUserNamed(t, "anna")
	fake.press(t, tg, 1, "Skip")
	wantScreen(t, fake, "step 3/5")
	if _, waiting := userStates.get(usersTestChat); waiting {
		t.Error("the protocols step waits for a text")
	}
}

// TestNewUserProtocols: the inbounds a user can have, the enabled ones
// ticked; a press flips one and changes the buttons only; «Next» with none
// ticked stays with a toast.
func TestNewUserProtocols(t *testing.T) {
	tg, fake := newUserNamed(t, "petr")
	fake.press(t, tg, 1, "Skip")
	want := []string{"✅ VLESS · NL Amsterdam #1", "✅ Trojan · de", "⬜ VMess · vm-off", "✅ AWG · awg", "Next ➡️", "✖ Cancel"}
	if got := newUserScreen(fake).labels; strings.Join(got[:len(want)], "|") != strings.Join(want, "|") {
		t.Fatalf("protocols:\n got %q\nwant %q", got, want)
	}
	fake.calls = nil
	fake.press(t, tg, 1, "Trojan · de")
	if strings.Join(fake.calls, "|") != "answerCallbackQuery|editMessageReplyMarkup #1" {
		t.Errorf("a tick should change the buttons only: %q", fake.calls)
	}
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels, "⬜ Trojan · de") {
		t.Errorf("after the tick: %q", labels)
	}

	for _, label := range []string{"NL Amsterdam", "AWG · awg"} {
		fake.press(t, tg, 1, label)
	}
	fake.calls = nil
	fake.press(t, tg, 1, "Next")
	if strings.Join(fake.calls, "|") != "answerCallbackQuery" {
		t.Errorf("Next with nothing ticked: %q", fake.calls)
	}
	wantScreen(t, fake, "step 3/5")
	fake.press(t, tg, 1, "VMess · vm-off")
	fake.press(t, tg, 1, "Next")
	wantScreen(t, fake, "step 4/5", "vm-off (VMess)", "50 GB per protocol", "Expiry: 30 d")
}

// TestNewUserLimits: presets mark the chosen one; «Custom value» takes a
// typed number, and refuses anything else on its step.
func TestNewUserLimits(t *testing.T) {
	tg, fake := newUserNamed(t, "petr")
	fake.press(t, tg, 1, "Skip")
	fake.press(t, tg, 1, "Next")
	labels := strings.Join(newUserScreen(fake).labels, "|")
	for _, want := range []string{"● 50 GB|100 GB|∞|✏️ Custom value", "● 30 d|90 d|∞|✏️ Custom value"} {
		if !strings.Contains(labels, want) {
			t.Errorf("limits buttons lack %q: %q", want, labels)
		}
	}
	fake.press(t, tg, 1, "100 GB")
	wantScreen(t, fake, "Traffic: 100 GB per protocol")
	fake.press(t, tg, 1, "90 d")
	wantScreen(t, fake, "Expiry: 90 d")

	fake.press(t, tg, 1, "Custom value") // the traffic's
	wantScreen(t, fake, "in GB")
	for i, bad := range []string{"lots", "-1", "1.5", "1000000"} {
		adminText(t, tg, 200+i, bad)
		wantScreen(t, fake, "⚠️", "not a whole number")
	}
	adminText(t, tg, 210, "15")
	wantScreen(t, fake, "step 4/5", "Traffic: 15 GB per protocol")

	m := newUserScreen(fake)
	tg.answerCallback(&telego.CallbackQuery{ID: "q", From: telego.User{ID: 1}, Data: m.data[labelIndex(t, m, "90 d")+2],
		Message: &telego.Message{MessageID: 1, Chat: telego.Chat{ID: usersTestChat}}}, true) // the expiry's custom value
	wantScreen(t, fake, "days after first use")
	adminText(t, tg, 211, "0")
	wantScreen(t, fake, "Traffic: 15 GB per protocol", "Expiry: ∞")
	fake.press(t, tg, 1, "∞") // the traffic's ∞
	wantScreen(t, fake, "Traffic: ∞ per protocol")
}

// newUserAtTelegram runs the dialogue to the Telegram step: name, no email,
// the enabled inbounds, the preset limits.
func newUserAtTelegram(t *testing.T, name string) (*Tgbot, *screenTelegram) {
	t.Helper()
	tg, fake := newUserNamed(t, name)
	fake.press(t, tg, 1, "Skip")
	fake.press(t, tg, 1, "Next")
	fake.press(t, tg, 1, "Next")
	wantScreen(t, fake, "step 5/5", "Telegram id")
	return tg, fake
}

// TestNewUserTelegram: the Telegram id is typed digits or an @nick (#219),
// and one user's only: a text that is neither, a nick the bot has not seen
// or another user's id is refused on the step.
func TestNewUserTelegram(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "petr")
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 4242, InboundIds: []int{2}})
	for i, bad := range []string{"12ab", "-5", "0"} {
		adminText(t, tg, 300+i, bad)
		wantScreen(t, fake, "step 5/5", "⚠️", "not a Telegram id")
	}
	adminText(t, tg, 305, "@petr")
	wantScreen(t, fake, "step 5/5", "⚠️", "@petr is unknown: the person has not written to the bot yet")
	adminText(t, tg, 310, "4242")
	wantScreen(t, fake, "step 5/5", "⚠️", "belongs to user ivan")
	adminText(t, tg, 311, "5151")
	wantScreen(t, fake, "Check the new user", "Telegram: 5151")
}

// TestNewUserCreate walks the whole dialogue and creates the user: a
// client per ticked inbound with the limits and the Telegram id, the contact
// email on the user and on its card, and the card on the screen with the
// way back to the menu only.
func TestNewUserCreate(t *testing.T) {
	tg, fake := newUserNamed(t, "petr")
	adminText(t, tg, 101, "petr@example.org")
	fake.press(t, tg, 1, "Trojan · de") // untick
	fake.press(t, tg, 1, "Next")
	fake.press(t, tg, 1, "100 GB")
	fake.press(t, tg, 1, "90 d")
	fake.press(t, tg, 1, "Next")
	adminText(t, tg, 102, "5151")
	wantScreen(t, fake, "Name: petr", "Contact email: petr@example.org", "NL Amsterdam #1 (VLESS), awg (AWG)",
		"100 GB per protocol", "Expiry: 90 d", "Telegram: 5151")
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels, "✅ Create|◀ Change|✖ Cancel") {
		t.Errorf("review buttons: %q", labels)
	}

	fake.press(t, tg, 1, "Create")
	v, err := (&SubUserService{}).Find("petr")
	if err != nil {
		t.Fatalf("not created: %v\n%s", err, newUserScreen(fake).text)
	}
	if v.ContactEmail != "petr@example.org" || v.TgId != 5151 || len(v.Clients) != 2 {
		t.Fatalf("user: %+v", v)
	}
	for _, name := range []string{"petr-NL-Amsterdam-1", "petr-awg"} {
		if c := clientByName(t, v, name); c.TotalGB != 100<<30 || c.ExpiryTime != -90*86400000 || c.TgId != 5151 || !c.Enable {
			t.Errorf("client %s: %+v", name, c)
		}
	}
	wantScreen(t, fake, "User petr created", "/sub/"+v.SubId, "<b>petr</b>", "Contact email: petr@example.org", "Telegram: 5151")
	if fake.sent() != 1 || len(fake.live()) != 1 {
		t.Errorf("the dialogue left messages: %q", fake.calls)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	wantScreen(t, fake, "Main menu")
	if again := press(t, tg, "nu_ok"); !strings.Contains(again.text, "expired") {
		t.Errorf("a second Create: %+v", again)
	}
}

// TestNewUserChange: «◀ Change» on the review goes back to the protocols,
// the rest of the draft kept.
func TestNewUserChange(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "petr")
	fake.press(t, tg, 1, "Later")
	wantScreen(t, fake, "Check the new user", "Telegram: later")
	fake.press(t, tg, 1, "Change")
	wantScreen(t, fake, "step 3/5")
	fake.press(t, tg, 1, "Trojan · de")
	fake.press(t, tg, 1, "Next")
	fake.press(t, tg, 1, "Next")
	fake.press(t, tg, 1, "Later")
	wantScreen(t, fake, "Name: petr", "NL Amsterdam #1 (VLESS), awg (AWG)")
}

// TestNewUserCancel: «✖ Cancel» on any step drops the draft and shows the
// main menu; nothing is created and the chat waits for no text.
func TestNewUserCancel(t *testing.T) {
	steps := map[string]func(*Tgbot, *screenTelegram){
		"name":  func(*Tgbot, *screenTelegram) {},
		"email": func(tg *Tgbot, _ *screenTelegram) { adminText(t, tg, 100, "petr") },
		"protocols": func(tg *Tgbot, fake *screenTelegram) {
			adminText(t, tg, 100, "petr")
			fake.press(t, tg, 1, "Skip")
		},
		"limits": func(tg *Tgbot, fake *screenTelegram) {
			adminText(t, tg, 100, "petr")
			fake.press(t, tg, 1, "Skip")
			fake.press(t, tg, 1, "Next")
		},
		"custom value": func(tg *Tgbot, fake *screenTelegram) {
			adminText(t, tg, 100, "petr")
			fake.press(t, tg, 1, "Skip")
			fake.press(t, tg, 1, "Next")
			fake.press(t, tg, 1, "Custom value")
		},
		"telegram": func(tg *Tgbot, fake *screenTelegram) {
			adminText(t, tg, 100, "petr")
			fake.press(t, tg, 1, "Skip")
			fake.press(t, tg, 1, "Next")
			fake.press(t, tg, 1, "Next")
		},
		"review": func(tg *Tgbot, fake *screenTelegram) {
			adminText(t, tg, 100, "petr")
			fake.press(t, tg, 1, "Skip")
			fake.press(t, tg, 1, "Next")
			fake.press(t, tg, 1, "Next")
			fake.press(t, tg, 1, "Later")
		},
	}
	for step, reach := range steps {
		t.Run(step, func(t *testing.T) {
			tg, fake := newUserDialog(t)
			reach(tg, fake)
			fake.press(t, tg, 1, "✖ Cancel")
			wantScreen(t, fake, "Main menu")
			if _, waiting := userStates.get(usersTestChat); waiting {
				t.Error("the chat still waits for a text")
			}
			if _, err := (&SubUserService{}).Find("petr"); err == nil {
				t.Error("a cancelled dialogue created the user")
			}
			if again := press(t, tg, "nu_ok"); !strings.Contains(again.text, "expired") {
				t.Errorf("the draft outlived the cancel: %+v", again)
			}
		})
	}
}

// TestNewUserLinksAnExistingAwgClient: an AmneziaWG client with the new
// client's name and no subscription is offered for linking; «Link» makes the
// user with it, no second AWG client made.
func TestNewUserLinksAnExistingAwgClient(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "ivan")
	awgPeer(t, 1, "ivan-awg", "")
	awgBefore := countAwgClients(t)
	fake.press(t, tg, 1, "Later")
	fake.press(t, tg, 1, "Create")
	wantScreen(t, fake, "Link the existing AWG client <code>ivan-awg</code>")
	if _, err := (&SubUserService{}).Find("ivan"); err == nil {
		t.Fatal("the user was created before the admin answered")
	}
	fake.press(t, tg, 1, "🔗 Link")
	v, err := (&SubUserService{}).Find("ivan")
	if err != nil {
		t.Fatalf("after linking: %v\n%s", err, newUserScreen(fake).text)
	}
	if c := clientByName(t, v, "ivan-awg"); c.Key != uuidN(1) {
		t.Errorf("linked client: %+v", c)
	}
	if n := countAwgClients(t); n != awgBefore {
		t.Errorf("AWG clients: %d, want %d", n, awgBefore)
	}
	wantScreen(t, fake, "User ivan created")
}

// TestNewUserRollsBack: a failure half-way is shown on the review as the
// service words it and leaves no user; the draft stays for another try.
func TestNewUserRollsBack(t *testing.T) {
	tg, fake := newUserAtTelegram(t, "ivan")
	if err := database.GetDB().Exec(`CREATE TRIGGER fail_inbound_2 BEFORE UPDATE ON inbounds WHEN NEW.id = 2
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	before := mustInbound(t, &InboundService{}, 1).Settings
	fake.press(t, tg, 1, "Later")
	fake.press(t, tg, 1, "Create")
	wantScreen(t, fake, "Check the new user", "⚠️", "injected failure")
	if _, err := (&SubUserService{}).Find("ivan"); err == nil {
		t.Error("the user survived the failed create")
	}
	if after := mustInbound(t, &InboundService{}, 1).Settings; after != before {
		t.Errorf("inbound 1 kept the rolled-back client:\n%s", after)
	}
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels, "✅ Create") {
		t.Errorf("the review should stay for another try: %q", labels)
	}
}

// TestNewUserTwoChats: each chat has its own draft (#200).
func TestNewUserTwoChats(t *testing.T) {
	tg := usersBotFixture(t)
	const other = usersTestChat + 1
	t.Cleanup(func() { usersSessions.drop(other); userStates.clear(other) })
	press(t, tg, "add_client")
	if _, ok := tg.usersCallback(other, "add_client"); !ok {
		t.Fatal("add_client not handled")
	}
	typeText(t, tg, "ivan")
	if reply, _ := tg.usersText(other, usersStateNewUser, "petr"); !strings.Contains(reply.text, "<b>petr</b>") {
		t.Fatalf("the other chat: %s", reply.text)
	}
	if reply := press(t, tg, "nu_skip"); !strings.Contains(reply.text, "<b>ivan</b>") {
		t.Errorf("this chat's draft: %s", reply.text)
	}
}

// TestNewUserCallbacksFitTelegram: every button of every step keeps its
// callback data within 64 bytes, for inbounds with long remarks too.
func TestNewUserCallbacksFitTelegram(t *testing.T) {
	tg := usersBotFixture(t)
	usersInbound(t, 999999, model.VLESS, strings.Repeat("r", 200))
	var replies []usersReply
	replies = append(replies, press(t, tg, "add_client"))
	replies = append(replies, typeText(t, tg, strings.Repeat("n", 64)))
	for _, data := range []string{"nu_skip", "nu_go limits", "nu_gbc", "nu_go limits", "nu_dyc", "nu_go limits", "nu_go tg", "nu_go review"} {
		replies = append(replies, press(t, tg, data))
	}
	for _, r := range replies {
		sc := &botScreen{route: "x", back: []string{screenMenuRoute}}
		buttons(t, tg.screenKeyboard(sc, r.keyboard))
		if r.keyboard == nil {
			t.Errorf("a step without buttons: %+v", r)
		}
	}
}

// TestNewUserSpeaksRussian: the dialogue's texts in Russian, as the
// prototype words them.
func TestNewUserSpeaksRussian(t *testing.T) {
	tg := usersBotFixture(t)
	initTestBotLocale(t, "ru-RU")
	fake := withScreenTelegram(t)
	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "➕ Новый пользователь")
	wantScreen(t, fake, "Новый пользователь", "шаг 1/5")
	adminText(t, tg, 100, "petr")
	fake.press(t, tg, 1, "Пропустить")
	fake.press(t, tg, 1, "Далее")
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels, "✏️ Своё значение") {
		t.Errorf("limits: %q", labels)
	}
	fake.press(t, tg, 1, "Далее")
	fake.press(t, tg, 1, "Позже")
	wantScreen(t, fake, "Проверьте", "Почта: —", "Telegram: позже")
	if labels := strings.Join(newUserScreen(fake).labels, "|"); !strings.Contains(labels, "✅ Создать|◀ Изменить|✖ Отмена") {
		t.Errorf("review: %q", labels)
	}
}

// TestNewUserTextsInEveryLanguage: every translation file carries the
// dialogue's keys, so no language renders an empty step.
func TestNewUserTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				NewUser map[string]string `toml:"newUser"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.NewUser {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.newUser] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.newUser] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
