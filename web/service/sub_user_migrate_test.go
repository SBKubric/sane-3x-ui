package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// initUsersTestDB opens a fresh database; InitDB already runs the users
// migration once through its post-migrate hook.
func initUsersTestDB(t *testing.T) {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
}

// awgPeer stores an AmneziaWG client straight in the table, the way an older
// panel left it, optionally linked to a subscription.
func awgPeer(t *testing.T, n int, email, subId string) *model.TunnelClient {
	t.Helper()
	server, err := (&AwgService{}).GetServer()
	if err != nil {
		t.Fatal(err)
	}
	c := &model.TunnelClient{ServerId: server.Id, UUID: uuidN(n), Name: email, Email: email, Enable: true}
	db := database.GetDB()
	if err := db.Create(c).Error; err != nil {
		t.Fatal(err)
	}
	if subId != "" {
		if err := (&TunnelSubscriptionService{}).Set(c.UUID, model.TunnelKindAwg, subId); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// usersInbound stores a disabled inbound of the protocol with the given
// clients, and a traffic row per distinct email (a legacy duplicate email
// shares the row, as it would have). Disabled, so the client paths never reach
// for the xray API.
func usersInbound(t *testing.T, id int, protocol model.Protocol, remark string, clients ...model.Client) *model.Inbound {
	t.Helper()
	settings := map[string]any{"clients": clients}
	if protocol == model.VLESS {
		settings["decryption"] = "none"
	}
	if protocol == model.Shadowsocks {
		settings["method"] = "2022-blake3-aes-256-gcm"
	}
	raw, _ := json.Marshal(settings)
	ib := &model.Inbound{Id: id, Port: 20000 + id, Protocol: protocol, Tag: fmt.Sprintf("inbound-users-%d", id),
		Remark: remark, Settings: string(raw), Enable: false, StreamSettings: "{}", Sniffing: "{}"}
	db := database.GetDB()
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	for _, c := range clients {
		if c.Email == "" {
			continue
		}
		row := xray.ClientTraffic{InboundId: id, Email: c.Email, Enable: c.Enable}
		if err := db.Where("email = ?", c.Email).FirstOrCreate(&row).Error; err != nil {
			t.Fatalf("create client traffic: %v", err)
		}
	}
	return ib
}

func uuidN(n int) string {
	const hex = "0123456789abcdef"
	return "cccccccc-0000-0000-0000-0000000000" + string(hex[n/16%16]) + string(hex[n%16])
}

func usersByKey(t *testing.T) map[string]model.SubUser {
	t.Helper()
	var users []model.SubUser
	if err := database.GetDB().Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]model.SubUser{}
	for _, u := range users {
		out[u.SubId] = u
	}
	return out
}

// TestSyncUsersFromExistingClients is the start-up migration: one user per
// distinct subId, named after its first client, with -2, -3 on a clash; the
// probe subId goes to monitoring; the technical users exist; nothing about the
// clients changes.
func TestSyncUsersFromExistingClients(t *testing.T) {
	initUsersTestDB(t)
	if err := (&SettingService{}).SetMonProbeSubId("probesub"); err != nil {
		t.Fatal(err)
	}
	usersInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "ivan", SubID: "s-ivan"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "ivan-phone", SubID: "s-ivan"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "nosub"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000004", Email: "probe-1", SubID: "probesub"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000005", Email: "robot", SubID: "s-robotic"},
	)
	usersInbound(t, 2, model.VLESS, "de",
		// A legacy clash: "Ivan" in another inbound, under another subId.
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000006", Email: "Ivan", SubID: "s-ivan2"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000007", Email: "ivan", SubID: "s-ivan3"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000008", Email: "petr", SubID: "s-petr"},
	)
	awgPeer(t, 1, "petr-awg", "s-petr")
	awgPeer(t, 2, "olga-awg", "s-olga")
	awgPeer(t, 3, "legacy-awg", "")
	before := map[int]string{1: mustInbound(t, &InboundService{}, 1).Settings, 2: mustInbound(t, &InboundService{}, 2).Settings}

	if err := (&SubUserService{}).Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	want := map[string]string{
		model.SubUserRobotKey:      "robot",
		model.SubUserMonitoringKey: "monitoring",
		"s-ivan":                   "ivan",
		"s-robotic":                "robot-2",
		"s-ivan2":                  "Ivan-2",
		"s-ivan3":                  "ivan-3",
		"s-petr":                   "petr",
		"s-olga":                   "olga-awg",
	}
	got := usersByKey(t)
	if len(got) != len(want) {
		t.Errorf("users: got %d, want %d: %+v", len(got), len(want), got)
	}
	for key, name := range want {
		if got[key].Name != name {
			t.Errorf("user %q: name %q, want %q", key, got[key].Name, name)
		}
	}
	if _, ok := got["probesub"]; ok {
		t.Error("the probe subId became a user of its own")
	}
	for id, settings := range before {
		if after := mustInbound(t, &InboundService{}, id).Settings; after != settings {
			t.Errorf("inbound %d settings changed by the migration", id)
		}
	}

	// Idempotent: a second run adds nothing and renames nothing, even after
	// a user was renamed by hand.
	if err := database.GetDB().Model(&model.SubUser{}).Where("sub_id = ?", "s-petr").Update("name", "pyotr").Error; err != nil {
		t.Fatal(err)
	}
	if err := (&SubUserService{}).Sync(); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	again := usersByKey(t)
	if len(again) != len(want) || again["s-petr"].Name != "pyotr" {
		t.Errorf("second Sync changed users: %+v", again)
	}
}

// TestInitDBCreatesTechnicalUsers: a fresh database comes up with robot and
// monitoring already there, from the post-migrate hook.
func TestInitDBCreatesTechnicalUsers(t *testing.T) {
	initUsersTestDB(t)
	got := usersByKey(t)
	if got[model.SubUserRobotKey].Name != model.SubUserRobot || got[model.SubUserMonitoringKey].Name != model.SubUserMonitoring {
		t.Errorf("technical users after InitDB: %+v", got)
	}
}
