package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

func TestTunnelSubscriptionSetClearAndRead(t *testing.T) {
	initUsersTestDB(t)
	links := &TunnelSubscriptionService{}
	a := awgPeer(t, 1, "a", "")
	b := awgPeer(t, 2, "b", "")

	if err := links.Set(a.UUID, model.TunnelKindAwg, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := links.Set(b.UUID, model.TunnelKindAwg, "s2"); err != nil {
		t.Fatal(err)
	}
	// Set again moves the client to another subscription.
	if err := links.Set(b.UUID, model.TunnelKindAwg, "s1"); err != nil {
		t.Fatal(err)
	}
	got, err := links.SubIdsByUUIDs([]string{a.UUID, b.UUID, "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[a.UUID] != "s1" || got[b.UUID] != "s1" {
		t.Errorf("SubIdsByUUIDs = %v", got)
	}
	if err := links.Clear(a.UUID); err != nil {
		t.Fatal(err)
	}
	if got, _ := links.SubIdsByUUIDs([]string{a.UUID}); len(got) != 0 {
		t.Errorf("link survived Clear: %v", got)
	}
	// Clearing what is not linked is not an error.
	if err := links.Clear(a.UUID); err != nil {
		t.Errorf("second Clear: %v", err)
	}
	// Set with an empty subId is a Clear.
	if err := links.Set(b.UUID, model.TunnelKindAwg, ""); err != nil {
		t.Fatal(err)
	}
	var n int64
	database.GetDB().Model(&model.TunnelClientSub{}).Count(&n)
	if n != 0 {
		t.Errorf("%d links left", n)
	}
}

// wgPeer is awgPeer for native WireGuard.
func wgPeer(t *testing.T, n int, email, subId string) *model.TunnelClient {
	t.Helper()
	server, err := (&WgService{}).GetServer()
	if err != nil {
		t.Fatal(err)
	}
	c := &model.TunnelClient{ServerId: server.Id, UUID: uuidN(n), Name: email, Email: email, Enable: true,
		IPv4Address: fmt.Sprintf("10.77.77.%d/32", n+1)}
	if err := database.GetDB().Create(c).Error; err != nil {
		t.Fatal(err)
	}
	if subId != "" {
		if err := (&TunnelSubscriptionService{}).Set(c.UUID, model.TunnelKindWg, subId); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// initTunnelSubTestDB is initUsersTestDB with an empty subscription cache:
// the cache outlives the database of the test before.
func initTunnelSubTestDB(t *testing.T) {
	t.Helper()
	initUsersTestDB(t)
	InvalidateTunnelSubCache()
	t.Cleanup(InvalidateTunnelSubCache)
}

func TestTunnelSubscriptionClientsBySubId(t *testing.T) {
	initTunnelSubTestDB(t)
	a := awgPeer(t, 1, "ivan-phone", "s1")
	awgPeer(t, 2, "petr-phone", "s2")
	awgPeer(t, 3, "robot-peer", "")
	b := wgPeer(t, 4, "ivan-laptop", "s1")

	got, err := (&TunnelSubscriptionService{}).ClientsBySubId("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ClientsBySubId(s1) = %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Kind != model.TunnelKindAwg || got[0].Client.UUID != a.UUID {
		t.Errorf("first entry = %s %s, want the AmneziaWG peer %s", got[0].Kind, got[0].Client.UUID, a.UUID)
	}
	if got[1].Kind != model.TunnelKindWg || got[1].Client.UUID != b.UUID {
		t.Errorf("second entry = %s %s, want the WireGuard peer %s", got[1].Kind, got[1].Client.UUID, b.UUID)
	}
	for _, e := range got {
		want, err := (&AwgService{}).GetClientConfigByUUID(e.Client.UUID)
		if e.Kind == model.TunnelKindWg {
			want, err = (&WgService{}).GetClientConfigByUUID(e.Client.UUID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.Conf != want || !strings.HasPrefix(e.Conf, "[Interface]\n") {
			t.Errorf("%s conf is not the panel's own .conf:\n%s", e.Client.Email, e.Conf)
		}
	}

	none, err := (&TunnelSubscriptionService{}).ClientsBySubId("unknown")
	if err != nil || len(none) != 0 {
		t.Errorf("ClientsBySubId(unknown) = %v, %v; want nothing", none, err)
	}
}

// Userinfo adds a subscription's tunnel clients up the way /sub adds its
// xray clients (sub/subService.go buildSubs).
func TestTunnelSubscriptionUserinfo(t *testing.T) {
	entry := func(up, down, total, expiry int64, enable bool) TunnelSubEntry {
		return TunnelSubEntry{Client: model.TunnelClient{Upload: up, Download: down, TotalGB: total, ExpiryTime: expiry, Enable: enable}}
	}
	cases := []struct {
		name       string
		entries    []TunnelSubEntry
		header     string
		wantEnable bool
	}{
		{"one client", []TunnelSubEntry{entry(1, 2, 1000, 1767225600000, true)},
			"upload=1; download=2; total=1000; expire=1767225600", true},
		{"limits add up, one expiry", []TunnelSubEntry{entry(1, 2, 1000, 1767225600000, false), entry(10, 20, 500, 1767225600000, true)},
			"upload=11; download=22; total=1500; expire=1767225600", true},
		{"one unlimited makes the total unlimited", []TunnelSubEntry{entry(1, 2, 1000, 0, true), entry(3, 4, 0, 0, true)},
			"upload=4; download=6; total=0; expire=0", true},
		{"different expiries make no expiry", []TunnelSubEntry{entry(0, 0, 0, 1767225600000, true), entry(0, 0, 0, 1767312000000, true)},
			"upload=0; download=0; total=0; expire=0", true},
		{"every client off", []TunnelSubEntry{entry(0, 0, 0, 0, false), entry(0, 0, 0, 0, false)},
			"upload=0; download=0; total=0; expire=0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header, enable := (&TunnelSubscriptionService{}).Userinfo(tc.entries)
			if header != tc.header || enable != tc.wantEnable {
				t.Errorf("Userinfo = %q, %v; want %q, %v", header, enable, tc.header, tc.wantEnable)
			}
		})
	}
}

// The public route reads through a short cache, which a change of the links
// drops at once and anything else outlives by at most the TTL.
func TestTunnelSubscriptionCache(t *testing.T) {
	initTunnelSubTestDB(t)
	svc := &TunnelSubscriptionService{}
	count := func() int {
		t.Helper()
		got, err := svc.ClientsBySubId("s1")
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	awgPeer(t, 1, "a", "s1")
	if n := count(); n != 1 {
		t.Fatalf("%d entries, want 1", n)
	}

	// A link written past the service is not seen while the entry is fresh.
	b := awgPeer(t, 2, "b", "")
	if err := database.GetDB().Create(&model.TunnelClientSub{ClientUUID: b.UUID, Kind: model.TunnelKindAwg, SubId: "s1"}).Error; err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("%d entries from the cache, want 1", n)
	}

	// Set drops it.
	c := awgPeer(t, 3, "c", "")
	if err := svc.Set(c.UUID, model.TunnelKindAwg, "s1"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 3 {
		t.Fatalf("%d entries after Set, want 3", n)
	}
	// So does Clear.
	if err := svc.Clear(c.UUID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("%d entries after Clear, want 2", n)
	}
	// And so does the explicit invalidation the client handlers call.
	database.GetDB().Where("client_uuid = ?", b.UUID).Delete(&model.TunnelClientSub{})
	InvalidateTunnelSubCache()
	if n := count(); n != 1 {
		t.Fatalf("%d entries after the invalidation, want 1", n)
	}

	// Anything else waits out the TTL.
	defer func(ttl time.Duration) { tunnelSubCacheTTL = ttl }(tunnelSubCacheTTL)
	tunnelSubCacheTTL = 20 * time.Millisecond
	InvalidateTunnelSubCache()
	count()
	if err := database.GetDB().Create(&model.TunnelClientSub{ClientUUID: b.UUID, Kind: model.TunnelKindAwg, SubId: "s1"}).Error; err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if n := count(); n != 2 {
		t.Fatalf("%d entries after the TTL, want 2", n)
	}
}
