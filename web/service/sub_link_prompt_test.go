package service

import (
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The question to the admins (#222): a burst of changes makes one question
// once it has settled, in each admin's private chat; nothing goes to the
// users before an admin says «Yes».

// linkPromptFixture is linkFixture with the bot on a linkTelegram and the
// watcher's clock at noon.
func linkPromptFixture(t *testing.T) (*Tgbot, *linkTelegram, *[]time.Duration, *time.Time, *SubUserView) {
	t.Helper()
	tg, ivan, _ := linkFixture(t)
	fake, slept := withLinkTelegram(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	linkClock(t, &now)
	return tg, fake, slept, &now, ivan
}

// promptOf is the question in the admin's chat.
func promptOf(t *testing.T, fake *linkTelegram, admin int64) linkCall {
	t.Helper()
	for _, c := range fake.to(admin) {
		if c.method == "sendMessage" && strings.Contains(c.text, "Subscription links changed") {
			return c
		}
	}
	t.Fatalf("admin %d was not asked: %+v", admin, fake.to(admin))
	return linkCall{}
}

// reportTo is the last broadcast report in the admin's chat.
func reportTo(t *testing.T, fake *linkTelegram, admin int64) linkCall {
	t.Helper()
	calls := fake.to(admin)
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].method == "sendMessage" && strings.Contains(calls[i].text, "Link broadcast #") {
			return calls[i]
		}
	}
	t.Fatalf("admin %d got no report: %+v", admin, calls)
	return linkCall{}
}

func countAsked(fake *linkTelegram, admin int64) int {
	n := 0
	for _, c := range fake.to(admin) {
		if c.method == "sendMessage" && strings.Contains(c.text, "Subscription links changed") {
			n++
		}
	}
	return n
}

// TestSubLinkPromptWaitsForTheBurstToSettle: changes a minute apart make one
// question, two minutes after the last of them, in every admin's chat; the
// users get nothing.
func TestSubLinkPromptWaitsForTheBurstToSettle(t *testing.T) {
	tg, fake, _, now, _ := linkPromptFixture(t)

	setSetting(t, "subPath", "/feed/")
	tg.CheckSubLinks()
	*now = now.Add(time.Minute)
	setSetting(t, "subPort", "2443")
	tg.CheckSubLinks()
	*now = now.Add(time.Minute + 30*time.Second)
	tg.CheckSubLinks()
	if countAsked(fake, linkAdmin) != 0 {
		t.Fatalf("asked before the burst settled: %+v", fake.to(linkAdmin))
	}
	*now = now.Add(31 * time.Second)
	tg.CheckSubLinks()
	for _, admin := range []int64{linkAdmin, linkAdmin2} {
		p := promptOf(t, fake, admin)
		for _, want := range []string{"Send the new link to 1 users?", "the subscriptions' path", "the front's address or port",
			"Without Telegram: 2"} {
			if !strings.Contains(p.text, want) {
				t.Errorf("admin %d: the question %q lacks %q", admin, p.text, want)
			}
		}
		if strings.Join(p.labels, "|") != "✅ Yes|✖ No" {
			t.Errorf("buttons: %q", p.labels)
		}
	}
	*now = now.Add(10 * time.Minute)
	tg.CheckSubLinks()
	if n := countAsked(fake, linkAdmin); n != 1 {
		t.Errorf("asked %d times", n)
	}
	if got := fake.to(linkPerson); len(got) != 0 {
		t.Errorf("ivan got %+v before anyone said yes", got)
	}
}

// settle makes the current changes a question: two looks two minutes apart.
func settle(tg *Tgbot, now *time.Time) {
	tg.CheckSubLinks()
	*now = now.Add(subLinkQuiet)
	tg.CheckSubLinks()
}

// TestSubLinkPromptNo: «No» sends nothing, the change is settled, and the
// other admin's question says who answered.
func TestSubLinkPromptNo(t *testing.T) {
	tg, fake, _, now, _ := linkPromptFixture(t)
	setSetting(t, "subPath", "/feed/")
	settle(tg, now)
	p := promptOf(t, fake, linkAdmin)

	linkPress(tg, linkAdmin, linkAdmin, 0, p.button(t, "No"))
	if got := fake.to(linkPerson); len(got) != 0 {
		t.Errorf("ivan got %+v", got)
	}
	if changes := mustScan(t, tg); len(changes.users) != 0 {
		t.Errorf("the change stays: %+v", changes)
	}
	if last := fake.lastTo(t, linkAdmin2); last.method != "editMessageText" || !strings.Contains(last.text, "Not sent") {
		t.Errorf("the other admin's question: %+v", last)
	}
	*now = now.Add(time.Hour)
	tg.CheckSubLinks()
	if n := countAsked(fake, linkAdmin); n != 1 {
		t.Errorf("asked again: %d", n)
	}
	var n int64
	database.GetDB().Model(&model.SubLinkBroadcast{}).Count(&n)
	if n != 0 {
		t.Errorf("a «No» made %d journal rows", n)
	}
}

// TestSubLinkPromptYes: «Yes» sends ivan the new link and the admin who
// said it the report; the journal has the broadcast with its reason.
func TestSubLinkPromptYes(t *testing.T) {
	tg, fake, _, now, ivan := linkPromptFixture(t)
	setSetting(t, "subPath", "/feed/")
	settle(tg, now)
	p := promptOf(t, fake, linkAdmin2)

	linkPress(tg, linkAdmin2, linkAdmin2, 0, p.button(t, "Yes"))
	link := "http://localhost:2096/feed/" + ivan.SubId
	got := fake.to(linkPerson)
	if len(got) != 1 || got[0].method != "sendPhoto" || !strings.Contains(got[0].text, link) {
		t.Fatalf("ivan got %+v", got)
	}
	if report := reportTo(t, fake, linkAdmin2); !strings.Contains(report.text, "Sent: 1") {
		t.Errorf("report: %q", report.text)
	}
	var b model.SubLinkBroadcast
	if err := database.GetDB().Last(&b).Error; err != nil || b.Trigger != model.SubLinkTriggerAuto ||
		b.Reasons != model.SubLinkReasonSubPath || b.StartedBy != "2" || b.Sent != 1 || b.NoTelegram != 2 || b.FinishedAt == 0 {
		t.Errorf("journal: %+v %v", b, err)
	}
	if changes := mustScan(t, tg); len(changes.users) != 0 {
		t.Errorf("the change stays: %+v", changes)
	}
}

// TestSubLinkPromptOutdated: a change after the question makes a new one,
// and the old question's «Yes» sends nothing.
func TestSubLinkPromptOutdated(t *testing.T) {
	tg, fake, _, now, _ := linkPromptFixture(t)
	setSetting(t, "subPath", "/feed/")
	settle(tg, now)
	old := promptOf(t, fake, linkAdmin)
	setSetting(t, "subPath", "/feed2/")
	settle(tg, now)
	if n := countAsked(fake, linkAdmin); n != 2 {
		t.Fatalf("asked %d times", n)
	}

	linkPress(tg, linkAdmin, linkAdmin, 0, old.button(t, "Yes"))
	if got := fake.to(linkPerson); len(got) != 0 {
		t.Errorf("ivan got %+v", got)
	}
	if toasts := fake.toasts(); len(toasts) == 0 || !strings.Contains(toasts[len(toasts)-1], "outdated") {
		t.Errorf("toasts: %q", toasts)
	}
}

// TestSubLinkPromptIsTheAdminsOnly: someone who is no admin pressing the
// question's buttons gets «No result», and nothing is sent or settled.
func TestSubLinkPromptIsTheAdminsOnly(t *testing.T) {
	tg, fake, _, now, _ := linkPromptFixture(t)
	setSetting(t, "subPath", "/feed/")
	settle(tg, now)
	p := promptOf(t, fake, linkAdmin)

	for _, label := range []string{"Yes", "No"} {
		linkPress(tg, linkPerson, linkPerson, 0, p.button(t, label))
	}
	if got := fake.to(linkPerson); len(got) != 0 {
		t.Errorf("ivan got %+v", got)
	}
	if toasts := fake.toasts(); len(toasts) != 2 || toasts[0] != "❗ No result!" {
		t.Errorf("toasts: %q", toasts)
	}
	if changes := mustScan(t, tg); len(changes.users) != 1 {
		t.Errorf("the change was settled: %+v", changes)
	}
}
