package service

import (
	"strings"
	"testing"
	"time"
)

const testClientRouting = `{"name":"RU","domainStrategy":"IPIfNonMatch","directSites":["geosite:category-ru"],"directIp":["geoip:ru"],"directProtocols":["bittorrent"],"proxySites":["domain:acomics.ru"]}`

// fixClientRoutingClock makes the change time's clock read the times the
// test sets.
func fixClientRoutingClock(t *testing.T, now *time.Time) {
	t.Helper()
	clientRoutingNow = func() time.Time { return *now }
	t.Cleanup(func() { clientRoutingNow = time.Now })
}

// TestSubClientRoutingSetting: the client routing (#263) through the
// settings form. Off on a fresh panel; saved, it is stored normalized with
// the time it changed; saved again unchanged — the form saves every field —
// the time stays, or every Happ would download its geo files again; changed,
// the time moves on; refused, nothing moves.
func TestSubClientRoutingSetting(t *testing.T) {
	s := newMonitoringSettingService(t)
	now := time.Unix(1760745600, 0)
	fixClientRoutingClock(t, &now)

	if r, updated, err := s.GetSubClientRouting(); r != nil || updated != 0 || err != nil {
		t.Fatalf("fresh panel: %v, %d, %v", r, updated, err)
	}
	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	if all.SubClientRouting != "" {
		t.Fatalf("the form shows %q", all.SubClientRouting)
	}

	all.SubClientRouting = testClientRouting
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatalf("save: %v", err)
	}
	r, updated, err := s.GetSubClientRouting()
	if err != nil || r == nil || r.Name != "RU" || updated != now.Unix() {
		t.Fatalf("after the save: %+v, %d, %v", r, updated, err)
	}
	stored, _ := s.GetSubClientRoutingSetting()
	if !strings.Contains(stored, "\n  \"blockIp\": []") {
		t.Errorf("stored not normalized:\n%s", stored)
	}

	// The form as it came back, an hour later: the time stays.
	now = now.Add(time.Hour)
	all, _ = s.GetAllSetting()
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.GetSubClientRouting(); again != updated {
		t.Errorf("an unchanged save moved the time: %d -> %d", updated, again)
	}

	// A changed routing moves it to now.
	all.SubClientRouting = strings.Replace(testClientRouting, `"domain:acomics.ru"`, `"domain:acomics.ru","domain:example.ru"`, 1)
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if r, again, _ := s.GetSubClientRouting(); again != now.Unix() || len(r.ProxySites) != 2 {
		t.Errorf("after a change: %+v, %d", r, again)
	}

	// A routing the apps could not use is refused, and the stored one stays.
	before, _ := s.GetSubClientRoutingSetting()
	all.SubClientRouting = `{"name":"RU","domainStrategy":"Sometimes"}`
	if err := s.UpdateAllSetting(all); err == nil || !strings.Contains(err.Error(), "client routing") {
		t.Errorf("refusal: %v", err)
	}
	if after, _ := s.GetSubClientRoutingSetting(); after != before {
		t.Errorf("a refused save changed the stored routing")
	}

	// Off, and back on: the time moves again.
	all.SubClientRouting = ""
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := s.GetSubClientRouting(); r != nil {
		t.Errorf("off: %+v", r)
	}
	now = now.Add(time.Hour)
	all.SubClientRouting = testClientRouting
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.GetSubClientRouting(); again != now.Unix() {
		t.Errorf("back on: %d, want %d", again, now.Unix())
	}
}

// TestSetSubClientRouting: the CLI's way in, as the orchestrator sets it on
// every run. The same routing again leaves the time; two changes within a
// second still give two times, later and later.
func TestSetSubClientRouting(t *testing.T) {
	s := newMonitoringSettingService(t)
	now := time.Unix(1760745600, 0)
	fixClientRoutingClock(t, &now)

	if err := s.SetSubClientRouting(testClientRouting); err != nil {
		t.Fatal(err)
	}
	_, first, _ := s.GetSubClientRouting()
	if err := s.SetSubClientRouting(testClientRouting); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.GetSubClientRouting(); again != first {
		t.Errorf("the same routing moved the time: %d -> %d", first, again)
	}
	if err := s.SetSubClientRouting(strings.Replace(testClientRouting, `"RU"`, `"RU2"`, 1)); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.GetSubClientRouting(); again != first+1 {
		t.Errorf("a change within the second: %d, want %d", again, first+1)
	}
	if err := s.SetSubClientRouting(`{"name":"RU"`); err == nil {
		t.Error("a broken routing was stored")
	}
	if err := s.SetSubClientRouting(""); err != nil {
		t.Fatal(err)
	}
	if r, _, _ := s.GetSubClientRouting(); r != nil {
		t.Errorf("off: %+v", r)
	}
}

// TestGetSubClientRouting_StampsAStoredRoutingOnce: a routing written past
// the form and the CLI has no change time; it gets one when first read, and
// keeps it.
func TestGetSubClientRouting_StampsAStoredRoutingOnce(t *testing.T) {
	s := newMonitoringSettingService(t)
	now := time.Unix(1760745600, 0)
	fixClientRoutingClock(t, &now)
	setSetting(t, subClientRoutingKey, testClientRouting)

	_, first, err := s.GetSubClientRouting()
	if err != nil || first != now.Unix() {
		t.Fatalf("first read: %d, %v", first, err)
	}
	now = now.Add(time.Hour)
	if _, again, _ := s.GetSubClientRouting(); again != first {
		t.Errorf("second read: %d, want %d", again, first)
	}
}
