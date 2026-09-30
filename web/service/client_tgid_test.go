package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// Clients of a Telegram account (#201): the lookup reads the clients out of
// the parsed settings JSON, not their text, so the way a serializer, an
// import or a hand edit wrote "tgId" does not matter.

// rawInbound stores an inbound whose settings are exactly settings, with a
// traffic row for every email.
func rawInbound(t *testing.T, id int, protocol model.Protocol, settings string, emails ...string) {
	t.Helper()
	db := database.GetDB()
	ib := &model.Inbound{Id: id, Port: 21000 + id, Protocol: protocol, Tag: fmt.Sprintf("inbound-raw-%d", id),
		Remark: fmt.Sprintf("raw-%d", id), Settings: settings, StreamSettings: "{}", Sniffing: "{}"}
	if err := db.Create(ib).Error; err != nil {
		t.Fatal(err)
	}
	for _, email := range emails {
		if err := db.Create(&xray.ClientTraffic{InboundId: id, Email: email, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// tunnelPeerOf stores a tunnel client of the server of kind with that tgId.
func tunnelPeerOf(t *testing.T, kind string, n int, email string, tgId int64) {
	t.Helper()
	db := database.GetDB()
	server := model.TunnelServer{Kind: kind}
	if err := db.Where("kind = ?", kind).FirstOrCreate(&server).Error; err != nil {
		t.Fatal(err)
	}
	c := &model.TunnelClient{ServerId: server.Id, UUID: uuidN(n), Name: email, Email: email, Enable: true,
		TgId: tgId, Upload: 1 << 20, TotalGB: 10 << 30}
	if err := db.Create(c).Error; err != nil {
		t.Fatal(err)
	}
}

// tgClientsFixture: the Telegram account 777 has clients in inbounds written
// every way — spaced and compact JSON, tgId as a number and as a string — and
// an AWG and a WG client; the others belong to someone else or no one.
func tgClientsFixture(t *testing.T) {
	t.Helper()
	initUsersTestDB(t)
	rawInbound(t, 1, model.VLESS, `{
  "clients": [
    {"id": "aaaaaaaa-0000-0000-0000-000000000001", "email": "ivan-spaced", "tgId": 777, "enable": true},
    {"id": "aaaaaaaa-0000-0000-0000-000000000002", "email": "neighbour", "tgId": 7771, "enable": true}
  ],
  "decryption": "none"
}`, "ivan-spaced", "neighbour")
	rawInbound(t, 2, model.Trojan,
		`{"clients":[{"password":"p1","email":"ivan-compact","tgId":777,"enable":true},{"password":"p2","email":"nobody","tgId":0,"enable":true}]}`,
		"ivan-compact", "nobody")
	rawInbound(t, 3, model.VMESS,
		`{"clients":[{"id":"aaaaaaaa-0000-0000-0000-000000000003","email":"ivan-string","tgId":"777","enable":true},{"id":"aaaaaaaa-0000-0000-0000-000000000004","email":"blank","tgId":"","enable":true}]}`,
		"ivan-string", "blank")
	rawInbound(t, 4, model.Shadowsocks, `{"method": "aes-256-gcm", "clients": "not a list"}`)
	rawInbound(t, 11, model.HTTP, `not json at all`)
	tunnelPeerOf(t, model.TunnelKindAwg, 1, "ivan-awg", 777)
	tunnelPeerOf(t, model.TunnelKindWg, 2, "ivan-wg", 777)
	tunnelPeerOf(t, model.TunnelKindAwg, 3, "someone-awg", 778)
}

func trafficEmails(traffics []*xray.ClientTraffic) []string {
	var out []string
	for _, tr := range traffics {
		out = append(out, tr.Email)
	}
	slices.Sort(out)
	return out
}

// TestGetClientTrafficTgBot_MatchesParsedTgId: /usage and the exhausted
// notice find the clients of the account however their settings were written,
// the AWG/WG ones included; a prefix of another id does not match.
func TestGetClientTrafficTgBot_MatchesParsedTgId(t *testing.T) {
	tgClientsFixture(t)

	traffics, err := (&InboundService{}).GetClientTrafficTgBot(777)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ivan-awg", "ivan-compact", "ivan-spaced", "ivan-string", "ivan-wg"}
	if got := trafficEmails(traffics); !slices.Equal(got, want) {
		t.Fatalf("clients of 777: %q, want %q", got, want)
	}
	for _, tr := range traffics {
		if tr.Email == "ivan-awg" && (tr.UUID != uuidN(1) || tr.Up+tr.Down != 1<<20 || tr.Total != 10<<30 || !tr.Enable) {
			t.Errorf("the AWG client's traffic: %+v", tr)
		}
		if tr.Email == "ivan-compact" && !tr.Enable {
			t.Errorf("the xray client's traffic: %+v", tr)
		}
	}
}

// TestGetClientTrafficTgBot_NoMatch: an account with no clients, and the
// empty id that blank clients carry, find nothing.
func TestGetClientTrafficTgBot_NoMatch(t *testing.T) {
	tgClientsFixture(t)
	for _, tgId := range []int64{555, 0} {
		traffics, err := (&InboundService{}).GetClientTrafficTgBot(tgId)
		if err != nil {
			t.Fatal(err)
		}
		if len(traffics) != 0 {
			t.Errorf("clients of %d: %q, want none", tgId, trafficEmails(traffics))
		}
	}
}

// TestTelegramSubUsers_StringTgId: a user whose client carries the account's
// id as a string ("777", as the panel's form sometimes stores it) is still
// the account's; so is one whose only client is a tunnel client.
func TestTelegramSubUsers_StringTgId(t *testing.T) {
	usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 2}})
	legacyUserOf(t, SubUserCreate{Name: "petr", InboundIds: []int{2}}, 778)
	mustCreateUser(t, SubUserCreate{Name: "olga", TgId: 779, InboundIds: []int{5}})
	for _, c := range ivan.Clients {
		setClientTgId(t, c.InboundId, c.Name, "777")
	}

	for tgId, want := range map[int64]string{777: "ivan", 778: "petr", 779: "olga", 555: ""} {
		users, err := telegramSubUsers(tgId)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, v := range users {
			got += v.Name
		}
		if got != want {
			t.Errorf("users of %d: %q, want %q", tgId, got, want)
		}
	}
}

// setClientTgId writes tgId onto the client email of the inbound behind the
// service's back, as compact JSON.
func setClientTgId(t *testing.T, inboundId int, email string, tgId any) {
	t.Helper()
	db := database.GetDB()
	var ib model.Inbound
	if err := db.First(&ib, inboundId).Error; err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	for _, raw := range settings["clients"].([]any) {
		if client := raw.(map[string]any); client["email"] == email {
			client["tgId"] = tgId
		}
	}
	out, _ := json.Marshal(settings)
	if err := db.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("settings", string(out)).Error; err != nil {
		t.Fatal(err)
	}
}
