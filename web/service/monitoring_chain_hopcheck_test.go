package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

func int64Ptr(value int64) *int64 { return &value }

// stateHop is one hop of GET /state's chain as mon-server reads it.
func stateHop(t *testing.T, m *MonitoringService, name string) map[string]any {
	t.Helper()
	st, err := m.State()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(st.Chain)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Hops []map[string]any `json:"hops"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, hop := range wire.Hops {
		if hop["name"] == name {
			return hop
		}
	}
	t.Fatalf("no hop %q in chain.hops: %s", name, raw)
	return nil
}

// TestStateNamesEachHopsNextHop (#254): chain.hops[].next is the hop it dials
// inward — the same one its document names — and "" for the panel itself. A
// hop that never entered is skipped, as the document skips it.
func TestStateNamesEachHopsNextHop(t *testing.T) {
	m := newMonitoringTestService(t)
	registry := &ChainService{}
	joinedWithSecret(t, registry, AddHopInput{Name: "inner-1", Host: "10.0.0.7", Role: chain.RoleInner})
	joinedWithSecret(t, registry, AddHopInput{Name: "inner-2", Host: "10.0.0.8", Role: chain.RoleInner})
	joinedWithSecret(t, registry, AddHopInput{Name: "edge-a", Host: "a.example.net", Role: chain.RoleEdge})
	joinedWithSecret(t, registry, AddHopInput{Name: "edge-b", Host: "b.example.net", Role: chain.RoleEdge})
	between := 1
	pendingHop(t, registry, AddHopInput{Name: "inner-new", Host: "10.0.0.9", Role: chain.RoleInner, Position: &between})

	for name, want := range map[string]string{"inner-1": "", "inner-2": "inner-1", "edge-a": "inner-2", "edge-b": "inner-2"} {
		hop := stateHop(t, m, name)
		next, present := hop["next"]
		if !present || next != want {
			t.Errorf("%s next = %v (present %t), want %q", name, next, present, want)
		}
	}
}

// TestStateCarriesTheNextHopCheck (#254): nextHopCheck is absent until the
// hop has reported one; then it is the latest check, with a null average when
// every echo was lost. It never moves the revision.
func TestStateCarriesTheNextHopCheck(t *testing.T) {
	m := newMonitoringTestService(t)
	registry := &ChainService{}
	wave := &ChainWaveService{}
	joinedWithSecret(t, registry, AddHopInput{Name: "inner-1", Host: "10.0.0.7", Role: chain.RoleInner})
	joinedWithSecret(t, registry, AddHopInput{Name: "edge-a", Host: "a.example.net", Role: chain.RoleEdge})
	rev := monRevision(t, m)
	before := rev()

	if _, present := stateHop(t, m, "inner-1")["nextHopCheck"]; present {
		t.Error("a hop that never reported has a nextHopCheck")
	}

	at := time.Now().Add(-time.Minute).UnixMilli()
	err := wave.RecordNextHopChecks("inner-1", &chain.HopCheck{At: at, Sent: 10, LossPct: 20, RttAvgMs: int64Ptr(31)},
		[]chain.OuterAck{{Name: "edge-a", NextHopCheck: &chain.HopCheck{At: at, Sent: 10, LossPct: 100}}})
	if err != nil {
		t.Fatalf("RecordNextHopChecks: %v", err)
	}
	inner, _ := json.Marshal(stateHop(t, m, "inner-1")["nextHopCheck"])
	if want := `{"at":` + jsonNumber(at) + `,"lossPct":20,"rttAvgMs":31,"sent":10}`; string(inner) != want {
		t.Errorf("inner-1 nextHopCheck = %s, want %s", inner, want)
	}
	edge, _ := json.Marshal(stateHop(t, m, "edge-a")["nextHopCheck"])
	if !strings.Contains(string(edge), `"rttAvgMs":null`) || !strings.Contains(string(edge), `"lossPct":100`) {
		t.Errorf("edge-a nextHopCheck = %s, want 100 %% lost and a null average", edge)
	}

	if after := rev(); after != before {
		t.Errorf("a host reachability check moved the revision %s -> %s", before, after)
	}
}

// TestRevisionHashesTheNextHop (#254): next is part of the chain mon-server
// builds its picture from, so it is in the revision's material; the check
// is not.
func TestRevisionHashesTheNextHop(t *testing.T) {
	c := &MonChain{Hops: []MonChainHop{{Name: "edge-a", Role: "edge", Host: "a.example.net", State: "joined", Next: "inner-1",
		NextHopCheck: &chain.HopCheck{At: 1, Sent: 10, LossPct: 0, RttAvgMs: int64Ptr(1)}}}}
	raw, _ := json.Marshal(c.revisionMaterial())
	if !strings.Contains(string(raw), `"next":"inner-1"`) {
		t.Errorf("revision material lacks next: %s", raw)
	}
	if strings.Contains(string(raw), "Check") || strings.Contains(string(raw), "lossPct") {
		t.Errorf("revision material carries the check: %s", raw)
	}
}

func jsonNumber(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
