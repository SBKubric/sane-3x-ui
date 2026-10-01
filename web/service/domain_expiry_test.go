package service

import (
	"strings"
	"testing"
	"time"
)

// The domain renewal reminder (#225): 30 days, 7 days before the expiry date
// and on the day, once each, kept across restarts, on a fake clock.

func expiryClock(t *testing.T, now *time.Time) {
	t.Helper()
	prev := domainExpiryNow
	domainExpiryNow = func() time.Time { return *now }
	t.Cleanup(func() { domainExpiryNow = prev })
}

// day is noon of a date, so the panel's time zone does not move the day.
func day(t *testing.T, date string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	return d.Add(12 * time.Hour)
}

// TestDomainExpiryReminders walks the days up to the date and after it.
func TestDomainExpiryReminders(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	setSetting(t, "vpnName", "vpn.example.com")
	setSetting(t, "domainExpiry", "2027-03-01")
	now := day(t, "2027-01-01")
	expiryClock(t, &now)

	posted := func() []string { return fake.sentTo(testNotifyChannel) }
	walk := func(date string) {
		now = day(t, date)
		tg.CheckDomainExpiry()
		tg.CheckDomainExpiry() // hourly: a second look the same day posts nothing
	}

	walk("2027-01-01")
	walk("2027-01-29") // 31 days before
	if n := len(posted()); n != 0 {
		t.Fatalf("posted early: %q", posted())
	}
	walk("2027-01-30") // 30 days
	if got := posted(); len(got) != 1 || !strings.Contains(got[0], "example.com") ||
		!strings.Contains(got[0], "2027-03-01") || !strings.Contains(got[0], "30 days") {
		t.Fatalf("the 30-day reminder: %q", got)
	}
	walk("2027-02-10")
	walk("2027-02-22") // 7 days
	if got := posted(); len(got) != 2 || !strings.Contains(got[1], "7 days") {
		t.Fatalf("the 7-day reminder: %q", got)
	}
	walk("2027-02-28")
	walk("2027-03-01") // the day
	if got := posted(); len(got) != 3 || !strings.Contains(got[2], "today") {
		t.Fatalf("the day's reminder: %q", got)
	}
	walk("2027-03-02")
	walk("2027-04-01")
	if got := posted(); len(got) != 3 {
		t.Fatalf("after the date: %q", got)
	}
	if fake.sentTo(testNotifyAdmin) != nil {
		t.Errorf("the admin's chat got %q", fake.sentTo(testNotifyAdmin))
	}

	// A new date starts over.
	setSetting(t, "domainExpiry", "2028-03-01")
	walk("2028-02-23") // 7 days: only this one, not the 30-day one as well
	if got := posted(); len(got) != 4 || !strings.Contains(got[3], "7 days") || !strings.Contains(got[3], "2028-03-01") {
		t.Fatalf("a new date: %q", got)
	}
	walk("2028-02-25")
	if got := posted(); len(got) != 4 {
		t.Fatalf("the 30-day window after the 7-day reminder: %q", got)
	}
}

// TestDomainExpiryReminderSurvivesRestart: the reminders posted are in the
// database, not in memory.
func TestDomainExpiryReminderSurvivesRestart(t *testing.T) {
	tg, fake := notifyBotFixture(t, testNotifyChannel)
	setSetting(t, "domainExpiry", "2027-03-01")
	now := day(t, "2027-02-25")
	expiryClock(t, &now)
	tg.CheckDomainExpiry()
	if got := fake.sentTo(testNotifyChannel); len(got) != 1 || !strings.Contains(got[0], "4 days") {
		t.Fatalf("posted: %q", got)
	}
	// Another Tgbot value, as after a restart, reads the same state.
	(&Tgbot{}).CheckDomainExpiry()
	if got := fake.sentTo(testNotifyChannel); len(got) != 1 {
		t.Fatalf("repeated after a restart: %q", got)
	}
}

// TestDomainExpiryReminderWaitsForTheChannel: with no channel nothing is
// posted and nothing marked; once one is set the reminder goes. A date long
// past gets one «expired» post; no date, none at all.
func TestDomainExpiryReminderWaitsForTheChannel(t *testing.T) {
	tg, fake := notifyBotFixture(t, "")
	now := day(t, "2027-03-05")
	expiryClock(t, &now)
	tg.CheckDomainExpiry() // no date
	setSetting(t, "domainExpiry", "2027-03-01")
	tg.CheckDomainExpiry()
	if n := fake.sentMessages(); n != 0 {
		t.Fatalf("sent %d messages without a channel", n)
	}
	setSetting(t, "tgNotifyChatId", testNotifyChannel)
	tg.CheckDomainExpiry()
	tg.CheckDomainExpiry()
	if got := fake.sentTo(testNotifyChannel); len(got) != 1 || !strings.Contains(got[0], "expired") ||
		!strings.Contains(got[0], "4 days ago") {
		t.Fatalf("posted: %q", got)
	}
}
