package service

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/pelletier/go-toml/v2"
)

// The broadcast's queue, report and journal (#222) on a fake Bot API: the
// pace, a 429 waited out, a person who blocked the bot, a refusal, the
// users without Telegram, the line about a new .conf only when it changed.

const (
	linkBlocked = int64(503) // oleg blocked the bot
	linkGone    = int64(504) // pavel's chat is not found
	linkOff     = int64(505) // dina is disabled
)

type linkQueueUsers struct {
	ivan, maria, oleg, pavel, anna, dina *SubUserView
}

func linkQueueFixture(t *testing.T) (*Tgbot, *linkTelegram, *[]time.Duration, linkQueueUsers) {
	t.Helper()
	tg := usersBotFixture(t)
	var u linkQueueUsers
	u.ivan = mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: linkPerson, InboundIds: []int{2}})
	u.maria = mustCreateUser(t, SubUserCreate{Name: "maria", TgId: linkPerson2, InboundIds: []int{2, 5}})
	u.oleg = mustCreateUser(t, SubUserCreate{Name: "oleg", TgId: linkBlocked, InboundIds: []int{2}})
	u.pavel = mustCreateUser(t, SubUserCreate{Name: "pavel", TgId: linkGone, InboundIds: []int{2}})
	u.anna = mustCreateUser(t, SubUserCreate{Name: "anna", InboundIds: []int{2}})
	u.dina = mustCreateUser(t, SubUserCreate{Name: "dina", TgId: linkOff, InboundIds: []int{2}})
	if _, err := (&SubUserService{}).SetEnable(u.dina.SubId, false); err != nil {
		t.Fatal(err)
	}
	fake, slept := withLinkTelegram(t)
	fake.refuse[linkPerson2] = []*ta.Error{{ErrorCode: 429, Description: "Too Many Requests: retry after 3",
		Parameters: &ta.ResponseParameters{RetryAfter: 3}}}
	fake.refuse[linkBlocked] = []*ta.Error{{ErrorCode: 403, Description: "Forbidden: bot was blocked by the user"}}
	fake.refuse[linkGone] = []*ta.Error{{ErrorCode: 400, Description: "Bad Request: chat not found"}}
	return tg, fake, slept, u
}

// TestSubLinkQueue: everyone with Telegram gets one message — the link and
// «My subscription», no QR picture and no files (#245); maria after the
// 429's retry_after, told that her AWG .conf is new; oleg is recorded as
// having blocked the bot; the calls keep ~25 a second.
func TestSubLinkQueue(t *testing.T) {
	tg, fake, slept, u := linkQueueFixture(t)

	started, err := tg.subLinkStartAll(model.SubLinkTriggerAll, "1", []int64{linkAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if started.Recipients != 4 || started.NoTelegram != 2 { // anna and the fixture's other-nl
		t.Errorf("started: %+v", started)
	}

	got := fake.to(linkPerson)
	if len(got) != 1 || got[0].method != "sendMessage" || got[0].file != "" {
		t.Fatalf("ivan got %+v", got)
	}
	msg := got[0]
	for _, want := range []string{"Your subscription link has changed", "http://localhost:2096/sub/" + u.ivan.SubId,
		"Update the subscription in your app", "delete the subscription and add it again"} {
		if !strings.Contains(msg.text, want) {
			t.Errorf("ivan's message %q lacks %q", msg.text, want)
		}
	}
	if strings.Contains(msg.text, "QR") || strings.Contains(msg.text, ".conf") {
		t.Errorf("ivan, with xray clients only, is told of a QR or a .conf: %q", msg.text)
	}
	mySub := msg.button(t, "📱 My subscription")
	if !tg.clientMayPress(&telego.CallbackQuery{From: telego.User{ID: linkPerson}, Data: mySub}) {
		t.Errorf("ivan may not press %q", mySub)
	}

	got = fake.to(linkPerson2)
	if len(got) != 1 || got[0].method != "sendMessage" || got[0].file != "" ||
		!strings.Contains(got[0].text, "http://localhost:2096/sub/"+u.maria.SubId) ||
		!strings.Contains(got[0].text, "There is a new .conf for AmneziaWG/WireGuard") {
		t.Errorf("maria got %+v", got)
	}
	for _, chat := range []int64{linkBlocked, linkGone, linkOff} {
		if got := fake.to(chat); len(got) != 0 {
			t.Errorf("chat %d got %+v", chat, got)
		}
	}

	// The pace: 40 ms before every call but the first; 3 s for the 429.
	calls, paced, waited := 0, 0, false
	for _, c := range fake.calls {
		if strings.HasPrefix(c.method, "send") && c.chat != linkAdmin {
			calls++
		}
	}
	for _, d := range *slept {
		switch d {
		case subLinkInterval:
			paced++
		case 3 * time.Second:
			waited = true
		}
	}
	if paced != calls-1 || !waited {
		t.Errorf("%d calls, paused %v", calls, *slept)
	}
	if subLinkInterval > time.Second/25 {
		t.Errorf("the pace %v is slower than 25 a second", subLinkInterval)
	}

	// The journal.
	var b model.SubLinkBroadcast
	if err := database.GetDB().First(&b, started.Id).Error; err != nil || b.Trigger != model.SubLinkTriggerAll ||
		b.StartedBy != "1" || b.Sent != 2 || b.Blocked != 1 || b.Failed != 1 || b.NoTelegram != 2 || b.FinishedAt == 0 {
		t.Errorf("broadcast: %+v %v", b, err)
	}
	var deliveries []model.SubLinkDelivery
	database.GetDB().Where("broadcast_id = ?", b.Id).Order("name").Find(&deliveries)
	status := map[string]string{}
	for _, d := range deliveries {
		status[d.Name] = d.Status + " " + d.Error
	}
	want := map[string]string{"anna": "no_telegram ", "ivan": "sent ", "maria": "sent ", "other-nl": "no_telegram ",
		"oleg": "blocked Forbidden: bot was blocked by the user", "pavel": "failed Bad Request: chat not found"}
	for name, s := range want {
		if status[name] != s {
			t.Errorf("%s: %q, want %q", name, status[name], s)
		}
	}
	if len(status) != len(want) {
		t.Errorf("deliveries: %q", status)
	}

	// maria's .conf did not change since: the next broadcast tells her the
	// link alone.
	if _, err := tg.subLinkStartAll(model.SubLinkTriggerAll, "1", []int64{linkAdmin}); err != nil {
		t.Fatal(err)
	}
	if got := fake.to(linkPerson2); len(got) != 2 || got[1].method != "sendMessage" || strings.Contains(got[1].text, ".conf") {
		t.Errorf("maria's second broadcast: %+v", got)
	}
}

// TestSubLinkReport: the admin who started the broadcast gets the report —
// sent, not sent with the reasons, without Telegram — and «📋 Links for
// manual sending» gives the links of those it did not reach, to an admin
// only.
func TestSubLinkReport(t *testing.T) {
	tg, fake, _, u := linkQueueFixture(t)
	started, err := tg.subLinkStartAll(model.SubLinkTriggerAll, "1", []int64{linkAdmin})
	if err != nil {
		t.Fatal(err)
	}
	report := reportTo(t, fake, linkAdmin)
	for _, want := range []string{"Link broadcast #" + strconv.FormatInt(started.Id, 10) + "</b> is over", "✅ Sent: 2",
		"❌ Not sent: 2", "• oleg — 🚫 blocked the bot", "• pavel — Bad Request: chat not found",
		"📵 Without Telegram: 2", "anna, other-nl"} {
		if !strings.Contains(report.text, want) {
			t.Errorf("the report %q lacks %q", report.text, want)
		}
	}
	if len(fake.to(linkAdmin2)) != 0 {
		t.Errorf("the other admin got %+v", fake.to(linkAdmin2))
	}
	manual := report.button(t, "📋 Links for manual sending")

	linkPress(tg, linkPerson, linkPerson, 0, manual)
	if toasts := fake.toasts(); len(toasts) != 1 || toasts[0] != "❗ No result!" || len(fake.to(linkPerson)) != 1 {
		t.Errorf("ivan pressed it: %q %+v", toasts, fake.to(linkPerson))
	}

	linkPress(tg, linkAdmin, linkAdmin, 0, manual)
	links := fake.lastTo(t, linkAdmin).text
	for _, v := range []*SubUserView{u.oleg, u.pavel, u.anna} {
		if !strings.Contains(links, "<b>"+v.Name+"</b>\r\n<code>http://localhost:2096/sub/"+v.SubId+"</code>") {
			t.Errorf("the links %q lack %s's", links, v.Name)
		}
	}
	if strings.Contains(links, u.ivan.SubId) || strings.Contains(links, u.maria.SubId) {
		t.Errorf("the links name those who got theirs: %q", links)
	}
}

// linkTap taps the button labelled label on the admin's screen.
func linkTap(t *testing.T, tg *Tgbot, fake *linkTelegram, label string) linkCall {
	t.Helper()
	adminPress(tg, fake.lastTo(t, usersTestChat).button(t, label))
	return fake.lastTo(t, usersTestChat)
}

func journalRows(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := database.GetDB().Model(&model.SubLinkBroadcast{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSubLinkFromTheServerScreen: «⚙️ Server» → «📣 Send links» shows who
// the link reaches and the journal; «Send to all» asks first, then starts
// the broadcast, and the journal lists it.
func TestSubLinkFromTheServerScreen(t *testing.T) {
	tg, fake, _, _ := linkQueueFixture(t)
	adminPress(tg, screenServerRoute)
	screen := linkTap(t, tg, fake, "📣 Send links")
	for _, want := range []string{"Subscription links", "enabled users with Telegram: 4", "Without Telegram: 2", "No broadcasts yet."} {
		if !strings.Contains(screen.text, want) {
			t.Errorf("the screen %q lacks %q", screen.text, want)
		}
	}
	ask := linkTap(t, tg, fake, "📣 Send to all (4)")
	if !strings.Contains(ask.text, "Send the new link to 4 users?") || journalRows(t) != 0 {
		t.Fatalf("the question: %q", ask.text)
	}
	done := linkTap(t, tg, fake, "Confirm")
	if !strings.Contains(done.text, "Broadcast #1 started: 4 users") || !strings.Contains(done.text, "#1 · ") ||
		!strings.Contains(done.text, "to all · ✅2 ❌2 📵2") {
		t.Errorf("after the start: %q", done.text)
	}
	if len(fake.to(linkPerson)) != 1 || !strings.Contains(reportTo(t, fake, usersTestChat).text, "✅ Sent: 2") {
		t.Errorf("ivan %+v", fake.to(linkPerson))
	}
}

// TestSubLinkFromTheUserCard: a user with Telegram, enabled, has «📣 Send
// the link» on the card; it asks, then sends that user alone the link.
// Without Telegram or disabled — no button.
func TestSubLinkFromTheUserCard(t *testing.T) {
	tg, fake, _, u := linkQueueFixture(t)
	for _, v := range []*SubUserView{u.anna, u.dina} {
		adminPress(tg, "usr_c "+v.SubId)
		if card := fake.lastTo(t, usersTestChat); strings.Contains(strings.Join(card.labels, "|"), "Send the link") {
			t.Errorf("%s's card: %q", v.Name, card.labels)
		}
	}
	adminPress(tg, "usr_c "+u.ivan.SubId)
	ask := linkTap(t, tg, fake, "📣 Send the link")
	if !strings.Contains(ask.text, "Send ivan the subscription link?") || len(fake.to(linkPerson)) != 0 {
		t.Fatalf("the question: %q", ask.text)
	}
	card := linkTap(t, tg, fake, "Confirm")
	if !strings.Contains(card.text, "Broadcast #1 started: 1 users") || !strings.Contains(card.text, "<b>ivan</b>") {
		t.Errorf("the card after: %q", card.text)
	}
	if got := fake.to(linkPerson); len(got) != 1 || got[0].method != "sendMessage" {
		t.Errorf("ivan got %+v", got)
	}
	for _, chat := range []int64{linkPerson2, linkBlocked, linkGone} {
		if len(fake.to(chat)) != 0 {
			t.Errorf("chat %d got %+v", chat, fake.to(chat))
		}
	}
	var b model.SubLinkBroadcast
	if database.GetDB().Last(&b); b.Trigger != model.SubLinkTriggerUser || b.Sent != 1 || b.NoTelegram != 0 {
		t.Errorf("journal: %+v", b)
	}
}

// TestSubLinkScreensAreTheAdminsOnly: someone who is no admin gets «No
// result» on every button of the broadcast, and nothing is sent.
func TestSubLinkScreensAreTheAdminsOnly(t *testing.T) {
	tg, fake, _, u := linkQueueFixture(t)
	datas := []string{subLinkScreenRoute, subLinkAllAsk, subLinkAllData, subLinkUserAsk + " " + u.ivan.SubId,
		subLinkUserData + " " + u.ivan.SubId, subLinkYesAction + " 1", subLinkNoAction + " 1", subLinkManualAction + " 1"}
	for _, data := range datas {
		linkPress(tg, linkPerson, linkPerson, 1, data)
	}
	toasts := fake.toasts()
	if len(toasts) != len(datas) {
		t.Fatalf("toasts: %q", toasts)
	}
	for i, toast := range toasts {
		if toast != "❗ No result!" {
			t.Errorf("%s: %q", datas[i], toast)
		}
	}
	if journalRows(t) != 0 || len(fake.to(linkPerson)) != 0 {
		t.Errorf("sent: %d broadcasts, ivan %+v", journalRows(t), fake.to(linkPerson))
	}
}

// TestSubLinkTextsInEveryLanguage: every translation file carries the
// broadcast's keys, the bot's and the users page's, and the Russian ones
// read as the decision on #214.
func TestSubLinkTextsInEveryLanguage(t *testing.T) {
	read := func(file string) map[string]string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Sublink map[string]any `toml:"sublink"`
			} `toml:"tgbot"`
			Pages struct {
				SubUsers struct {
					Sublink map[string]string `toml:"sublink"`
				} `toml:"subUsers"`
			} `toml:"pages"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		out := map[string]string{}
		for k, v := range doc.Tgbot.Sublink {
			switch v := v.(type) {
			case string:
				out[k] = v
			case map[string]any:
				for sub, s := range v {
					out[k+"."+sub], _ = s.(string)
				}
			}
		}
		for k, v := range doc.Pages.SubUsers.Sublink {
			out["page."+k] = v
		}
		return out
	}
	names := func(m map[string]string) []string {
		var out []string
		for k, v := range m {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := names(read("translate.en_US.toml"))
	if len(want) < 40 {
		t.Fatalf("en_US: %q", want)
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := names(read(e.Name())); !slices.Equal(got, want) {
			t.Errorf("%s: keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
	ru := read("translate.ru_RU.toml")
	for key, part := range map[string]string{"askAll": "Разослать {{ .Count }} пользователям?", "yes": "Да", "no": "Нет",
		"message": "Ссылка на подписку обновилась", "mySub": "Моя подписка", "manual": "📋 Ссылки для ручной отправки",
		"button": "📣 Разослать ссылки", "reportNoTg": "Без Telegram", "blocked": "заблокировал бота"} {
		if !strings.Contains(ru[key], part) {
			t.Errorf("ru %s = %q, want %q in it", key, ru[key], part)
		}
	}
}
