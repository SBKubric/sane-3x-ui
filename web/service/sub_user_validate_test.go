package service

import (
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// validationFixture: petr has a vless client and an AWG peer, a legacy
// duplicate "dup" sits in two inbounds, and monitoring has its probe subId.
func validationFixture(t *testing.T) {
	t.Helper()
	initUsersTestDB(t)
	if err := (&SettingService{}).SetMonProbeSubId("probesub"); err != nil {
		t.Fatal(err)
	}
	usersInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "petr-nl", SubID: "s-petr"},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "dup", SubID: "s-dup1"},
	)
	usersInbound(t, 2, model.VLESS, "de",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "dup", SubID: "s-dup2"},
	)
	awgPeer(t, 1, "petr-awg", "s-petr")
	if err := (&SubUserService{}).Sync(); err != nil {
		t.Fatal(err)
	}
	// The migration named petr after his first client; he is plain petr.
	if err := database.GetDB().Model(&model.SubUser{}).Where("sub_id = ?", "s-petr").Update("name", "petr").Error; err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, what string, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error, want one naming %v", what, parts)
		return
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("%s: error %q does not name %q", what, err, p)
		}
	}
}

// TestValidateClientWrites covers each rule of the shared validation.
func TestValidateClientWrites(t *testing.T) {
	validationFixture(t)
	users := &SubUserService{}

	// A client name is unique across protocols, ignoring case.
	wantErr(t, "xray name taken by AWG", users.ValidateClientWrites(ClientWrite{Name: "Petr-AWG"}), `"Petr-AWG"`, "AmneziaWG", "petr")
	wantErr(t, "AWG name taken by xray", users.ValidateClientWrites(ClientWrite{Name: "petr-nl"}), `"petr-nl"`, "inbound nl", "petr")
	// Within one batch too.
	wantErr(t, "duplicate in batch", users.ValidateClientWrites(ClientWrite{Name: "x1"}, ClientWrite{Name: "X1"}), `"X1"`)
	// Keeping one's own name is not a clash, even for a legacy duplicate.
	if err := users.ValidateClientWrites(ClientWrite{Name: "dup", OldName: "dup"}); err != nil {
		t.Errorf("keeping a legacy duplicate name: %v", err)
	}
	if err := users.ValidateClientWrites(ClientWrite{Name: "fresh", SubId: "s-new"}); err != nil {
		t.Errorf("a fresh name and subId: %v", err)
	}

	// Reserved names.
	wantErr(t, "client named robot", users.ValidateClientWrites(ClientWrite{Name: "Robot"}), "reserved")
	wantErr(t, "client named monitoring", users.ValidateClientWrites(ClientWrite{Name: "monitoring"}), "reserved")

	// A subId belongs to exactly one user.
	wantErr(t, "subId of another user", users.ValidateClientWrites(ClientWrite{Name: "ivan-nl", SubId: "s-petr", User: "ivan"}), `"s-petr"`, "user petr")
	if err := users.ValidateClientWrites(ClientWrite{Name: "petr-de", SubId: "s-petr", User: "petr"}); err != nil {
		t.Errorf("petr's own subId: %v", err)
	}
	// Joining an existing subscription without naming a user is how the
	// panel adds a protocol: allowed.
	if err := users.ValidateClientWrites(ClientWrite{Name: "petr-de", SubId: "s-petr"}); err != nil {
		t.Errorf("joining petr's subscription: %v", err)
	}
	wantErr(t, "probe subId", users.ValidateClientWrites(ClientWrite{Name: "sneaky", SubId: "probesub"}), "monitoring")
	wantErr(t, "technical key as subId", users.ValidateClientWrites(ClientWrite{Name: "sneaky", SubId: "@robot"}), "reserved")
	// The captcha page is <subPath>captcha (#220): a subscription there
	// would never be served.
	for _, subId := range []string{"captcha", "Captcha"} {
		wantErr(t, "subId "+subId, users.ValidateClientWrites(ClientWrite{Name: "sneaky", SubId: subId}), "reserved", "captcha")
	}
}

// TestInboundServiceEnforcesUserRules: the panel's add and update paths go
// through the same validation.
func TestInboundServiceEnforcesUserRules(t *testing.T) {
	validationFixture(t)
	s := &InboundService{}

	clash := model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000010", Email: "petr-awg", SubID: "s-x"}
	_, err := s.AddInboundClient(clientsPayload(2, clash))
	wantErr(t, "AddInboundClient with an AWG name", err, `"petr-awg"`, "petr")

	reserved := model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000011", Email: "robot"}
	_, err = s.AddInboundClient(clientsPayload(2, reserved))
	wantErr(t, "AddInboundClient named robot", err, "reserved")

	techKey := model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000012", Email: "ok", SubID: "@monitoring"}
	_, err = s.AddInboundClient(clientsPayload(2, techKey))
	wantErr(t, "AddInboundClient with a technical key", err, "reserved")

	if n := len(mustClients(t, s, 2)); n != 1 {
		t.Fatalf("refused adds changed inbound 2: %d clients", n)
	}

	// A new subId is fine and gets its user.
	fresh := model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000013", Email: "olga-de", SubID: "s-olga"}
	if _, err := s.AddInboundClient(clientsPayload(2, fresh)); err != nil {
		t.Fatalf("AddInboundClient(olga): %v", err)
	}
	if u, err := (&SubUserService{}).Get("s-olga"); err != nil || u.Name != "olga-de" {
		t.Errorf("user of the new subId: %+v, %v", u, err)
	}

	// Update: renaming into a taken name is refused, keeping a legacy
	// duplicate is not.
	renamed := fresh
	renamed.Email = "PETR-AWG"
	_, err = s.UpdateInboundClient(clientsPayload(2, renamed), fresh.ID)
	wantErr(t, "UpdateInboundClient into an AWG name", err, `"PETR-AWG"`, "AmneziaWG client of user petr")

	dup := model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "dup", SubID: "s-dup2", Comment: "edited"}
	if _, err := s.UpdateInboundClient(clientsPayload(2, dup), dup.ID); err != nil {
		t.Errorf("editing a legacy duplicate without renaming it: %v", err)
	}
}
