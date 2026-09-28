package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// A standby edge and the chain-following inbounds (sane-3x-ui#161, decision
// #157 Q2): the inbounds accept the active edge's server name only, and a
// standby edge sends that name to its own neighbour target, so a probe of
// them through a standby edge could only ever be DOWN. The panel hands out
// no such probe; everything else through a standby edge stays.

// monStandbyChain is `real ← core-1 ← {edge-a (active), edge-b}` with a
// chain-following Reality inbound 1, a plain inbound 2 and the AmneziaWG
// server, the probe set ensured for mon-client ams-1.
func monStandbyChain(t *testing.T) (*MonitoringService, *model.ChainHop, *model.ChainHop) {
	t.Helper()
	m := newMonitoringTestService(t)
	awgTestServer(t)
	follower := monInbound(t, 1, model.VLESS, true, model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "alice"})
	if err := database.GetDB().Model(follower).Updates(map[string]any{
		"follow_chain": true, "stream_settings": probeRealityStream(nil)}).Error; err != nil {
		t.Fatal(err)
	}
	monInbound(t, 2, model.VLESS, true, model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "bob"})
	monHop(t, "core-1", "inner", "joined", 0, false, "10.0.0.7")
	edgeA := monHop(t, "edge-a", "edge", "joined", 0, true, "a.example.net")
	edgeB := monHop(t, "edge-b", "edge", "joined", 0, false, "b.example.net")
	monHopUpdate(t, "edge-a", map[string]any{"reality_target": "www.a-neighbour.example:443"})
	monHopUpdate(t, "edge-b", map[string]any{"reality_target": "www.b-neighbour.example:443"})
	if _, err := m.EnsureProbeSet([]MonClient{{Id: "ams-1"}}); err != nil {
		t.Fatal(err)
	}
	return m, edgeA, edgeB
}

// monItems lists what GET /probe/configs hands out for one hop (or direct,
// with hop empty) as kind:inboundId, in the response's order.
func monItems(t *testing.T, m *MonitoringService, hop string) string {
	t.Helper()
	res, err := m.ProbeConfigs("203.0.113.10", hop, "")
	if err != nil {
		t.Fatalf("ProbeConfigs(hop=%q): %v", hop, err)
	}
	items := make([]string, 0, len(res.Items))
	for _, item := range res.Items {
		items = append(items, fmt.Sprintf("%s:%d", item.Kind, item.InboundId))
	}
	return strings.Join(items, ",")
}

// TestProbeConfigsLeaveChainFollowersOffStandbyEdges: through a standby edge
// the chain-following inbound has no probe link, while the plain inbound and
// AmneziaWG keep theirs; direct, the inner and the active edge carry all of
// them. A switch of the active edge moves the gap to the other edge and the
// revision with it, so mon-server rereads the material; with no active edge
// at all no edge carries the followers.
func TestProbeConfigsLeaveChainFollowersOffStandbyEdges(t *testing.T) {
	m, _, edgeB := monStandbyChain(t)
	revision := monRevision(t, m)
	const all, noFollower = "awg:0,xray:1,xray:2", "awg:0,xray:2"

	check := func(stage string, want map[string]string) {
		t.Helper()
		for hop, items := range want {
			if got := monItems(t, m, hop); got != items {
				t.Errorf("%s: items through %q = %s, want %s", stage, hop, got, items)
			}
		}
	}
	check("edge-a active", map[string]string{"": all, "core-1": all, "edge-a": all, "edge-b": noFollower})
	before := revision()

	if err := (&ChainService{}).SetActive(edgeB.Id); err != nil {
		t.Fatalf("SetActive(edge-b): %v", err)
	}
	check("edge-b active", map[string]string{"": all, "core-1": all, "edge-a": noFollower, "edge-b": all})
	switched := revision()
	if switched == before {
		t.Error("a switch of the active edge left the revision as it was")
	}

	if err := (&ChainService{}).ClearActive(); err != nil {
		t.Fatalf("ClearActive: %v", err)
	}
	check("no active edge", map[string]string{"": all, "core-1": all, "edge-a": noFollower, "edge-b": noFollower})
	if revision() == switched {
		t.Error("clearing the active edge left the revision as it was")
	}
}

// TestRevisionTracksTheChainFollowerFlag: the flag decides whether an
// inbound is probed through a standby edge, so it is probe material — even
// when its stream, already on the active edge's neighbour, does not change.
func TestRevisionTracksTheChainFollowerFlag(t *testing.T) {
	m, _, _ := monStandbyChain(t)
	revision := monRevision(t, m)
	flagged := revision()
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", 1).Update("follow_chain", false).Error; err != nil {
		t.Fatal(err)
	}
	if revision() == flagged {
		t.Error("taking the chain-follower flag off left the revision as it was")
	}
}

// monTargetKeys lists the stored targets as kind:inboundId@path, sorted.
func monTargetKeys(t *testing.T) string {
	t.Helper()
	var rows []model.MonTarget
	if err := database.GetDB().Order("inbound_kind, inbound_id, path").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, fmt.Sprintf("%s:%d@%s", r.InboundKind, r.InboundId, r.Path))
	}
	return strings.Join(keys, ",")
}

// monSeedTargets stores one target of ams-1 per kind:inboundId@path key.
func monSeedTargets(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		var kind, path string
		var id int
		ref, p, _ := strings.Cut(key, "@")
		k, i, _ := strings.Cut(ref, ":")
		kind, path = k, p
		fmt.Sscan(i, &id)
		if err := database.GetDB().Create(&model.MonTarget{MonClientId: "ams-1", InboundKind: kind, InboundId: id, Path: path, State: "DOWN"}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// TestStandbyEdgeTargetsOfChainFollowersArePruned: the panel keeps no target
// of a chain-following inbound through a standby edge — a stale DOWN there
// would fail verify and keep the edge from ever being proposed as the one to
// switch to. The switch drops the row of the edge that became standby, the
// ensure drops one left from before, clearing the active edge drops them on
// every edge; targets of the other inbounds and of AmneziaWG stay.
func TestStandbyEdgeTargetsOfChainFollowersArePruned(t *testing.T) {
	m, _, edgeB := monStandbyChain(t)
	monSeedTargets(t, "awg:0@edge:edge-b", "xray:1@direct", "xray:1@edge:edge-a", "xray:1@edge:edge-b", "xray:2@edge:edge-b")

	if _, err := m.EnsureProbeSet([]MonClient{{Id: "ams-1"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := monTargetKeys(t), "awg:0@edge:edge-b,xray:1@direct,xray:1@edge:edge-a,xray:2@edge:edge-b"; got != want {
		t.Errorf("after an ensure: targets = %s, want %s", got, want)
	}

	if err := (&ChainService{}).SetActive(edgeB.Id); err != nil {
		t.Fatal(err)
	}
	monSeedTargets(t, "xray:1@edge:edge-b")
	if got, want := monTargetKeys(t), "awg:0@edge:edge-b,xray:1@direct,xray:1@edge:edge-b,xray:2@edge:edge-b"; got != want {
		t.Errorf("after the switch to edge-b: targets = %s, want %s", got, want)
	}

	if err := (&ChainService{}).ClearActive(); err != nil {
		t.Fatal(err)
	}
	if got, want := monTargetKeys(t), "awg:0@edge:edge-b,xray:1@direct,xray:2@edge:edge-b"; got != want {
		t.Errorf("with no active edge: targets = %s, want %s", got, want)
	}
}
