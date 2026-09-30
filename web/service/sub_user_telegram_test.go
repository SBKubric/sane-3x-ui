package service

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// tgIdOfTunnel reads a tunnel client's tgId off its row.
func tgIdOfTunnel(t *testing.T, uuid string) int64 {
	t.Helper()
	var c model.TunnelClient
	if err := database.GetDB().First(&c, "uuid = ?", uuid).Error; err != nil {
		t.Fatal(err)
	}
	return c.TgId
}

// xrayTgIds maps the email of every xray client of the inbound to its tgId.
func xrayTgIds(t *testing.T, inboundId int) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, c := range mustClients(t, &InboundService{}, inboundId) {
		out[c.Email] = c.TgID
	}
	return out
}

func mustGetUser(t *testing.T, key string) *SubUserView {
	t.Helper()
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		t.Fatalf("Get(%s): %v", key, err)
	}
	return v
}

// telegramMigrationFixture is the data the identity migration meets:
//   - agreed: every client with a tgId says 101 (one has none) and no one
//     else has it — the user takes 101;
//   - split: its clients say 201 and 202 — a conflict of the first kind;
//   - twin-a and twin-b: each agrees on 301 on its own, but the id is on
//     both — a conflict of the second kind;
//   - holder owns 401 already, and taken's clients say 401 — also a
//     conflict of the second kind;
//   - robot's client carries 501 and so does lone's: a technical user holds
//     nothing, lone takes 501.
func telegramMigrationFixture(t *testing.T) {
	t.Helper()
	initUsersTestDB(t)
	if err := database.GetDB().Create(&model.SubUser{SubId: "s-holder", Name: "holder", TgId: 401}).Error; err != nil {
		t.Fatal(err)
	}
	usersInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "agreed-nl", SubID: "s-agreed", TgID: 101},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "split-nl", SubID: "s-split", TgID: 201},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "twin-a", SubID: "s-twin-a", TgID: 301},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000004", Email: "twin-b", SubID: "s-twin-b", TgID: 301},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000005", Email: "taken-nl", SubID: "s-taken", TgID: 401},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000006", Email: "robots", TgID: 501},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000007", Email: "lone-nl", SubID: "s-lone", TgID: 501},
	)
	usersInbound(t, 2, model.Trojan, "de",
		model.Client{Password: "p1", Email: "agreed-de", SubID: "s-agreed"},
		model.Client{Password: "p2", Email: "split-de", SubID: "s-split", TgID: 202},
	)
	usersInbound(t, 5, model.AmneziaWG, "awg")
	agreed := awgPeer(t, 1, "agreed-awg", "s-agreed")
	if err := database.GetDB().Model(agreed).Update("tg_id", 101).Error; err != nil {
		t.Fatal(err)
	}
}

// TestTelegramMigrationMovesAnAgreedTgId (#186 point 3): a user whose
// clients agree on one tgId that nobody else has gets it; no conflict. The
// clients are not written.
func TestTelegramMigrationMovesAnAgreedTgId(t *testing.T) {
	telegramMigrationFixture(t)
	before := mustInbound(t, &InboundService{}, 2).Settings

	if err := (&SubUserService{}).Sync(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int64{"s-agreed": 101, "s-lone": 501} {
		v := mustGetUser(t, key)
		if v.TgId != want || v.TgConflict {
			t.Errorf("%s: tgId %d conflict %v, want %d and no conflict", v.Name, v.TgId, v.TgConflict, want)
		}
	}
	if after := mustInbound(t, &InboundService{}, 2).Settings; after != before {
		t.Error("the migration wrote the clients")
	}
}

// TestTelegramMigrationMarksConflicts: clients that disagree, and a tgId
// another user has — on its row or on its clients — leave the user without
// a tgId and marked; the clients keep theirs. A second run changes nothing.
func TestTelegramMigrationMarksConflicts(t *testing.T) {
	telegramMigrationFixture(t)
	users := &SubUserService{}
	for run := 1; run <= 2; run++ {
		if err := users.Sync(); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"s-split", "s-twin-a", "s-twin-b", "s-taken"} {
			if v := mustGetUser(t, key); v.TgId != 0 || !v.TgConflict {
				t.Errorf("run %d, %s: tgId %d conflict %v, want 0 and a conflict", run, v.Name, v.TgId, v.TgConflict)
			}
		}
		if v := mustGetUser(t, "s-holder"); v.TgId != 401 || v.TgConflict {
			t.Errorf("run %d, holder: %+v", run, v.SubUser)
		}
		nl := xrayTgIds(t, 1)
		for email, want := range map[string]int64{"split-nl": 201, "twin-a": 301, "twin-b": 301, "taken-nl": 401, "robots": 501} {
			if nl[email] != want {
				t.Errorf("run %d, client %s: tgId %d, want %d", run, email, nl[email], want)
			}
		}
		if de := xrayTgIds(t, 2); de["split-de"] != 202 {
			t.Errorf("run %d, split-de: tgId %d", run, de["split-de"])
		}
	}
}

// TestTelegramMigrationLeavesAUserWithATgIdAlone: a user that has a tgId
// keeps it, whatever its clients say; the clients that differ make a
// conflict.
func TestTelegramMigrationLeavesAUserWithATgIdAlone(t *testing.T) {
	telegramMigrationFixture(t)
	if err := database.GetDB().Create(&model.SubUser{SubId: "s-split", Name: "split", TgId: 999}).Error; err != nil {
		t.Fatal(err)
	}
	if v := mustGetUser(t, "s-split"); v.TgId != 999 || !v.TgConflict {
		t.Errorf("split: tgId %d conflict %v", v.TgId, v.TgConflict)
	}
}

// TestTelegramIdIsUniqueInTheSchema: a database where two users share a
// tgId (older than the rule) comes up with neither holding it — they are
// conflicts to resolve by hand — and from then on the schema refuses a
// second user with a tgId other than 0.
func TestTelegramIdIsUniqueInTheSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x-ui.db")
	old, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.AutoMigrate(&model.SubUser{}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []model.SubUser{{SubId: "s1", Name: "ivan", TgId: 42}, {SubId: "s2", Name: "petr", TgId: 42}, {SubId: "s3", Name: "olga", TgId: 43}} {
		if err := old.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	if db, err := old.DB(); err == nil {
		db.Close()
	}
	if err := database.InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })

	got := usersByKey(t)
	if got["s1"].TgId != 0 || got["s2"].TgId != 0 || got["s3"].TgId != 43 {
		t.Errorf("after the migration: %+v", got)
	}
	db := database.GetDB()
	if err := db.Create(&model.SubUser{SubId: "s4", Name: "anna", TgId: 43}).Error; err == nil {
		t.Error("a second user with tgId 43 was stored")
	}
	for _, u := range []model.SubUser{{SubId: "s5", Name: "a"}, {SubId: "s6", Name: "b"}} {
		if err := db.Create(&u).Error; err != nil {
			t.Errorf("a user without a tgId: %v", err)
		}
	}
}

// TestTelegramDetailsNameTheOtherOwners: the user's Telegram view lists the
// tgIds of its clients, which clients carry each, who else has it — the user
// that owns it, the users whose clients carry it — and whether it can be
// assigned.
func TestTelegramDetailsNameTheOtherOwners(t *testing.T) {
	telegramMigrationFixture(t)
	users := &SubUserService{}

	split, err := users.Telegram("s-split")
	if err != nil {
		t.Fatal(err)
	}
	if !split.Conflict || split.TgId != 0 || len(split.Candidates) != 2 {
		t.Fatalf("split: %+v", split)
	}
	for i, want := range []SubUserTgCandidate{
		{TgId: 201, Clients: []string{"split-nl"}, Assignable: true},
		{TgId: 202, Clients: []string{"split-de"}, Assignable: true},
	} {
		c := split.Candidates[i]
		if c.TgId != want.TgId || !slices.Equal(c.Clients, want.Clients) || c.Assignable != want.Assignable || c.Owner != "" || len(c.AlsoOn) != 0 {
			t.Errorf("split candidate %d: %+v", i, c)
		}
	}

	twin, err := users.Telegram("s-twin-a")
	if err != nil {
		t.Fatal(err)
	}
	if c := twin.Candidates[0]; c.TgId != 301 || !c.Assignable || !slices.Equal(c.AlsoOn, []string{"twin-b"}) {
		t.Errorf("twin-a candidate: %+v", c)
	}
	taken, err := users.Telegram("s-taken")
	if err != nil {
		t.Fatal(err)
	}
	if c := taken.Candidates[0]; c.TgId != 401 || c.Assignable || c.Owner != "holder" || c.OwnerSubId != "s-holder" {
		t.Errorf("taken candidate: %+v", c)
	}
}

// TestAssignTelegramResolvesAConflict (#186 point 8): «Назначить <tg_id>»
// gives the user one of its clients' ids and writes it onto all of its
// clients, xray and tunnel; an id another user owns is refused and nothing
// changes. The other user whose clients carry the id stays in conflict.
func TestAssignTelegramResolvesAConflict(t *testing.T) {
	telegramMigrationFixture(t)
	users := &SubUserService{}

	v, err := users.SetTelegram("s-split", 202)
	if err != nil {
		t.Fatalf("SetTelegram: %v", err)
	}
	if v.TgId != 202 || v.TgConflict {
		t.Errorf("split after the assign: tgId %d conflict %v", v.TgId, v.TgConflict)
	}
	if nl, de := xrayTgIds(t, 1), xrayTgIds(t, 2); nl["split-nl"] != 202 || de["split-de"] != 202 {
		t.Errorf("clients: split-nl %d, split-de %d", nl["split-nl"], de["split-de"])
	}

	before := mustInbound(t, &InboundService{}, 1).Settings
	if _, err := users.SetTelegram("s-taken", 401); err == nil {
		t.Error("401 of holder was assigned to taken")
	}
	if _, err := users.SetTelegram("s-twin-a", 202); err == nil {
		t.Error("202 of split was assigned to twin-a")
	}
	if after := mustInbound(t, &InboundService{}, 1).Settings; after != before {
		t.Error("a refused assign wrote the clients")
	}

	if _, err := users.SetTelegram("s-twin-a", 301); err != nil {
		t.Fatalf("twin-a takes 301: %v", err)
	}
	if b := mustGetUser(t, "s-twin-b"); b.TgId != 0 || !b.TgConflict {
		t.Errorf("twin-b: tgId %d conflict %v", b.TgId, b.TgConflict)
	}
	if _, err := users.SetTelegram("s-twin-b", 301); err == nil {
		t.Error("301 went to twin-b too")
	}
	if _, err := users.SetTelegram(model.SubUserRobotKey, 777); err == nil {
		t.Error("robot got a Telegram id")
	}
	if _, err := users.SetTelegram("s-split", -5); err == nil {
		t.Error("a negative id was assigned")
	}
}

// TestUnlinkTelegram (#186 point 9): «Отвязать Telegram» leaves the user and
// every one of its clients without a tgId; the Telegram account stays.
func TestUnlinkTelegram(t *testing.T) {
	telegramMigrationFixture(t)
	if _, err := writeTgAccount(model.TgAccount{TgId: 101, Username: "agreed_tg"}); err != nil {
		t.Fatal(err)
	}
	users := &SubUserService{}
	if v := mustGetUser(t, "s-agreed"); v.TgId != 101 {
		t.Fatalf("agreed: tgId %d", v.TgId)
	}
	v, err := users.UnlinkTelegram("s-agreed")
	if err != nil {
		t.Fatalf("UnlinkTelegram: %v", err)
	}
	if v.TgId != 0 || v.TgConflict {
		t.Errorf("after unlink: tgId %d conflict %v", v.TgId, v.TgConflict)
	}
	for _, c := range v.Clients {
		if c.TgId != 0 {
			t.Errorf("client %s kept tgId %d", c.Name, c.TgId)
		}
	}
	if nl := xrayTgIds(t, 1); nl["agreed-nl"] != 0 {
		t.Errorf("agreed-nl stored tgId %d", nl["agreed-nl"])
	}
	if a, err := (&TgAccountService{}).Get(101); err != nil || a == nil || a.Username != "agreed_tg" {
		t.Errorf("the account after unlink: %+v, %v", a, err)
	}
	// The unlinked clients do not bring the id back.
	if err := users.Sync(); err != nil {
		t.Fatal(err)
	}
	if v := mustGetUser(t, "s-agreed"); v.TgId != 0 {
		t.Errorf("after Sync: tgId %d", v.TgId)
	}

	// A conflict is unlinked the same way: every client loses its id.
	if _, err := users.UnlinkTelegram("s-split"); err != nil {
		t.Fatal(err)
	}
	if nl, de := xrayTgIds(t, 1), xrayTgIds(t, 2); nl["split-nl"] != 0 || de["split-de"] != 0 {
		t.Errorf("split clients: %d, %d", nl["split-nl"], de["split-de"])
	}
	if _, err := users.UnlinkTelegram(model.SubUserMonitoringKey); err == nil {
		t.Error("monitoring was unlinked")
	}
}

// TestSavingAUserWritesItsTgIdOntoItsClients (#186 point 2): create (a
// linked AWG client included), ➕ protocol, assign from robot and the
// switch write the user's tgId onto every client of the user, those that
// had another one or none included.
func TestSavingAUserWritesItsTgIdOntoItsClients(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	db := database.GetDB()

	// Create, taking over an unlinked AWG client that has no tgId.
	legacy := awgPeer(t, 30, "ivan-awg", "")
	if _, err := users.Create(SubUserCreate{Name: "ivan", TgId: 42, InboundIds: []int{1, 5}}); err == nil {
		t.Fatal("create without linkExisting took the AWG client over")
	}
	v, err := users.Create(SubUserCreate{Name: "ivan", TgId: 42, InboundIds: []int{1, 5}, LinkExisting: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := tgIdOfTunnel(t, legacy.UUID); got != 42 {
		t.Errorf("linked AWG client: tgId %d", got)
	}

	// A client that drifted (edited in its inbound) is written back by ➕.
	if err := db.Model(&model.TunnelClient{}).Where("uuid = ?", legacy.UUID).Update("tg_id", 99).Error; err != nil {
		t.Fatal(err)
	}
	if v = mustGetUser(t, v.SubId); !v.TgConflict {
		t.Error("a drifted client shows no conflict")
	}
	v, err = users.AddProtocol(v.SubId, 2, false)
	if err != nil {
		t.Fatalf("AddProtocol: %v", err)
	}
	for _, c := range v.Clients {
		if c.TgId != 42 {
			t.Errorf("after ➕, client %s: tgId %d", c.Name, c.TgId)
		}
	}

	// Assign from robot: an xray client and an AWG client, both with another id.
	usersInbound(t, 3, model.VMESS, "vm",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000031", Email: "stray-vm", TgID: 5, Enable: true})
	stray := awgPeer(t, 31, "stray-awg", "")
	for _, name := range []string{"stray-vm", "stray-awg"} {
		if _, err := users.Assign(v.SubId, name); err != nil {
			t.Fatalf("Assign(%s): %v", name, err)
		}
	}
	if got := xrayTgIds(t, 3)["stray-vm"]; got != 42 {
		t.Errorf("assigned xray client: tgId %d", got)
	}
	if got := tgIdOfTunnel(t, stray.UUID); got != 42 {
		t.Errorf("assigned AWG client: tgId %d", got)
	}

	// The switch is a save too.
	if err := db.Model(&model.TunnelClient{}).Where("uuid = ?", stray.UUID).Update("tg_id", 7).Error; err != nil {
		t.Fatal(err)
	}
	if v, err = users.SetEnable(v.SubId, false); err != nil {
		t.Fatal(err)
	}
	if got := tgIdOfTunnel(t, stray.UUID); got != 42 || v.TgConflict {
		t.Errorf("after the switch: AWG tgId %d, conflict %v", got, v.TgConflict)
	}
}

// TestAddProtocolTakesTheUsersTgId: the new client gets the user's tgId,
// not whatever its first client carries.
func TestAddProtocolTakesTheUsersTgId(t *testing.T) {
	telegramMigrationFixture(t)
	users := &SubUserService{}
	// split has no tgId (a conflict): the new client gets none, and the
	// conflicting clients keep theirs.
	v, err := users.AddProtocol("s-split", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	c := clientByName(t, v, "split-nl-awg")
	if c.TgId != 0 {
		t.Errorf("new client of a user without a tgId: %d", c.TgId)
	}
	if nl := xrayTgIds(t, 1); nl["split-nl"] != 201 || !v.TgConflict {
		t.Errorf("split-nl: %d, conflict %v", nl["split-nl"], v.TgConflict)
	}
}
