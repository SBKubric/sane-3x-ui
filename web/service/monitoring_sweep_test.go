package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// Diagnostic sweep events (kind sweep) and the target reasons derived and
// sweep — contract §4.6, SBKubric/sane-3x-ui#255.

// monSweepExampleReport is the report of the issue's start example: the
// edge's tunnel is down while its host answers, the leg from the edge to the
// inner hop has not reported since a minute ago, the real server loses some
// echoes but its tunnel works.
func monSweepExampleReport(lastAt int64) *MonSweepReport {
	return &MonSweepReport{
		Paths: []MonSweepPath{
			{Path: "edge:proxy", Ok: false, Reason: "awg_no_handshake"},
			{Path: "inner:bridge", Ok: true},
			{Path: "direct", Ok: true},
		},
		Hosts: []MonSweepHost{
			{From: MonSweepFromMonClient, To: "proxy", At: i64(lastAt + 60000), Sent: 10, LossPct: 0, RttAvgMs: i64(2)},
			{From: "proxy", To: "bridge", At: nil, LastAt: i64(lastAt)},
			{From: MonSweepFromMonClient, To: "", At: i64(lastAt + 60000), Sent: 10, LossPct: 12, RttAvgMs: i64(64)},
		},
	}
}

func sweepEvent(id string, ts int64, phase string, report *MonSweepReport) MonEventIn {
	return MonEventIn{Id: id, Ts: ts, Kind: "sweep", MonClientId: "ams-1", InboundKind: "awg", Phase: phase, Report: report}
}

// TestApplyEventsAcceptsSweep: a sweep goes into the feed with its phase and
// report, is offered to the Telegram hook when mon-server has not notified,
// is a duplicate on a repeated id, and never touches mon_targets.
func TestApplyEventsAcceptsSweep(t *testing.T) {
	m := newMonitoringTestService(t)
	monInbound(t, 1, model.VLESS, true)
	rec := &recordingNotifier{}
	SetMonEventNotifier(rec)
	t.Cleanup(func() { SetMonEventNotifier(nil) })
	db := database.GetDB()

	// A target row that a sweep must leave as it is.
	if _, err := m.ApplyEvents([]MonEventIn{targetEvent(ev1, 1000, "", model.MonStateUp, "")}); err != nil {
		t.Fatal(err)
	}
	var before model.MonTarget
	db.First(&before)
	rec.got = nil

	start := sweepEvent(ev2, 5000, "start", monSweepExampleReport(4000))
	start.From, start.To = "UP", "DOWN" // a sweep has no from/to: dropped, not checked
	res, err := m.ApplyEvents([]MonEventIn{start})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 || len(res.Rejected) != 0 || len(res.Ignored) != 0 {
		t.Fatalf("result = %+v, want the sweep accepted", res)
	}

	var stored model.MonEvent
	if err := db.First(&stored, "id = ?", ev2).Error; err != nil {
		t.Fatalf("load sweep: %v", err)
	}
	if stored.Kind != "sweep" || stored.Phase != "start" || stored.MonClientId != "ams-1" || stored.InboundKind != "awg" ||
		stored.InboundId != 0 || stored.Path != "" || stored.From != "" || stored.To != "" {
		t.Errorf("stored sweep = %+v", stored)
	}
	var report MonSweepReport
	if err := json.Unmarshal([]byte(stored.Report), &report); err != nil {
		t.Fatalf("stored report %q: %v", stored.Report, err)
	}
	if len(report.Paths) != 3 || len(report.Hosts) != 3 || report.Hosts[1].At != nil || *report.Hosts[1].LastAt != 4000 {
		t.Errorf("stored report = %+v", report)
	}
	if len(rec.got) != 1 || rec.got[0].Id != ev2 {
		t.Errorf("notifier got %+v, want the sweep", rec.got)
	}

	var targets []model.MonTarget
	db.Find(&targets)
	if len(targets) != 1 || targets[0].State != before.State || targets[0].Since != before.Since || targets[0].Reason != before.Reason {
		t.Errorf("mon_targets after a sweep = %+v, want only the untouched %+v", targets, before)
	}

	// A repeat is a duplicate; a sweep mon-server already notified is stored
	// but not offered.
	rec.got = nil
	notified := sweepEvent(ev3, 6000, "end", &MonSweepReport{Paths: []MonSweepPath{{Path: "edge:proxy", Ok: true}}})
	notified.Notified = true
	res, err = m.ApplyEvents([]MonEventIn{start, notified})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 || res.Duplicates != 1 {
		t.Errorf("result = %+v, want one duplicate and one accepted", res)
	}
	if len(rec.got) != 0 {
		t.Errorf("notifier got %+v, want nothing for a notified sweep", rec.got)
	}
}

// TestApplyEventsRejectsBadSweeps: each broken sweep is rejected on its own,
// named by the field that is wrong.
func TestApplyEventsRejectsBadSweeps(t *testing.T) {
	m := newMonitoringTestService(t)
	ok := monSweepExampleReport(4000)
	tests := []struct {
		name  string
		event MonEventIn
		field string
	}{
		{"no mon-client", func() MonEventIn { e := sweepEvent(ev1, 1, "start", ok); e.MonClientId = ""; return e }(), "events[0].monClientId"},
		{"unknown inbound kind", func() MonEventIn { e := sweepEvent(ev1, 1, "start", ok); e.InboundKind = "wg"; return e }(), "events[0].inboundKind"},
		{"unknown phase", sweepEvent(ev1, 1, "begin", ok), "events[0].phase"},
		{"no phase", sweepEvent(ev1, 1, "", ok), "events[0].phase"},
		{"no report", sweepEvent(ev1, 1, "start", nil), "events[0].report"},
		{"bad path", sweepEvent(ev1, 1, "start", &MonSweepReport{Paths: []MonSweepPath{{Path: "tunnel"}}}), "events[0].report.paths[0].path"},
		{"bad from", sweepEvent(ev1, 1, "start", &MonSweepReport{Hosts: []MonSweepHost{{From: "Mon Client", To: ""}}}), "events[0].report.hosts[0].from"},
		{"bad to", sweepEvent(ev1, 1, "start", &MonSweepReport{Hosts: []MonSweepHost{{From: "mon-client", To: "Bridge!"}}}), "events[0].report.hosts[0].to"},
		{"loss above 100", sweepEvent(ev1, 1, "start", &MonSweepReport{Hosts: []MonSweepHost{{From: "mon-client", At: i64(1), LossPct: 101}}}), "events[0].report.hosts[0].lossPct"},
		{"negative rtt", sweepEvent(ev1, 1, "start", &MonSweepReport{Hosts: []MonSweepHost{{From: "mon-client", At: i64(1), RttAvgMs: i64(-1)}}}), "events[0].report.hosts[0].rttAvgMs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := m.ApplyEvents([]MonEventIn{tc.event})
			if err != nil {
				t.Fatal(err)
			}
			if res.Accepted != 0 || len(res.Rejected) != 1 || !strings.Contains(res.Rejected[0].Error, tc.field) {
				t.Errorf("result = %+v, want one rejection naming %s", res, tc.field)
			}
		})
	}

	// An empty report is still a report: the phase alone is news.
	res, err := m.ApplyEventsRaw([]json.RawMessage{json.RawMessage(
		`{"id":"` + ev2 + `","ts":5,"kind":"sweep","monClientId":"ams-1","inboundKind":"xray","phase":"end","report":{},"extra":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 1 {
		t.Errorf("result = %+v, want an empty report accepted", res)
	}
}

// TestDerivedAndSweepReasonsMoveTheTarget: the new reasons of a target event
// are ordinary reasons — the row moves, the feed gets it, and with
// notified=true nothing goes to Telegram.
func TestDerivedAndSweepReasonsMoveTheTarget(t *testing.T) {
	m := newMonitoringTestService(t)
	monInbound(t, 1, model.VLESS, true)
	rec := &recordingNotifier{}
	SetMonEventNotifier(rec)
	t.Cleanup(func() { SetMonEventNotifier(nil) })

	derived := targetEvent(ev1, 1000, "", model.MonStateUp, MonReasonDerived)
	derived.Path, derived.Notified = "inner:bridge", true
	swept := targetEvent(ev2, 1000, "", model.MonStateDown, MonReasonSweep)
	swept.Path, swept.Notified = "direct", true
	res, err := m.ApplyEvents([]MonEventIn{derived, swept})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 2 {
		t.Fatalf("result = %+v, want both accepted", res)
	}
	db := database.GetDB()
	for path, want := range map[string]model.MonTarget{
		"inner:bridge": {State: model.MonStateUp, Reason: MonReasonDerived},
		"direct":       {State: model.MonStateDown, Reason: MonReasonSweep},
	} {
		var got model.MonTarget
		if err := db.First(&got, "path = ?", path).Error; err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		if got.State != want.State || got.Reason != want.Reason {
			t.Errorf("%s = %s/%s, want %s/%s", path, got.State, got.Reason, want.State, want.Reason)
		}
	}
	var feed int64
	db.Model(&model.MonEvent{}).Count(&feed)
	if feed != 2 {
		t.Errorf("%d feed rows, want 2", feed)
	}
	if len(rec.got) != 0 {
		t.Errorf("notifier got %+v, want nothing for notified events", rec.got)
	}
}

// TestUIEventsShowsTheSweepReport: the feed hands the page the decoded report
// and names no inbound for a sweep — it covers every inbound of its kind.
func TestUIEventsShowsTheSweepReport(t *testing.T) {
	m := newMonitoringTestService(t)
	monRegister(t, MonClient{Id: "ams-1", Name: "Amsterdam", Region: "nl", State: "ONLINE"})
	if _, err := m.ApplyEvents([]MonEventIn{sweepEvent(ev1, 5000, "start", monSweepExampleReport(4000))}); err != nil {
		t.Fatal(err)
	}
	events, err := m.UIEvents(0, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want the sweep", events)
	}
	e := events[0]
	if e.Kind != "sweep" || e.Phase != "start" || e.MonClientName != "Amsterdam" || e.InboundRemark != "" ||
		e.SweepReport == nil || len(e.SweepReport.Hosts) != 3 {
		t.Errorf("feed entry = %+v", e)
	}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"phase":"start"`, `"report":{"paths":[{"path":"edge:proxy","ok":false,"reason":"awg_no_handshake"}`, `"lastAt":4000`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("feed JSON %s does not contain %s", body, want)
		}
	}

	// The inbound filter of the page is about target events only.
	filtered, err := m.UIEvents(0, 10, "awg", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Errorf("filtered feed = %+v, want no sweep in an inbound's feed", filtered)
	}
}

// sweepRow is a stored sweep event as the notifier gets it.
func sweepRow(t *testing.T, id string, ts int64, phase string, report *MonSweepReport) model.MonEvent {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return model.MonEvent{Id: id, Ts: ts, Kind: model.MonEventKindSweep, MonClientId: "ams-1", InboundKind: "awg",
		Phase: phase, Report: string(body)}
}

// TestSweepMessages renders the three phases the way the issue spells them
// out, in Russian, and checks the English copy renders too.
func TestSweepMessages(t *testing.T) {
	newMonitoringTestService(t)
	initTestBotLocale(t, "ru-RU")
	monRegister(t, MonClient{Id: "ams-1", Name: "monclient", State: "ONLINE"})

	base := monSummaryNow.UnixMilli()
	lastAt := base - 2*60000
	since := time.UnixMilli(lastAt).Format("15:04")
	report := monSweepExampleReport(lastAt)

	bot, sent := newTestBot(t)
	ids := bot.NotifyMonitoringEvents([]model.MonEvent{sweepRow(t, ev1, base, "start", report)})
	if len(ids) != 1 || len(*sent) != 1 {
		t.Fatalf("ids %v, sent %q, want one message", ids, *sent)
	}
	want := strings.Join([]string{
		"⛔ AWG недоступен через все edge · monclient",
		"proxy — ICMP ✅ 0% · 2 мс · туннель ❌ awg_no_handshake",
		"proxy → bridge — ICMP ❌ нет отчёта с " + since,
		"real (direct) — ICMP ⚠️ 12% · 64 мс · туннель ✅",
		"bridge — туннель ✅",
	}, "\n")
	if (*sent)[0] != want {
		t.Errorf("start message\n%s\nwant\n%s", (*sent)[0], want)
	}

	// change: the same lines under its own head.
	*sent = nil
	bot.NotifyMonitoringEvents([]model.MonEvent{sweepRow(t, ev2, base+60000, "change", report)})
	if len(*sent) != 1 || !strings.HasPrefix((*sent)[0], "⛔ AWG недоступен через все edge, картина изменилась · monclient\nproxy — ICMP") {
		t.Errorf("change message = %q", *sent)
	}

	// end: through which edge, and how long since the start in the feed.
	monStoreSweepStart(t, ev3, base)
	*sent = nil
	end := &MonSweepReport{Paths: []MonSweepPath{{Path: "edge:proxy2", Ok: false, Reason: "tcp_timeout"}, {Path: "edge:proxy", Ok: true}}}
	bot.NotifyMonitoringEvents([]model.MonEvent{sweepRow(t, ev4, base+42*60000, "end", end)})
	if len(*sent) != 1 || (*sent)[0] != "✅ AWG снова доступен через proxy · monclient · авария длилась 42m" {
		t.Errorf("end message = %q", *sent)
	}

	// An end whose start is gone from the feed says nothing about duration.
	*sent = nil
	early := sweepRow(t, ev5, base-1, "end", end)
	bot.NotifyMonitoringEvents([]model.MonEvent{early})
	if len(*sent) != 1 || (*sent)[0] != "✅ AWG снова доступен через proxy · monclient" {
		t.Errorf("end without a start = %q", *sent)
	}

	initTestBotLocale(t, "en-US")
	*sent = nil
	bot.NotifyMonitoringEvents([]model.MonEvent{sweepRow(t, ev1, base, "start", report)})
	for _, want := range []string{"⛔ AWG unreachable through every edge · monclient", "proxy → bridge — ICMP ❌ no report since " + since,
		"real (direct) — ICMP ⚠️ 12% · 64 ms · tunnel ✅"} {
		if len(*sent) != 1 || !strings.Contains((*sent)[0], want) {
			t.Errorf("en start message %q does not contain %q", *sent, want)
		}
	}
}

// TestSweepIcmpMarks: ✅ for no loss, ⚠️ for some, ❌ for all or no report,
// always with the numbers.
func TestSweepIcmpMarks(t *testing.T) {
	newMonitoringTestService(t)
	initTestBotLocale(t, "en-US")
	bot, _ := newTestBot(t)
	for _, tc := range []struct {
		host MonSweepHost
		want string
	}{
		{MonSweepHost{At: i64(1), LossPct: 0, RttAvgMs: i64(3)}, "✅ 0% · 3 ms"},
		{MonSweepHost{At: i64(1), LossPct: 50, RttAvgMs: i64(80)}, "⚠️ 50% · 80 ms"},
		{MonSweepHost{At: i64(1), LossPct: 100}, "❌ 100%"},
		{MonSweepHost{}, "❌ no report"},
	} {
		if got := bot.monSweepIcmp(tc.host); got != tc.want {
			t.Errorf("icmp of %+v = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// TestApplyEventsSendsOneSweepMessage wires the bot in: a sweep with
// notified=false is one message and comes back notified; with notified=true
// it is stored and says nothing.
func TestApplyEventsSendsOneSweepMessage(t *testing.T) {
	m := newMonitoringTestService(t)
	initTestBotLocale(t, "en-US")
	bot, sent := newTestBot(t)
	SetMonEventNotifier(bot)
	t.Cleanup(func() { SetMonEventNotifier(nil) })

	quiet := sweepEvent(ev2, 6000, "change", monSweepExampleReport(4000))
	quiet.Notified = true
	if _, err := m.ApplyEvents([]MonEventIn{sweepEvent(ev1, 5000, "start", monSweepExampleReport(4000)), quiet}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || !strings.HasPrefix((*sent)[0], "⛔ AWG unreachable through every edge") {
		t.Fatalf("sent %q, want only the start", *sent)
	}
	var stored model.MonEvent
	database.GetDB().First(&stored, "id = ?", ev1)
	if !stored.Notified {
		t.Error("the delivered sweep was not marked notified")
	}
}

// TestSweepEventLine: the bot's events screen words a sweep by its phase.
func TestSweepEventLine(t *testing.T) {
	newMonitoringTestService(t)
	initTestBotLocale(t, "en-US")
	bot, _ := newTestBot(t)
	ev := MonUIEvent{MonEvent: model.MonEvent{Id: ev1, Ts: monSummaryNow.UnixMilli(), Kind: "sweep", MonClientId: "ams-1",
		InboundKind: "awg", Phase: "start"}, MonClientName: "Amsterdam"}
	if line := bot.monEventLine(&ev); !strings.Contains(line, "AWG: diagnostic sweep started · Amsterdam") {
		t.Errorf("start line = %q", line)
	}
	ev.Phase = "end"
	if line := bot.monEventLine(&ev); !strings.Contains(line, "AWG: diagnostic sweep over") {
		t.Errorf("end line = %q", line)
	}
}

func monStoreSweepStart(t *testing.T, id string, ts int64) {
	t.Helper()
	ev := model.MonEvent{Id: id, Ts: ts, ReceivedAt: ts, Kind: model.MonEventKindSweep, MonClientId: "ams-1",
		InboundKind: "awg", Phase: model.MonSweepPhaseStart, Notified: true}
	if err := database.GetDB().Create(&ev).Error; err != nil {
		t.Fatalf("create sweep start %s: %v", id, err)
	}
}
