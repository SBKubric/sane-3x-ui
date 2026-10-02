package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// storedVlessInbound puts an xray inbound into the table the way the panel's
// own writes leave it, together with the client_traffics rows of its clients.
func storedVlessInbound(t *testing.T, port int, enable bool, emails ...string) *model.Inbound {
	t.Helper()
	inbound := &model.Inbound{
		UserId: 1, Enable: enable, Port: port, Protocol: model.VLESS,
		Tag: fmt.Sprintf("inbound-%d", port), Remark: "vless",
		Settings:       vlessSettings(t, emails...),
		StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: "{}",
	}
	db := database.GetDB()
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	for _, email := range emails {
		if err := db.Create(&xray.ClientTraffic{InboundId: inbound.Id, Email: email, Enable: true}).Error; err != nil {
			t.Fatalf("create client traffic: %v", err)
		}
	}
	return inbound
}

func vlessSettings(t *testing.T, emails ...string) string {
	t.Helper()
	clients := make([]model.Client, 0, len(emails))
	for index, email := range emails {
		clients = append(clients, model.Client{
			ID: fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", index+1), Email: email, Enable: true,
		})
	}
	settings, err := json.Marshal(map[string]any{"clients": clients, "decryption": "none"})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return string(settings)
}

// withoutXray asserts the precondition the tests below are about: no xray
// process, so the panel's API client cannot connect.
func withoutXray(t *testing.T) {
	t.Helper()
	if currentProcess() != nil {
		t.Fatal("an xray process is registered; these tests need xray stopped")
	}
}

// updateInbound runs UpdateInbound and turns a panic into a test failure, so
// the nil dereference of #250 reports itself instead of killing the suite.
func updateInbound(t *testing.T, edit *model.Inbound) (needRestart bool, err error) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("UpdateInbound panicked: %v", recovered)
		}
	}()
	_, needRestart, err = (&InboundService{}).UpdateInbound(edit)
	return needRestart, err
}

func addInbound(t *testing.T, inbound *model.Inbound) (needRestart bool, err error) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("AddInbound panicked: %v", recovered)
		}
	}()
	_, needRestart, err = (&InboundService{}).AddInbound(inbound)
	return needRestart, err
}

func clientTrafficEmails(t *testing.T, inboundId int) map[string]bool {
	t.Helper()
	var rows []xray.ClientTraffic
	if err := database.GetDB().Where("inbound_id = ?", inboundId).Find(&rows).Error; err != nil {
		t.Fatalf("read client traffics: %v", err)
	}
	emails := make(map[string]bool, len(rows))
	for _, row := range rows {
		emails[row.Email] = true
	}
	return emails
}

// TestAddInbound_XrayStoppedSavesAndAsksForRestart (#102): adding an enabled
// xray inbound while xray is stopped dereferenced the nil API client and
// panicked. The row must be stored and the caller told to restart xray.
func TestAddInbound_XrayStoppedSavesAndAsksForRestart(t *testing.T) {
	newPanel(t)
	withoutXray(t)

	needRestart, err := addInbound(t, &model.Inbound{
		UserId: 1, Enable: true, Port: 34567, Protocol: model.VLESS, Tag: "inbound-34567",
		Remark: "vless", Settings: vlessSettings(t, "alice"),
		StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: "{}",
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if !needRestart {
		t.Error("needRestart = false, want true: the running config could not be updated")
	}
	var stored model.Inbound
	if err := database.GetDB().Where("port = ?", 34567).First(&stored).Error; err != nil {
		t.Fatalf("the inbound was not stored: %v", err)
	}
	if !clientTrafficEmails(t, stored.Id)["alice"] {
		t.Error("alice has no client_traffics row")
	}
}

// TestUpdateInbound_XrayStoppedSavesAndAsksForRestart (#250, 3): editing an
// enabled xray inbound while xray is stopped panicked on the nil API client —
// gin answered 500, and the deferred commit still ran. The edit must be stored
// and the caller told to restart xray.
func TestUpdateInbound_XrayStoppedSavesAndAsksForRestart(t *testing.T) {
	newPanel(t)
	withoutXray(t)
	stored := storedVlessInbound(t, 34567, true, "alice")

	edit := *stored
	edit.Remark = "edited"
	needRestart, err := updateInbound(t, &edit)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if !needRestart {
		t.Error("needRestart = false, want true: the running config could not be updated")
	}
	got, err := (&InboundService{}).GetInbound(stored.Id)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	if got.Remark != "edited" {
		t.Errorf("Remark = %q, want the edit stored", got.Remark)
	}
}

// TestUpdateInbound_PortCollisionDoesNotMoveTheRevision (#250, 1): the ports
// hook ran before the edited row was saved, so it composed the old port list,
// found nothing wrong and moved the chain to a revision whose document then
// failed to build — every hop got 404. It must see the edit, refuse the
// revision and leave the problem for the banner.
func TestUpdateInbound_PortCollisionDoesNotMoveTheRevision(t *testing.T) {
	newPanel(t)
	withoutXray(t)
	stored := storedVlessInbound(t, 34567, true, "alice")
	joinedRegistry(t)
	var setting SettingService
	if err := setting.SetChainExtraPorts([]ChainExtraPort{{Port: 8443, Network: chain.NetworkTCP, Note: "clash"}}); err != nil {
		t.Fatalf("SetChainExtraPorts: %v", err)
	}
	before := chainRevision(t)

	edit := *stored
	edit.Port = 8443
	if _, err := updateInbound(t, &edit); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	if after := chainRevision(t); after != before {
		t.Errorf("chainRevision = %d after an edit put the inbound on the extra port 8443, want it left at %d", after, before)
	}
	problem := (&ChainPortsService{}).LastProblem()
	if problem == nil {
		t.Fatal("LastProblem is nil; the editor has nothing to put in its banner")
	}
	if problem.Code != CodeDuplicatePort {
		t.Errorf("LastProblem code = %q, want %q", problem.Code, CodeDuplicatePort)
	}
}

// TestUpdateInbound_FailedSaveRollsBack (#250, 2): the error of the final save
// never reached the err the deferred commit looks at, so a save that failed
// still committed the client_traffics changes and the revision bump around
// it. The whole edit must roll back.
func TestUpdateInbound_FailedSaveRollsBack(t *testing.T) {
	newPanel(t)
	withoutXray(t)
	// An inbound whose tag names a port it does not listen on — the tag an
	// edit of the second inbound to port 5000 will want for itself.
	db := database.GetDB()
	if err := db.Create(&model.Inbound{
		UserId: 1, Enable: false, Port: 6000, Protocol: model.VLESS, Tag: "inbound-5000",
		Remark: "squatter", Settings: vlessSettings(t), StreamSettings: "{}", Sniffing: "{}",
	}).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	stored := storedVlessInbound(t, 34567, false, "alice")
	joinedRegistry(t)
	before := chainRevision(t)

	edit := *stored
	edit.Port = 5000
	edit.Remark = "edited"
	edit.Settings = vlessSettings(t, "bob")
	if _, err := updateInbound(t, &edit); err == nil {
		t.Fatal("UpdateInbound returned nil, want the UNIQUE failure on the tag")
	}

	got, err := (&InboundService{}).GetInbound(stored.Id)
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	if got.Port != 34567 || got.Remark != "vless" {
		t.Errorf("inbound = port %d remark %q, want it untouched", got.Port, got.Remark)
	}
	emails := clientTrafficEmails(t, stored.Id)
	if !emails["alice"] || emails["bob"] {
		t.Errorf("client_traffics = %v after a failed save, want alice only", emails)
	}
	if after := chainRevision(t); after != before {
		t.Errorf("chainRevision = %d after a failed save, want it left at %d", after, before)
	}
}
