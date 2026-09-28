package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// State resync (sane-3x-ui#151, #158): the answer to POST /stats names every
// target the panel holds no state for — UNKNOWN and not a single event since
// the row was created — and mon-server answers with a resync event, which the
// panel applies without a feed row or a Telegram message.

// TestStatsResponseNamesTargetsWithoutState: a target re-created from stats
// (the path dropped out and came back, or the panel lost its database) is
// named in resync on every answer until an event for it arrives; a repeated
// batch does not grow the list.
func TestStatsResponseNamesTargetsWithoutState(t *testing.T) {
	m := newMonitoringTestService(t)
	monInbound(t, 1, model.VLESS, true, model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "alice"})
	bucket := (time.Now().UnixMilli() / monBucketMs) * monBucketMs
	want := []MonTargetKey{{MonClientId: "ams-1", InboundKind: "xray", InboundId: 1, Path: "inner:bridge"}}

	for round := 1; round <= 2; round++ {
		res, err := m.UpsertStats([]MonStatIn{stat("ams-1", 1, "inner:bridge", bucket, 5, 0, i64(1), i64(2), i64(3))})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if !reflect.DeepEqual(res.Resync, want) {
			t.Errorf("round %d: resync = %+v, want %+v", round, res.Resync, want)
		}
	}

	if _, err := m.ApplyEvents([]MonEventIn{{Id: ev1, Ts: bucket + 1, Kind: "target", MonClientId: "ams-1",
		InboundKind: "xray", InboundId: 1, Path: "inner:bridge", From: "UNKNOWN", To: "UP", Reason: "recovered", Notified: true}}); err != nil {
		t.Fatal(err)
	}
	res, err := m.UpsertStats([]MonStatIn{stat("ams-1", 1, "inner:bridge", bucket, 6, 0, i64(1), i64(2), i64(3))})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resync) != 0 {
		t.Errorf("after an event: resync = %+v, want none", res.Resync)
	}
}

// TestResyncEventIsAppliedSilently: a resync event moves the target to what
// mon-server holds — state, since, reason — but writes no feed row and
// reaches no Telegram message, whatever its notified flag; the target then
// leaves the resync list. An older resync does not roll the state back.
func TestResyncEventIsAppliedSilently(t *testing.T) {
	m := newMonitoringTestService(t)
	monInbound(t, 1, model.VLESS, true, model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "alice"})
	rec := &recordingNotifier{}
	SetMonEventNotifier(rec)
	t.Cleanup(func() { SetMonEventNotifier(nil) })
	db := database.GetDB()
	bucket := (time.Now().UnixMilli() / monBucketMs) * monBucketMs

	if _, err := m.UpsertStats([]MonStatIn{
		stat("ams-1", 1, "direct", bucket, 5, 0, i64(1), i64(2), i64(3)),
		stat("ams-1", 1, "inner:bridge", bucket, 5, 0, i64(1), i64(2), i64(3)),
	}); err != nil {
		t.Fatal(err)
	}

	resync := func(id string, ts int64, path, state string, notified bool) MonEventIn {
		return MonEventIn{Id: id, Ts: ts, Kind: "target", MonClientId: "ams-1", InboundKind: "xray", InboundId: 1,
			Path: path, From: state, To: state, Reason: "resync", Notified: notified}
	}
	res, err := m.ApplyEvents([]MonEventIn{
		resync(ev1, bucket+1000, "direct", "UP", true),
		resync(ev2, bucket+1000, "inner:bridge", "DOWN", false),
		resync(ev3, bucket-1, "direct", "DOWN", true), // older than the row: no roll-back
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Accepted != 3 || len(res.Rejected) != 0 || len(res.Ignored) != 0 {
		t.Errorf("result = %+v, want all three accepted", res)
	}

	var direct, inner model.MonTarget
	db.Where("path = ?", "direct").First(&direct)
	db.Where("path = ?", "inner:bridge").First(&inner)
	if direct.State != "UP" || direct.Since != bucket+1000 || direct.Reason != "resync" {
		t.Errorf("direct after resync = %+v, want UP since %d reason resync", direct, bucket+1000)
	}
	if inner.State != "DOWN" || inner.Since != bucket+1000 {
		t.Errorf("inner:bridge after resync = %+v, want DOWN since %d", inner, bucket+1000)
	}
	var feed int64
	db.Model(&model.MonEvent{}).Count(&feed)
	if feed != 0 {
		t.Errorf("%d feed rows after resync events, want none", feed)
	}
	if len(rec.got) != 0 {
		t.Errorf("notifier got %+v, want no message for a resync", rec.got)
	}

	res2, err := m.UpsertStats([]MonStatIn{stat("ams-1", 1, "direct", bucket, 6, 0, i64(1), i64(2), i64(3))})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Resync) != 0 {
		t.Errorf("after resync: resync = %+v, want none", res2.Resync)
	}
}

// TestOrdinaryEventsKeepTheirBehaviour: an event that is not a resync — even
// UP → UP — still goes into the feed and to the Telegram hook, and clears
// the resync request. A target that an event created is never named, even in
// UNKNOWN: the panel has mon-server's word for it. A late event that the
// since guard keeps from moving the row does not count as state.
func TestOrdinaryEventsKeepTheirBehaviour(t *testing.T) {
	m := newMonitoringTestService(t)
	monInbound(t, 1, model.VLESS, true, model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "alice"})
	rec := &recordingNotifier{}
	SetMonEventNotifier(rec)
	t.Cleanup(func() { SetMonEventNotifier(nil) })
	db := database.GetDB()
	bucket := (time.Now().UnixMilli() / monBucketMs) * monBucketMs

	if _, err := m.UpsertStats([]MonStatIn{
		stat("ams-1", 1, "direct", bucket, 5, 0, i64(1), i64(2), i64(3)),
		stat("ams-1", 1, "edge:edge-a", bucket, 5, 0, i64(1), i64(2), i64(3)),
	}); err != nil {
		t.Fatal(err)
	}
	ev := func(id string, ts int64, path, from, to, reason string) MonEventIn {
		return MonEventIn{Id: id, Ts: ts, Kind: "target", MonClientId: "ams-1", InboundKind: "xray", InboundId: 1,
			Path: path, From: from, To: to, Reason: reason}
	}
	if _, err := m.ApplyEvents([]MonEventIn{
		ev(ev1, bucket+1000, "direct", "UP", "UP", "recovered"),
		ev(ev2, bucket-1, "edge:edge-a", "DOWN", "UP", "recovered"), // late: the row is newer
		ev(ev3, bucket+1000, "inner:bridge", "", "UNKNOWN", "mon_client_revoked"),
	}); err != nil {
		t.Fatal(err)
	}

	var feed int64
	db.Model(&model.MonEvent{}).Count(&feed)
	if feed != 3 {
		t.Errorf("%d feed rows, want all three events in the feed", feed)
	}
	if len(rec.got) != 3 || rec.got[1].Id != ev1 || rec.got[1].From != "UP" || rec.got[1].To != "UP" {
		t.Errorf("notifier got %+v, want the three events, UP → UP among them", rec.got)
	}
	var direct model.MonTarget
	db.Where("path = ?", "direct").First(&direct)
	if direct.State != "UP" || direct.Since != bucket+1000 || direct.Reason != "recovered" {
		t.Errorf("direct after UP → UP = %+v", direct)
	}

	res, err := m.UpsertStats([]MonStatIn{stat("ams-1", 1, "inner:bridge", bucket, 1, 0, i64(1), i64(2), i64(3))})
	if err != nil {
		t.Fatal(err)
	}
	want := []MonTargetKey{{MonClientId: "ams-1", InboundKind: "xray", InboundId: 1, Path: "edge:edge-a"}}
	if !reflect.DeepEqual(res.Resync, want) {
		t.Errorf("resync = %+v, want only the target whose one event came too late, %+v", res.Resync, want)
	}
}
