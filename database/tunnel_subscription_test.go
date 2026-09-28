package database

import (
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The tunnel subscription link (docs/spec/tunnel-subscription.md §2) lives in
// its own table so the upstream TunnelClient model stays untouched; deleting a
// tunnel client by any path must take its link along, which only the foreign
// key with ON DELETE CASCADE guarantees.
func TestTunnelClientSubCascadesOnClientDelete(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
	db := GetDB()

	server := &model.TunnelServer{Kind: model.TunnelKindAwg}
	if err := db.Create(server).Error; err != nil {
		t.Fatal(err)
	}
	client := &model.TunnelClient{ServerId: server.Id, UUID: "aaaaaaaa-0000-0000-0000-000000000001", Name: "ivan-awg", Email: "ivan-awg", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatal(err)
	}
	link := &model.TunnelClientSub{ClientUUID: client.UUID, Kind: model.TunnelKindAwg, SubId: "sub-ivan"}
	if err := db.Create(link).Error; err != nil {
		t.Fatalf("create link: %v", err)
	}
	orphan := &model.TunnelClientSub{ClientUUID: "bbbbbbbb-0000-0000-0000-000000000002", Kind: model.TunnelKindAwg, SubId: "sub-x"}
	if err := db.Create(orphan).Error; err == nil {
		t.Error("a link to a missing tunnel client was accepted: no foreign key")
	}

	if err := db.Delete(&model.TunnelClient{}, client.Id).Error; err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&model.TunnelClientSub{}).Count(&n)
	if n != 0 {
		t.Errorf("%d links survived their client", n)
	}
}
