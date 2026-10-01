package service

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/pelletier/go-toml/v2"
)

// TestUserCardWithoutTelegram (#187 point 3): the card of a user without
// Telegram says so and offers the invite link and the typed tg_id or @nick;
// a user with one gets neither on the card, the typed entry on its Telegram
// screen.
func TestUserCardWithoutTelegram(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1}})
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgId: 901, InboundIds: []int{1}})

	card := press(t, bot, "usr_c "+ivan.SubId)
	labels := buttonTexts(t, card.keyboard)
	if !strings.Contains(card.text, "📱 Telegram: not linked") ||
		!slices.Contains(labels, "🔗 Invite link") || !slices.Contains(labels, "✏️ Enter tg_id or @nick") {
		t.Errorf("ivan's card: %q\n%s", labels, card.text)
	}
	card = press(t, bot, "usr_c "+anna.SubId)
	labels = buttonTexts(t, card.keyboard)
	if strings.Contains(card.text, "not linked") || slices.Contains(labels, "🔗 Invite link") ||
		slices.Contains(labels, "✏️ Enter tg_id or @nick") || !slices.Contains(labels, "📱 Telegram") {
		t.Errorf("anna's card: %q\n%s", labels, card.text)
	}
	screen := press(t, bot, button(t, card.keyboard, "📱 Telegram"))
	if !slices.Contains(buttonTexts(t, screen.keyboard), "✏️ Enter tg_id or @nick") {
		t.Errorf("anna's Telegram screen: %q", buttonTexts(t, screen.keyboard))
	}
	// Robot and monitoring have no Telegram at all.
	card = press(t, bot, "usr_c "+model.SubUserRobotKey)
	if strings.Contains(card.text, "Telegram") || slices.Contains(buttonTexts(t, card.keyboard), "🔗 Invite link") {
		t.Errorf("robot's card: %q\n%s", buttonTexts(t, card.keyboard), card.text)
	}
}

// TestUserCardInviteLink (#187 point 2): «🔗 Invite link» shows the deep
// link to the bot with the user's open invite, the same one again on the
// next press; «🖼 QR» sends its QR as a picture; «🔄 Reissue» shows a new
// link and the old token links nobody.
func TestUserCardInviteLink(t *testing.T) {
	bot := usersBotFixture(t)
	withFakeTelegram(t) // the bot's @username comes from getMe
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1}})
	invites := &TgInviteService{}

	card := press(t, bot, "usr_c "+ivan.SubId)
	view := press(t, bot, button(t, card.keyboard, "🔗 Invite link"))
	inv, err := invites.Current(ivan.SubId)
	if err != nil || inv == nil {
		t.Fatalf("no open invite: %v", err)
	}
	if link := "https://t.me/test_bot?start=" + inv.Token; !strings.Contains(view.text, link) ||
		view.route != "usr_tgi "+ivan.SubId || !strings.Contains(view.text, "ivan") {
		t.Errorf("the invite view:\n%s", view.text)
	}
	if again := press(t, bot, "usr_tgi "+ivan.SubId); !strings.Contains(again.text, inv.Token) {
		t.Errorf("a second press made another link:\n%s", again.text)
	}

	qr := bot.screenRoute(usersTestChat, button(t, view.keyboard, "🖼 QR"))
	if len(qr.files) != 1 || !bytes.HasPrefix(qr.files[0].data, []byte("\x89PNG")) {
		t.Errorf("the QR: %+v", qr.files)
	}

	reissued := press(t, bot, button(t, view.keyboard, "🔄 Reissue"))
	fresh, err := invites.Current(ivan.SubId)
	if err != nil || fresh == nil || fresh.Token == inv.Token || !strings.Contains(reissued.text, fresh.Token) ||
		reissued.toast != "🔄 The old link no longer works" {
		t.Fatalf("reissue: %+v %v\n%s", fresh, err, reissued.text)
	}
	if r, err := invites.Redeem(inv.Token, 555); err != nil || r.Outcome != TgInviteUsed {
		t.Errorf("the old token: %+v, %v", r, err)
	}
}

// TestUserCardTelegramEntry (#219): «✏️ Enter tg_id or @nick» takes a
// number or the @nick of an account the bot has seen and links it; a nick
// nobody has is answered with the hint to send an invite link — with the
// button for it — and the chat waits for another try.
func TestUserCardTelegramEntry(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 2}})
	if _, err := writeTgAccount(model.TgAccount{TgId: 903, Username: "ivan_tg"}); err != nil {
		t.Fatal(err)
	}

	card := press(t, bot, "usr_c "+ivan.SubId)
	ask := press(t, bot, button(t, card.keyboard, "✏️ Enter tg_id or @nick"))
	if !strings.Contains(ask.text, "Send the Telegram id (a number) or the @nick of ivan") {
		t.Errorf("prompt:\n%s", ask.text)
	}
	unknown := typeText(t, bot, "@nobody_tg")
	if !strings.Contains(unknown.text, "@nobody_tg is unknown: the person has not written to the bot yet. Send them an invite link.") ||
		!slices.Contains(buttonTexts(t, unknown.keyboard), "🔗 Invite link") {
		t.Errorf("unknown nick: %q\n%s", buttonTexts(t, unknown.keyboard), unknown.text)
	}
	bad := typeText(t, bot, "ivan tg")
	if !strings.Contains(bad.text, "«ivan tg» is neither a Telegram id nor an @nick") {
		t.Errorf("not an entry:\n%s", bad.text)
	}
	done := typeText(t, bot, "@IVAN_TG")
	if !strings.Contains(done.text, "📱 Telegram: 903") || done.route != "usr_c "+ivan.SubId {
		t.Errorf("linked by nick: %+v", done)
	}
	for _, c := range mustGetUser(t, ivan.SubId).Clients {
		if c.TgId != 903 {
			t.Errorf("client %s: %d", c.Name, c.TgId)
		}
	}
	if _, waiting := userStates.get(usersTestChat); waiting {
		t.Error("the chat still waits for a text")
	}
}

// TestUserCardMovesTelegram (#187 point 4): an id another user has is
// refused with that user named and «➡️ Move here»; the move asks first,
// then takes the id from them and gives it to this user.
func TestUserCardMovesTelegram(t *testing.T) {
	bot := usersBotFixture(t)
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgId: 904, InboundIds: []int{1}})
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	press(t, bot, "usr_tge "+ivan.SubId)
	refused := typeText(t, bot, "904")
	if !strings.Contains(refused.text, "Telegram 904 is linked to user <b>anna</b>") {
		t.Errorf("refusal:\n%s", refused.text)
	}
	ask := press(t, bot, button(t, refused.keyboard, "➡️ Move here"))
	if !strings.Contains(ask.text, "Move Telegram 904 from anna to ivan?") {
		t.Errorf("question:\n%s", ask.text)
	}
	if v := mustGetUser(t, anna.SubId); v.TgId != 904 {
		t.Fatalf("the question moved it: anna %d", v.TgId)
	}
	done := press(t, bot, button(t, ask.keyboard, "Confirm"))
	if !strings.Contains(done.text, "📱 Telegram: 904") || done.route != "usr_c "+ivan.SubId {
		t.Errorf("after the move: %+v", done)
	}
	if v := mustGetUser(t, anna.SubId); v.TgId != 0 || v.Clients[0].TgId != 0 {
		t.Errorf("anna kept it: %+v", v)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != 904 || v.Clients[0].TgId != 904 {
		t.Errorf("ivan: %+v", v)
	}
}

// TestNonAdminCannotBindTelegram: someone who is no admin — even the user's
// own account — gets «No result» for the invite, the entry and the move, and
// no invite is made.
func TestNonAdminCannotBindTelegram(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)
	for _, data := range []string{tgInviteAction + " s-mine", tgReissueAction + " s-mine", tgInviteQRAction + " s-mine",
		tgEntryAction + " s-mine", tgMoveAction + " s-mine 1", tgMoveDoAction + " s-mine " + strconv.FormatInt(usersTestChat, 10)} {
		fake.calls = nil
		nonAdminPress(tg, data)
		if len(fake.calls) != 1 || fake.calls[0].params["text"] != "❗ No result!" {
			t.Errorf("%q: the bot said\n%s", data, fake.texts())
		}
	}
	if inv, err := (&TgInviteService{}).Current("s-mine"); err != nil || inv != nil {
		t.Errorf("an invite was made: %+v, %v", inv, err)
	}
}

// TestTgInviteSpeaksRussian: the words the owner chose (#187).
func TestTgInviteSpeaksRussian(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	initTestBotLocale(t, "ru-RU")
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgId: usersTestChat, InboundIds: []int{1}})
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})

	card := press(t, tg, "usr_c "+ivan.SubId)
	labels := buttonTexts(t, card.keyboard)
	if !strings.Contains(card.text, "📱 Telegram: не привязан") || !slices.Contains(labels, "🔗 Ссылка-приглашение") ||
		!slices.Contains(labels, "✏️ Ввести tg_id или @ник") {
		t.Errorf("card: %q\n%s", labels, card.text)
	}
	view := press(t, tg, "usr_tgi "+ivan.SubId)
	if !slices.Contains(buttonTexts(t, view.keyboard), "🔄 Перевыпустить") {
		t.Errorf("invite: %q", buttonTexts(t, view.keyboard))
	}
	press(t, tg, "usr_tge "+ivan.SubId)
	if refused := typeText(t, tg, strconv.FormatInt(usersTestChat, 10)); !slices.Contains(buttonTexts(t, refused.keyboard), "➡️ Перенести сюда") ||
		!strings.Contains(refused.text, "уже привязан к пользователю <b>anna</b>") {
		t.Errorf("refusal: %q\n%s", buttonTexts(t, refused.keyboard), refused.text)
	}

	inv, err := (&TgInviteService{}).Current(ivan.SubId)
	if err != nil || inv == nil {
		t.Fatal(err)
	}
	startFrom(tg, petrov, "/start "+inv.Token)
	if sent := strings.Join(fake.sentTo(usersTestChat), "\n"); !strings.Contains(sent,
		"Этот Telegram уже привязан к другой подписке, обратитесь к админу") {
		t.Errorf("the sender got %q", sent)
	}
	if v := mustGetUser(t, anna.SubId); v.TgId != usersTestChat {
		t.Errorf("anna: %d", v.TgId)
	}
	if _, err := (&SubUserService{}).UnlinkTelegram(anna.SubId); err != nil {
		t.Fatal(err)
	}
	startFrom(tg, petrov, "/start "+inv.Token)
	if got := fake.sentTo(testNotifyChannel); len(got) != 2 || got[1] != "🔗 ivan привязал Telegram @petrov" {
		t.Errorf("the channel got %q", got)
	}
}

// TestTgInviteTextsInEveryLanguage: every translation file carries the
// [tgbot.tginvite] and [pages.subUsers.tginvite] keys.
func TestTgInviteTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) (bot, page []string) {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Invite map[string]string `toml:"tginvite"`
			} `toml:"tgbot"`
			Pages struct {
				SubUsers struct {
					Invite map[string]string `toml:"tginvite"`
				} `toml:"subUsers"`
			} `toml:"pages"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		nonEmpty := func(m map[string]string) []string {
			var out []string
			for k, v := range m {
				if strings.TrimSpace(v) != "" {
					out = append(out, k)
				}
			}
			slices.Sort(out)
			return out
		}
		return nonEmpty(doc.Tgbot.Invite), nonEmpty(doc.Pages.SubUsers.Invite)
	}
	wantBot, wantPage := keys("translate.en_US.toml")
	if len(wantBot) == 0 || len(wantPage) == 0 {
		t.Fatal("no [tgbot.tginvite] or [pages.subUsers.tginvite] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		bot, page := keys(e.Name())
		if !slices.Equal(bot, wantBot) || !slices.Equal(page, wantPage) {
			t.Errorf("%s: keys\n got %q %q\nwant %q %q", e.Name(), bot, page, wantBot, wantPage)
		}
	}
}
