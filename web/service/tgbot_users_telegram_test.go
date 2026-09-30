package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	"github.com/pelletier/go-toml/v2"
)

// setXrayTgId writes tgId onto the stored xray client with the email, behind
// the service's back: an edit in the inbound, or data older than #186.
func setXrayTgId(t *testing.T, inboundId int, email string, tgId int64) {
	t.Helper()
	db := database.GetDB()
	var ib model.Inbound
	if err := db.First(&ib, inboundId).Error; err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, raw := range settings["clients"].([]any) {
		if c := raw.(map[string]any); c["email"] == email {
			c["tgId"], found = tgId, true
		}
	}
	if !found {
		t.Fatalf("no client %s in inbound %d", email, inboundId)
	}
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := db.Model(&model.Inbound{}).Where("id = ?", inboundId).Update("settings", string(out)).Error; err != nil {
		t.Fatal(err)
	}
}

// telegramConflictBot: anna owns 202; ivan has none, and his clients say 201
// (the bot saw that account as @ivan_tg) and 202.
func telegramConflictBot(t *testing.T) (*Tgbot, *SubUserView) {
	t.Helper()
	bot := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "anna", TgId: 202, InboundIds: []int{1}})
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 2}})
	setXrayTgId(t, 1, "ivan-NL-Amsterdam-1", 201)
	setXrayTgId(t, 2, "ivan-de", 202)
	if _, err := writeTgAccount(model.TgAccount{TgId: 201, Username: "ivan_tg", FirstName: "Ivan"}); err != nil {
		t.Fatal(err)
	}
	return bot, ivan
}

// TestUserCardResolvesATelegramConflict (#186 point 8): the card of a user
// in conflict says so; its Telegram screen lists the ids of the clients with
// their owners, offers «Assign» only for an id nobody else owns, and the
// assign writes it onto the user and all its clients.
func TestUserCardResolvesATelegramConflict(t *testing.T) {
	bot, ivan := telegramConflictBot(t)

	card := press(t, bot, "usr_c "+ivan.SubId)
	if !strings.Contains(card.text, "⚠️ Telegram: conflict") {
		t.Errorf("card:\n%s", card.text)
	}
	screen := press(t, bot, button(t, card.keyboard, "⚠️ Telegram"))
	if screen.route != "usr_tg "+ivan.SubId {
		t.Errorf("route %q", screen.route)
	}
	for _, want := range []string{"<code>201</code> (@ivan_tg) — ivan-NL-Amsterdam-1", "<code>202</code> — ivan-de", "owned by user anna"} {
		if !strings.Contains(screen.text, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen.text)
		}
	}
	labels := buttonTexts(t, screen.keyboard)
	if !slices.Contains(labels, "Assign 201") || slices.Contains(labels, "Assign 202") || !slices.Contains(labels, "Unlink Telegram") {
		t.Errorf("buttons: %q", labels)
	}

	// anna's id is refused even when pressed from an old keyboard.
	refused := press(t, bot, "usr_tga "+ivan.SubId+" 202")
	if !strings.Contains(refused.text, "Telegram id 202 belongs to user anna") {
		t.Errorf("refusal:\n%s", refused.text)
	}

	done := press(t, bot, button(t, screen.keyboard, "Assign 201"))
	if done.toast == "" || strings.Contains(done.text, "conflict") || !strings.Contains(done.text, "📱 Telegram: 201") {
		t.Errorf("after the assign: %q\n%s", done.toast, done.text)
	}
	v := mustGetUser(t, ivan.SubId)
	if v.TgId != 201 || v.TgConflict {
		t.Errorf("ivan: tgId %d conflict %v", v.TgId, v.TgConflict)
	}
	for _, c := range v.Clients {
		if c.TgId != 201 {
			t.Errorf("client %s: tgId %d", c.Name, c.TgId)
		}
	}
	if !slices.Contains(buttonTexts(t, done.keyboard), "📱 Telegram") {
		t.Errorf("card buttons: %q", buttonTexts(t, done.keyboard))
	}
}

// TestUserCardUnlinksTelegram (#186 point 9): «Unlink Telegram» asks first,
// then leaves the user and all its clients without an id.
func TestUserCardUnlinksTelegram(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 201, InboundIds: []int{1, 5}})

	screen := press(t, bot, "usr_tg "+ivan.SubId)
	ask := press(t, bot, button(t, screen.keyboard, "Unlink Telegram"))
	if ask.route != "usr_tgu "+ivan.SubId || !strings.Contains(ask.text, "Unlink Telegram from user ivan?") {
		t.Errorf("question: %+v", ask)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != 201 {
		t.Fatalf("the question unlinked: %d", v.TgId)
	}
	done := press(t, bot, button(t, ask.keyboard, "Confirm"))
	if done.toast == "" || strings.Contains(done.text, "Telegram: 201") {
		t.Errorf("after the unlink: %q\n%s", done.toast, done.text)
	}
	v := mustGetUser(t, ivan.SubId)
	for _, c := range v.Clients {
		if c.TgId != 0 {
			t.Errorf("client %s kept %d", c.Name, c.TgId)
		}
	}
	if v.TgId != 0 {
		t.Errorf("ivan kept %d", v.TgId)
	}
	// With no id and no conflict, the card offers no Telegram screen.
	if slices.ContainsFunc(buttonTexts(t, done.keyboard), func(s string) bool { return strings.Contains(s, "Telegram") }) {
		t.Errorf("card buttons: %q", buttonTexts(t, done.keyboard))
	}
}

// TestNonAdminCannotResolveTelegram: someone who is no admin — even the
// account of the user itself — gets «No result» for the Telegram screen and
// its buttons, and the user keeps its id.
func TestNonAdminCannotResolveTelegram(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)
	mine := mustGetUser(t, "s-mine")
	if mine.TgId != usersTestChat {
		t.Fatalf("s-mine: tgId %d", mine.TgId)
	}
	for _, data := range []string{"usr_tg s-mine", "usr_tga s-mine 1", "usr_tgu s-mine", "usr_tguc s-mine"} {
		if tg.clientMayPress(&telego.CallbackQuery{From: telego.User{ID: usersTestChat}, Data: data}) {
			t.Errorf("%q let through", data)
		}
		fake.calls = nil
		nonAdminPress(tg, data)
		if len(fake.calls) != 1 || fake.calls[0].params["text"] != "❗ No result!" {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
	}
	if v := mustGetUser(t, "s-mine"); v.TgId != usersTestChat {
		t.Errorf("s-mine: tgId %d", v.TgId)
	}
}

// TestTelegramScreenSpeaksRussian: the words the owner chose.
func TestTelegramScreenSpeaksRussian(t *testing.T) {
	bot, ivan := telegramConflictBot(t)
	initTestBotLocale(t, "ru-RU")

	card := press(t, bot, "usr_c "+ivan.SubId)
	if !strings.Contains(card.text, "⚠️ Telegram: конфликт") {
		t.Errorf("card:\n%s", card.text)
	}
	screen := press(t, bot, "usr_tg "+ivan.SubId)
	labels := buttonTexts(t, screen.keyboard)
	if !slices.Contains(labels, "Назначить 201") || !slices.Contains(labels, "Отвязать Telegram") ||
		!strings.Contains(screen.text, "занят пользователем anna") {
		t.Errorf("screen:\n%s\n%q", screen.text, labels)
	}
}

// TestTelegramTextsInEveryLanguage: every translation file carries the
// [tgbot.tgaccount] keys.
func TestTelegramTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Account map[string]string `toml:"tgaccount"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Account {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.tgaccount] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.tgaccount] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
