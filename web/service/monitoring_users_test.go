package service

import (
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestEnsureLinksTunnelProbesToTheProbeSubId: the AmneziaWG probe peers are
// linked to the shared probe subId like the xray probes carry it, peers made
// before the link existed included, and all of them are monitoring's.
func TestEnsureLinksTunnelProbesToTheProbeSubId(t *testing.T) {
	m := newMonitoringTestService(t)
	awgTestServer(t)
	// A peer from before links existed.
	old := &model.TunnelClient{ServerId: 1, UUID: "bbbbbbbb-0000-0000-0000-000000000009", Name: "probe-awg-ams-1-direct",
		Email: "probe-awg-ams-1-direct", Enable: true, IPv4Address: "10.66.66.40/32"}
	if err := database.GetDB().Create(old).Error; err != nil {
		t.Fatal(err)
	}

	res, err := m.EnsureProbeSet([]MonClient{{Id: "ams-1", State: "ONLINE"}})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	peers := awgProbePeers(t)
	uuids := []string{}
	for _, p := range peers {
		uuids = append(uuids, p.UUID)
	}
	links, err := (&TunnelSubscriptionService{}).SubIdsByUUIDs(uuids)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 || len(links) != 2 {
		t.Fatalf("peers %d, links %v", len(peers), links)
	}
	for uuid, subId := range links {
		if subId != res.SubId {
			t.Errorf("peer %s linked to %q, want the probe subId %q", uuid, subId, res.SubId)
		}
	}

	mon, err := (&SubUserService{}).Get(model.SubUserMonitoringKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(mon.Clients) != 2 {
		t.Errorf("monitoring's clients: %+v", mon.Clients)
	}
}
