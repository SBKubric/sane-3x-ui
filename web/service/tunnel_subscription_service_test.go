package service

import (
	"testing"

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
