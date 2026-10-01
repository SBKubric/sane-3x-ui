package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// opsFixture: a vless inbound "NL Amsterdam #1" (id 1) with an unrelated
// client, a trojan inbound "de" (id 2) with one too, and the AmneziaWG
// inbound row "awg" (id 5).
func opsFixture(t *testing.T) {
	t.Helper()
	initUsersTestDB(t)
	usersInbound(t, 1, model.VLESS, "NL Amsterdam #1",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "other-nl", SubID: "s-other", Flow: "xtls-rprx-vision", Enable: true})
	usersInbound(t, 2, model.Trojan, "de",
		model.Client{Password: "other-pass", Email: "other-de", SubID: "s-other", Enable: true})
	usersInbound(t, 5, model.AmneziaWG, "awg")
	if _, err := (&AwgService{}).GetServer(); err != nil {
		t.Fatal(err)
	}
}

func clientByName(t *testing.T, v *SubUserView, name string) SubUserClient {
	t.Helper()
	for _, c := range v.Clients {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("user %s has no client %s: %+v", v.Name, name, v.Clients)
	return SubUserClient{}
}

func countAwgClients(t *testing.T) int64 {
	t.Helper()
	var n int64
	database.GetDB().Model(&model.TunnelClient{}).Count(&n)
	return n
}

// TestCreateUserWithSeveralProtocols: one call makes a client per inbound,
// named <user>-<remark>, all under the user's subId, each with the limits.
func TestCreateUserWithSeveralProtocols(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	v, err := users.Create(SubUserCreate{Name: "ivan", TgId: 42, InboundIds: []int{1, 2, 5},
		SubUserParams: SubUserParams{TotalGB: 50 << 30, ExpiryTime: 1893456000000, LimitIp: 2}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.Name != "ivan" || len(v.SubId) != 16 || len(v.Clients) != 3 {
		t.Fatalf("created: %+v", v)
	}
	for _, name := range []string{"ivan-NL-Amsterdam-1", "ivan-de", "ivan-awg"} {
		c := clientByName(t, v, name)
		if c.SubId != v.SubId || !c.Enable || c.TotalGB != 50<<30 || c.ExpiryTime != 1893456000000 || c.LimitIp != 2 || c.TgId != 42 {
			t.Errorf("client %s: %+v", name, c)
		}
	}
	// The vless client travels like the inbound's other clients.
	for _, c := range mustClients(t, &InboundService{}, 1) {
		if c.Email == "ivan-NL-Amsterdam-1" && (c.Flow != "xtls-rprx-vision" || c.ID == "") {
			t.Errorf("vless client: %+v", c)
		}
	}
	// Limits are per client: the subscription shows the sum.
	if v.Total != 3*(50<<30) {
		t.Errorf("total = %d", v.Total)
	}

	// The name is taken now, and so are the reserved ones.
	for _, name := range []string{"IVAN", "robot", "Monitoring", "probe-x", ""} {
		if _, err := users.Create(SubUserCreate{Name: name}); err == nil {
			t.Errorf("Create(%q) succeeded", name)
		}
	}
	// A subId is one user's.
	if _, err := users.Create(SubUserCreate{Name: "petr", SubId: v.SubId}); err == nil {
		t.Error("Create with ivan's subId succeeded")
	}
	// A user without clients is fine: it keeps its link.
	if p, err := users.Create(SubUserCreate{Name: "petr", SubId: "s-petr"}); err != nil || p.SubId != "s-petr" || len(p.Clients) != 0 {
		t.Errorf("Create(petr) without inbounds: %+v, %v", p, err)
	}
}

// TestCreateUserRollsBack: a failure half-way leaves nothing behind — no
// client, no traffic row, no link, no user.
func TestCreateUserRollsBack(t *testing.T) {
	opsFixture(t)
	// Inbound 2 refuses every write, so the third client of the create fails
	// after a vless and an AWG client were made.
	if err := database.GetDB().Exec(`CREATE TRIGGER fail_inbound_2 BEFORE UPDATE ON inbounds WHEN NEW.id = 2
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	before := mustInbound(t, &InboundService{}, 1).Settings
	awgBefore := countAwgClients(t)

	_, err := (&SubUserService{}).Create(SubUserCreate{Name: "ivan", InboundIds: []int{1, 5, 2}})
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("Create through a failing inbound: %v", err)
	}
	if after := mustInbound(t, &InboundService{}, 1).Settings; after != before {
		t.Errorf("inbound 1 kept the rolled-back client:\n%s", after)
	}
	var traffic int64
	database.GetDB().Model(&xray.ClientTraffic{}).Where("email LIKE ?", "ivan-%").Count(&traffic)
	if traffic != 0 {
		t.Errorf("%d traffic rows of rolled-back clients survived", traffic)
	}
	if n := countAwgClients(t); n != awgBefore {
		t.Errorf("AWG clients: %d, want %d", n, awgBefore)
	}
	var links, users int64
	database.GetDB().Model(&model.TunnelClientSub{}).Count(&links)
	database.GetDB().Model(&model.SubUser{}).Where("name = ?", "ivan").Count(&users)
	if links != 0 || users != 0 {
		t.Errorf("links %d, users named ivan %d after the rollback", links, users)
	}
}

// TestAddAndRemoveProtocol: a protocol is added with the parameters of the
// user's xray client (else of its AWG client), removed again, and removing
// the last client keeps the user and its subId.
func TestAddAndRemoveProtocol(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	v, err := users.Create(SubUserCreate{Name: "ivan", InboundIds: []int{2},
		SubUserParams: SubUserParams{TotalGB: 10 << 30, ExpiryTime: -86400000 * 30, LimitIp: 1, Reset: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.AddProtocol(v.SubId, 2, false); err == nil {
		t.Error("adding an inbound the user already has succeeded")
	}
	v, err = users.AddProtocol(v.SubId, 5, false)
	if err != nil {
		t.Fatalf("AddProtocol(awg): %v", err)
	}
	awg := clientByName(t, v, "ivan-awg")
	if awg.TotalGB != 10<<30 || awg.ExpiryTime != -86400000*30 || awg.LimitIp != 1 || awg.Reset != 30 || awg.SubId != v.SubId {
		t.Errorf("AWG client did not copy the xray client: %+v", awg)
	}

	// Only AWG left: the next protocol copies from it.
	if v, err = users.RemoveProtocol(v.SubId, 2); err != nil {
		t.Fatalf("RemoveProtocol(trojan): %v", err)
	}
	if len(v.Clients) != 1 {
		t.Fatalf("clients after removing trojan: %+v", v.Clients)
	}
	if v, err = users.AddProtocol(v.SubId, 1, false); err != nil {
		t.Fatalf("AddProtocol(vless): %v", err)
	}
	if c := clientByName(t, v, "ivan-NL-Amsterdam-1"); c.TotalGB != 10<<30 || c.LimitIp != 1 {
		t.Errorf("vless client did not copy the AWG client: %+v", c)
	}

	subId := v.SubId
	for _, ib := range []int{1, 5} {
		if v, err = users.RemoveProtocol(subId, ib); err != nil {
			t.Fatalf("RemoveProtocol(%d): %v", ib, err)
		}
	}
	if v.SubId != subId || v.Name != "ivan" || len(v.Clients) != 0 {
		t.Errorf("user after its last client went: %+v", v)
	}
	if got, err := users.Get(subId); err != nil || got.Name != "ivan" {
		t.Errorf("Get after the last client went: %+v, %v", got, err)
	}
	if _, err := users.RemoveProtocol(subId, 5); err == nil {
		t.Error("removing a protocol the user does not have succeeded")
	}
}

// TestEnableCascades: switching a user switches every client of it, xray and
// tunnel alike.
func TestEnableCascades(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	v, err := users.Create(SubUserCreate{Name: "ivan", InboundIds: []int{1, 2, 5}})
	if err != nil {
		t.Fatal(err)
	}
	if v, err = users.SetEnable(v.SubId, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	for _, c := range v.Clients {
		if c.Enable {
			t.Errorf("client %s still enabled", c.Name)
		}
	}
	if v.Enable {
		t.Error("the disabled user reads as enabled")
	}
	var ct xray.ClientTraffic
	database.GetDB().Where("email = ?", "ivan-de").First(&ct)
	if ct.Enable {
		t.Error("the traffic row of ivan-de stayed enabled")
	}
	// Other users' clients are not touched.
	for _, c := range mustClients(t, &InboundService{}, 1) {
		if c.Email == "other-nl" && !c.Enable {
			t.Error("disabling ivan disabled other-nl")
		}
	}
	if v, err = users.SetEnable(v.SubId, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	for _, c := range v.Clients {
		if !c.Enable {
			t.Errorf("client %s still disabled", c.Name)
		}
	}
}

// TestDeleteUser removes its clients and the user; technical users stay.
func TestDeleteUser(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	v, err := users.Create(SubUserCreate{Name: "ivan", InboundIds: []int{1, 5}})
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Delete(v.SubId); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := users.Get(v.SubId); err == nil {
		t.Error("the deleted user is still there")
	}
	for _, c := range mustClients(t, &InboundService{}, 1) {
		if c.Email == "ivan-NL-Amsterdam-1" {
			t.Error("the deleted user's vless client is still there")
		}
	}
	if n := countAwgClients(t); n != 0 {
		t.Errorf("%d AWG clients left", n)
	}
	for _, key := range []string{model.SubUserRobotKey, model.SubUserMonitoringKey} {
		if err := users.Delete(key); err == nil {
			t.Errorf("deleting %s succeeded", key)
		}
		if _, err := users.SetEnable(key, false); err == nil {
			t.Errorf("disabling %s succeeded", key)
		}
		if _, err := users.AddProtocol(key, 1, false); err == nil {
			t.Errorf("adding a protocol to %s succeeded", key)
		}
	}
}

// TestAssignRobotClients: a client without a subscription, xray or AWG, is
// given to a user; a client that already has a user is not.
func TestAssignRobotClients(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	if _, err := (&InboundService{}).AddInboundClient(clientsPayload(2,
		model.Client{Password: "legacy-pass", Email: "legacy-de", Enable: true})); err != nil {
		t.Fatal(err)
	}
	awgPeer(t, 7, "legacy-awg", "")
	v, err := users.Create(SubUserCreate{Name: "ivan"})
	if err != nil {
		t.Fatal(err)
	}
	robot, _ := users.Get(model.SubUserRobotKey)
	if len(robot.Clients) != 2 {
		t.Fatalf("robot's clients: %+v", robot.Clients)
	}

	for _, name := range []string{"legacy-de", "legacy-awg"} {
		if v, err = users.Assign(v.SubId, name); err != nil {
			t.Fatalf("Assign(%s): %v", name, err)
		}
	}
	if c := clientByName(t, v, "legacy-de"); c.SubId != v.SubId {
		t.Errorf("legacy-de: %+v", c)
	}
	if c := clientByName(t, v, "legacy-awg"); c.SubId != v.SubId {
		t.Errorf("legacy-awg: %+v", c)
	}
	if robot, _ = users.Get(model.SubUserRobotKey); len(robot.Clients) != 0 {
		t.Errorf("robot still has %+v", robot.Clients)
	}
	// Not robot's: refused.
	if _, err := users.Assign(v.SubId, "other-nl"); err == nil {
		t.Error("assigning another user's client succeeded")
	}
	if _, err := users.Assign(model.SubUserMonitoringKey, "legacy-de"); err == nil {
		t.Error("assigning to monitoring succeeded")
	}
}

// TestUnlinkedAwgClientIsLinked: an AWG client with the generated name and no
// subscription is linked instead of a new one being made — on request; one
// that belongs to another user stops the whole create.
func TestUnlinkedAwgClientIsLinked(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	peer := awgPeer(t, 1, "ivan-awg", "")

	_, err := users.Create(SubUserCreate{Name: "ivan", InboundIds: []int{1, 5}})
	var conflict *SubUserConflict
	if !errors.As(err, &conflict) || conflict.Code != SubUserConflictAwgLinkable || conflict.Client != "ivan-awg" {
		t.Fatalf("Create over an unlinked AWG client: %v", err)
	}
	if _, err := users.Find("ivan"); err == nil {
		t.Error("the refused create left a user")
	}

	v, err := users.Create(SubUserCreate{Name: "ivan", InboundIds: []int{1, 5}, LinkExisting: true})
	if err != nil {
		t.Fatalf("Create linking the AWG client: %v", err)
	}
	if c := clientByName(t, v, "ivan-awg"); c.Key != peer.UUID || c.SubId != v.SubId {
		t.Errorf("ivan-awg: %+v, want the existing peer linked", c)
	}
	if n := countAwgClients(t); n != 1 {
		t.Errorf("%d AWG clients, want the one that was there", n)
	}

	// petr-awg is linked to ivan: creating petr stops, nothing is made.
	if err := (&TunnelSubscriptionService{}).Set(awgPeer(t, 2, "petr-awg", "").UUID, model.TunnelKindAwg, v.SubId); err != nil {
		t.Fatal(err)
	}
	before := mustInbound(t, &InboundService{}, 1).Settings
	if _, err := users.Create(SubUserCreate{Name: "petr", InboundIds: []int{1, 5}, LinkExisting: true}); err == nil {
		t.Fatal("Create over another user's AWG client succeeded")
	}
	if mustInbound(t, &InboundService{}, 1).Settings != before {
		t.Error("the refused create added a vless client")
	}
}

// TestFindUser: by user name, subId or client name; always one user.
func TestFindUser(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	v, err := users.Create(SubUserCreate{Name: "ivan", SubId: "s-ivan", InboundIds: []int{2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ivan", "IVAN", "s-ivan", "ivan-de", " Ivan-DE "} {
		if got, err := users.Find(q); err != nil || got.SubId != v.SubId {
			t.Errorf("Find(%q) = %+v, %v", q, got, err)
		}
	}
	if got, err := users.Find("robot"); err != nil || got.SubId != model.SubUserRobotKey {
		t.Errorf("Find(robot) = %+v, %v", got, err)
	}
	if _, err := users.Find("nobody"); err == nil {
		t.Error("Find(nobody) found someone")
	}
	// A legacy duplicate client name shared by two users is ambiguous.
	usersInbound(t, 8, model.VLESS, "x", model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000099", Email: "other-de", SubID: "s-ivan"})
	if _, err := users.Find("other-de"); err == nil {
		t.Error("Find over a duplicate client name of two users succeeded")
	}
}
