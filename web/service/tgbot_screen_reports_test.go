package service

import (
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// useTraffic sets the traffic an xray client has used, in bytes.
func useTraffic(t *testing.T, email string, up, down int64) {
	t.Helper()
	if err := database.GetDB().Model(&xray.ClientTraffic{}).Where("email = ?", email).
		Updates(map[string]any{"up": up, "down": down}).Error; err != nil {
		t.Fatal(err)
	}
}

// reportsFixture: maria has used 49.5 of 50 GB, ivan 3 GB without a limit
// and runs out in two days, boris 1 GB of 100 with a year to go; the
// thresholds are 1 GB and 3 days.
func reportsFixture(t *testing.T) *Tgbot {
	t.Helper()
	tg := usersBotFixture(t)
	const gb = int64(1) << 30
	mustCreateUser(t, SubUserCreate{Name: "maria", InboundIds: []int{2}, SubUserParams: SubUserParams{TotalGB: 50 * gb}})
	mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2},
		SubUserParams: SubUserParams{ExpiryTime: time.Now().Add(2 * 24 * time.Hour).UnixMilli()}})
	mustCreateUser(t, SubUserCreate{Name: "boris", InboundIds: []int{2},
		SubUserParams: SubUserParams{TotalGB: 100 * gb, ExpiryTime: time.Now().Add(365 * 24 * time.Hour).UnixMilli()}})
	useTraffic(t, "maria-de", 9*gb, 40*gb+gb/2)
	useTraffic(t, "ivan-de", gb, 2*gb)
	useTraffic(t, "boris-de", 0, gb)
	setSetting(t, "trafficDiff", "1")
	setSetting(t, "expireDiff", "3")
	return tg
}

// TestScreenReports: «📊 Reports» sums the users' traffic with the top
// three, and leads to the traffic by user and to who runs out soon.
func TestScreenReports(t *testing.T) {
	tg := reportsFixture(t)
	fake := withScreenTelegram(t)
	screen := func() *screenMessage { return fake.messages[1] }

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "📊 Reports")
	for _, want := range []string{"<b>Reports</b>", "Users' traffic: 53.5 GB", "Top: maria 49.5 · ivan 3 · boris 1"} {
		if !strings.Contains(screen().text, want) {
			t.Errorf("reports: want %q in %q", want, screen().text)
		}
	}
	want := []string{"📈 Traffic by user", "⏳ Running out soon (2)", "⬅️ Back", "🏠 Menu"}
	if got := screen().labels; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("reports buttons:\n got %q\nwant %q", got, want)
	}

	fake.press(t, tg, 1, "📈 Traffic by user")
	if want := "1. maria 49.5/50 GB\n2. ivan 3/∞\n3. boris 1/100 GB"; !strings.Contains(screen().text, want) {
		t.Errorf("traffic by user: want %q in %q", want, screen().text)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	fake.press(t, tg, 1, "⏳ Running out soon")
	for _, want := range []string{"Running out soon", "• maria — traffic 99 %", "• ivan — expires in 2 d"} {
		if !strings.Contains(screen().text, want) {
			t.Errorf("running out: want %q in %q", want, screen().text)
		}
	}
	if strings.Contains(screen().text, "boris") {
		t.Errorf("boris is not running out: %q", screen().text)
	}
	fake.press(t, tg, 1, "maria")
	if !strings.Contains(screen().text, "<b>maria</b>") {
		t.Fatalf("maria's card: %q", screen().text)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(screen().text, "Running out soon") {
		t.Errorf("back from the card: %q", screen().text)
	}
}

// TestScreenReportsNobodyRunsOut: without users running out the list says
// so, and a paused client does not count.
func TestScreenReportsNobodyRunsOut(t *testing.T) {
	tg := reportsFixture(t)
	found, err := (&SubUserService{}).Search("ivan")
	if err != nil || len(found) != 1 {
		t.Fatal(found, err)
	}
	if _, err := (&SubUserService{}).SetEnable(found[0].SubId, false); err != nil {
		t.Fatal(err)
	}
	setSetting(t, "trafficDiff", "0")

	reply := screenPressData(t, tg, "s_rdp 0")
	if !strings.Contains(reply.text, "Nobody runs out soon") || reply.keyboard != nil {
		t.Errorf("nobody: %q %+v", reply.text, reply.keyboard)
	}
}

// TestScreenReportsPages: the traffic by user goes 20 lines a page.
func TestScreenReportsPages(t *testing.T) {
	tg := usersBotFixture(t)
	for _, name := range strings.Fields("a b c d e f g h i j k l m n o p q r s t u v") {
		mustCreateUser(t, SubUserCreate{Name: "u" + name, InboundIds: []int{2}})
	}
	first := screenPressData(t, tg, "s_rtr 0")
	if got := buttonTexts(t, first.keyboard); strings.Join(got, "|") != "1/2|▶" {
		t.Errorf("first page buttons: %q", got)
	}
	second := screenPressData(t, tg, "s_rtr 1")
	if !strings.Contains(second.text, "21. ") || strings.Contains(second.text, "\n1. ") || second.route != "s_rtr 1" {
		t.Errorf("second page: %q (route %q)", second.text, second.route)
	}
}
