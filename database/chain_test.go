package database

import (
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The chain registry is worth nothing if two hops can share a name: the bot,
// the UI and the monitoring path all address a hop by name (§2.1), and the
// document builder looks a hop up by it. The uniqueness lives in the schema,
// as index idx_chain_hops_name, so this checks the migration actually created
// it rather than trusting the struct tag.
func TestChainHopsTableEnforcesUniqueName(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { CloseDB() })

	db := GetDB()
	if !db.Migrator().HasTable(&model.ChainHop{}) {
		t.Fatal("chain_hops was not created by initModels")
	}
	for _, index := range []string{"idx_chain_hops_name", "idx_chain_hops_next", "idx_chain_hops_role"} {
		if !db.Migrator().HasIndex(&model.ChainHop{}, index) {
			t.Errorf("index %s missing", index)
		}
		if table, ok := namedIndexes[index]; !ok || table != "chain_hops" {
			t.Errorf("namedIndexes[%s] = %q, want chain_hops", index, table)
		}
	}

	first := &model.ChainHop{Name: "edge-a", Host: "a.example.net", Role: model.ChainRoleEdge,
		State: model.ChainStatePending, SubPort: 2096, SubScheme: "https"}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first hop: %v", err)
	}
	if first.Id == 0 {
		t.Fatal("id was not assigned")
	}
	if first.CreatedAt == 0 || first.UpdatedAt == 0 {
		t.Fatalf("autoCreateTime/autoUpdateTime did not fill: %+v", first)
	}

	duplicate := &model.ChainHop{Name: "edge-a", Host: "b.example.net", Role: model.ChainRoleEdge,
		State: model.ChainStatePending, SubPort: 2096, SubScheme: "https"}
	if err := db.Create(duplicate).Error; err == nil {
		t.Fatal("a second hop named edge-a was accepted")
	}

	// next_hop_id is nullable — a hop whose next hop is the panel itself
	// stores NULL, not 0 (§2.2).
	inner := &model.ChainHop{Name: "inner-1", Host: "10.0.0.7", Role: model.ChainRoleInner,
		State: model.ChainStateJoined, SubPort: 2096, SubScheme: "https"}
	if err := db.Create(inner).Error; err != nil {
		t.Fatalf("create inner: %v", err)
	}
	var loaded model.ChainHop
	if err := db.First(&loaded, inner.Id).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.NextHopId != nil {
		t.Fatalf("NextHopId = %v, want nil", *loaded.NextHopId)
	}
}

// TestChainHopsGainTheNextHopCheck (#254): a registry from before the host
// reachability check gets its columns on the next start, and the hops already
// in it read as never reported — no check, no round trip.
func TestChainHopsGainTheNextHopCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x-ui.db")
	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	db := GetDB()
	columns := []string{"next_check_at", "next_check_sent", "next_check_loss_pct", "next_check_rtt_ms"}
	for _, column := range columns {
		if err := db.Exec("ALTER TABLE chain_hops DROP COLUMN " + column).Error; err != nil {
			t.Fatalf("drop %s to fake the old schema: %v", column, err)
		}
	}
	if err := db.Exec(`INSERT INTO chain_hops (name, host, role, state, sub_port, sub_scheme, position, is_active)
		VALUES ('edge-a', 'a.example.net', 'edge', 'joined', 2096, 'https', 0, 0)`).Error; err != nil {
		t.Fatalf("insert an old row: %v", err)
	}
	CloseDB()

	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB over the old schema: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
	db = GetDB()
	for _, column := range columns {
		if !db.Migrator().HasColumn(&model.ChainHop{}, column) {
			t.Errorf("column %s was not added", column)
		}
	}
	var hop model.ChainHop
	if err := db.Where("name = ?", "edge-a").First(&hop).Error; err != nil {
		t.Fatal(err)
	}
	if hop.NextCheckAt != 0 || hop.NextCheckSent != 0 || hop.NextCheckRttMs != nil {
		t.Errorf("an old hop reads as checked: %+v", hop)
	}
}
