package service

import (
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/global"
)

// monScreenFixture is the monitoring test database with the bot's locale and
// hash storage, monitoring on, mon-server last heard at contact (0: never),
// two enabled inbounds and a disabled one, and two mon-clients.
func monScreenFixture(t *testing.T, contact time.Time) *Tgbot {
	t.Helper()
	newMonitoringTestService(t)
	initTestBotLocale(t, "en-US")
	resetMonStaleForTest()
	prevHash := hashStorage
	hashStorage = global.NewHashStorage(time.Minute)
	monContact.Lock()
	prevLoaded, prevLast, prevPersisted := monContact.loaded, monContact.last, monContact.persisted
	monContact.loaded, monContact.last, monContact.persisted = true, 0, 0
	if !contact.IsZero() {
		monContact.last = contact.UnixMilli()
	}
	monContact.Unlock()
	t.Cleanup(func() {
		resetMonStaleForTest()
		hashStorage = prevHash
		monContact.Lock()
		monContact.loaded, monContact.last, monContact.persisted = prevLoaded, prevLast, prevPersisted
		monContact.Unlock()
		botScreens.drop(usersTestChat)
	})
	if err := (&SettingService{}).SetMonEnable(true); err != nil {
		t.Fatal(err)
	}
	monInbound(t, 1, model.VLESS, true)
	monInbound(t, 2, model.VLESS, true)
	monInbound(t, 3, model.VLESS, false)
	monRemark(t, 1, "vless-reality")
	monRemark(t, 2, "vless-spare")
	monRemark(t, 3, "vless-off")
	monRegister(t,
		MonClient{Id: "ams-1", Name: "Amsterdam", Region: "eu-west", State: "ONLINE"},
		MonClient{Id: "fra-1", Name: "Frankfurt", Region: "", State: "OFFLINE"},
	)
	return &Tgbot{}
}

// TestScreenMonitoring: «📡 Monitoring» shows how fresh the data is, the
// mon-clients, and for each enabled inbound the state of its paths (the
// worst over the mon-clients) with the range of their 24 h uptime.
func TestScreenMonitoring(t *testing.T) {
	tg := monScreenFixture(t, time.Now().Add(-5*time.Minute))
	from := time.Now().Add(-24*time.Hour).UnixMilli() + 60000
	monTarget(t, "ams-1", "xray", 1, "direct", model.MonStateUp, from, "")
	monTarget(t, "ams-1", "xray", 1, "proxy", model.MonStateUp, from, "")
	monTarget(t, "fra-1", "xray", 1, "proxy", model.MonStateDown, from, "tls_timeout")
	monTarget(t, "ams-1", "xray", 3, "direct", model.MonStatePaused, from, "")
	monCurrent(t, "ams-1", "xray", 1, "direct", from, 10, 10, 0)
	monCurrent(t, "ams-1", "xray", 1, "proxy", from, 10, 9, 1)
	monCurrent(t, "fra-1", "xray", 1, "proxy", from, 10, 1, 1)

	reply := screenPressData(t, tg, "s_mon")
	for _, want := range []string{
		"<b>Monitoring</b> · data fresh (5m ago)",
		"mon-clients: Amsterdam (eu-west) 🟢 ONLINE · Frankfurt 🔴 OFFLINE",
		"<b>vless-reality</b>: direct UP · proxy DOWN · 24h 50.0–100.0 %",
		"<b>vless-spare</b>: no data",
	} {
		if !strings.Contains(reply.text, want) {
			t.Errorf("want %q in\n%s", want, reply.text)
		}
	}
	if strings.Contains(reply.text, "vless-off") {
		t.Errorf("a disabled inbound is listed:\n%s", reply.text)
	}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != "📜 Events|🔄 Refresh|📡 Probe accounts" {
		t.Errorf("buttons: %q", got)
	}
	if reply.route != "s_mon" || button(t, reply.keyboard, "Probe accounts") != "prb_l 0" {
		t.Errorf("route %q, probes %q", reply.route, button(t, reply.keyboard, "Probe accounts"))
	}
}

// TestScreenMonitoringFreshness: silent mon-server shows as stale since
// when it went quiet; one that never called in says so; monitoring off says
// that and nothing else.
func TestScreenMonitoringFreshness(t *testing.T) {
	tg := monScreenFixture(t, time.Time{})
	if reply := screenPressData(t, tg, "s_mon"); !strings.Contains(reply.text, "no data from mon-server yet") {
		t.Errorf("never: %q", reply.text)
	}

	since := time.Now().Add(-20 * time.Minute)
	monStale.Lock()
	monStale.loaded, monStale.stale, monStale.since = true, true, since.UnixMilli()
	monStale.Unlock()
	if reply := screenPressData(t, tg, "s_mon"); !strings.Contains(reply.text, "⚠️ data stale since "+since.Format("15:04")) {
		t.Errorf("stale: %q", reply.text)
	}

	if err := (&SettingService{}).SetMonEnable(false); err != nil {
		t.Fatal(err)
	}
	reply := screenPressData(t, tg, "s_mon")
	if !strings.Contains(reply.text, "Monitoring is off") || strings.Contains(reply.text, "vless") {
		t.Errorf("off: %q", reply.text)
	}
	if got := buttonTexts(t, reply.keyboard); strings.Join(got, "|") != "📜 Events|🔄 Refresh|📡 Probe accounts" {
		t.Errorf("off buttons: %q", got)
	}
}

// TestScreenMonitoringEvents: «📜 Events» is the feed, newest first, ten a
// page, with the way to older ones; Back leads to the newer page.
func TestScreenMonitoringEvents(t *testing.T) {
	tg := monScreenFixture(t, time.Now())
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.Local).UnixMilli()
	for i := range 11 {
		monStoreEvent(t, "ev-"+string(rune('a'+i)), base+int64(i)*60000, model.MonEventKindTarget, "ams-1", "xray", 1, "proxy", "UP", "DOWN")
	}
	monStoreEvent(t, "ev-client", base+20*60000, model.MonEventKindMonClient, "fra-1", "", 0, "", "ONLINE", "OFFLINE")

	first := screenPressData(t, tg, "s_mev 0")
	for _, want := range []string{
		"<b>Monitoring events</b>",
		"20.09 10:20 · mon-client Frankfurt: ONLINE → OFFLINE",
		"20.09 10:10 · vless-reality via proxy: UP → DOWN · Amsterdam (eu-west)",
	} {
		if !strings.Contains(first.text, want) {
			t.Errorf("want %q in\n%s", want, first.text)
		}
	}
	if strings.Contains(first.text, "10:01") {
		t.Errorf("the first page holds more than ten:\n%s", first.text)
	}
	older := button(t, first.keyboard, "Older")
	second := screenPressData(t, tg, older)
	if !strings.Contains(second.text, "10:01") || !strings.Contains(second.text, "10:00") || strings.Contains(second.text, "10:02") {
		t.Errorf("second page:\n%s", second.text)
	}
	if strings.Contains(strings.Join(buttonTexts(t, second.keyboard), "|"), "Older") {
		t.Errorf("the last page offers older ones: %q", buttonTexts(t, second.keyboard))
	}

	fake := withScreenTelegram(t)
	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "📡 Monitoring")
	fake.press(t, tg, 1, "📜 Events")
	fake.press(t, tg, 1, "Older")
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(fake.messages[1].text, "10:20") {
		t.Errorf("back from the older page: %q", fake.messages[1].text)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	fake.press(t, tg, 1, "📡 Probe accounts")
	// No probes yet: monitoring's card, read-only (#183).
	if !strings.Contains(fake.messages[1].text, "<b>monitoring</b>") {
		t.Errorf("probe accounts: %q", fake.messages[1].text)
	}
}

// TestScreenMonitoringNoEvents: an empty feed says so.
func TestScreenMonitoringNoEvents(t *testing.T) {
	tg := monScreenFixture(t, time.Now())
	reply := screenPressData(t, tg, "s_mev 0")
	if !strings.Contains(reply.text, "No monitoring events yet") {
		t.Errorf("empty: %q", reply.text)
	}
}
