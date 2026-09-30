package service

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// opsCallbacks are the callback data of #192's screens.
var opsCallbacks = []string{screenOnlineRoute, screenReportsRoute, screenTrafficRoute, screenRunningOutRoute,
	screenMonitoringRoute, screenEventsRoute, screenServerRoute, screenRestartAsk, screenRestartData,
	screenResetAllAsk, screenResetAllData, screenAdminLinkData}

// TestNonAdminCannotReachOpsScreens: the screens are admins' only — a
// sender who is not an admin gets «no result» for every one of their
// buttons, and nothing changes.
func TestNonAdminCannotReachOpsScreens(t *testing.T) {
	tg := accessBotFixture(t)
	fake := withFakeTelegram(t)
	restarts := 0
	prev := screenRestartXray
	screenRestartXray = func(*Tgbot) (bool, error) { restarts++; return true, nil }
	t.Cleanup(func() { screenRestartXray = prev })
	before := botState(t)

	for _, action := range opsCallbacks {
		for _, data := range []string{action, action + " 0", action + " 1"} {
			fake.calls = nil
			nonAdminPress(tg, data)
			if len(fake.calls) != 1 || fake.calls[0].method != "answerCallbackQuery" ||
				fake.calls[0].params["text"] != "❗ No result!" {
				t.Errorf("%q: the bot said\n%s", data, fake.texts())
			}
		}
	}
	if after := botState(t); after != before || restarts != 0 {
		t.Errorf("the bot's state changed (%d restarts):\n before %s\n after  %s", restarts, before, after)
	}
}

// TestOpsCallbacksFitTelegram: every button of the screens keeps its
// callback data within Telegram's 64 bytes.
func TestOpsCallbacksFitTelegram(t *testing.T) {
	tg := reportsFixture(t)
	mustCreateUser(t, SubUserCreate{Name: strings.Repeat("l", 60), SubId: strings.Repeat("s", 60), InboundIds: []int{2},
		SubUserParams: SubUserParams{TotalGB: 1}})
	markOnline(t, strings.Repeat("l", 60)+"-de")
	tg.setCachedServerStats("status")
	t.Cleanup(func() { tg.setCachedServerStats("") })

	for _, data := range []string{"s_onl 0", "s_rep", "s_rtr 0", "s_rdp 0", "s_srv", "s_rx", "s_rall"} {
		reply := screenPressData(t, tg, data)
		if reply.text == "" {
			t.Errorf("%q shows nothing: %+v", data, reply)
		}
		sc := &botScreen{route: "x", back: []string{screenMenuRoute}}
		buttons(t, tg.screenKeyboard(sc, reply.keyboard)) // fails on data over 64 bytes
	}
}

// TestOpsScreensSpeakRussian: the screens render in the bot's language.
func TestOpsScreensSpeakRussian(t *testing.T) {
	tg := reportsFixture(t)
	initTestBotLocale(t, "ru-RU")
	tg.setCachedServerStats("status")
	t.Cleanup(func() { tg.setCachedServerStats("") })

	for data, want := range map[string]string{
		"s_rep":   "⏳ Скоро закончатся (2)",
		"s_rdp 0": "срок через 2 дн.",
		"s_onl 0": "Онлайн сейчас: 0",
		"s_srv":   "♻️ Сбросить весь трафик",
		"s_rx":    "Перезапустить xray?",
	} {
		reply := screenPressData(t, tg, data)
		if got := reply.text + strings.Join(buttonTexts(t, reply.keyboard), "|"); !strings.Contains(got, want) {
			t.Errorf("%s: want %q in %q", data, want, got)
		}
	}
}

// TestOpsTextsInEveryLanguage: every translation file carries the keys of
// [tgbot.ops], so no language renders an empty screen.
func TestOpsTextsInEveryLanguage(t *testing.T) {
	keys := func(file string) []string {
		raw, err := os.ReadFile(filepath.Join("..", "translation", file))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tgbot struct {
				Ops map[string]string `toml:"ops"`
			} `toml:"tgbot"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for k, v := range doc.Tgbot.Ops {
			if strings.TrimSpace(v) != "" {
				out = append(out, k)
			}
		}
		slices.Sort(out)
		return out
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("no [tgbot.ops] in en_US")
	}
	entries, err := os.ReadDir(filepath.Join("..", "translation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if got := keys(e.Name()); !slices.Equal(got, want) {
			t.Errorf("%s: [tgbot.ops] keys\n got %q\nwant %q", e.Name(), got, want)
		}
	}
}
