package service

import (
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestCreateUserKeepsTheContactEmail: the user's contact email (#193) is
// stored on the user, trimmed, and is not the xray email of its clients.
func TestCreateUserKeepsTheContactEmail(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	v, err := users.Create(SubUserCreate{Name: "ivan", ContactEmail: "  Ivan@Example.org ", InboundIds: []int{2}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, _ := users.Get(v.SubId); got.ContactEmail != "Ivan@Example.org" {
		t.Errorf("contact email = %q", got.ContactEmail)
	}
	if c := clientByName(t, v, "ivan-de"); c.Name != "ivan-de" {
		t.Errorf("the xray email is still generated: %+v", c)
	}
	if p, err := users.Create(SubUserCreate{Name: "petr"}); err != nil || p.ContactEmail != "" {
		t.Errorf("without a contact email: %+v, %v", p, err)
	}
}

// TestContactEmailFormat: a contact email is a bare address; anything else
// is refused before the user is made.
func TestContactEmailFormat(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	for _, bad := range []string{"ivan", "ivan@", "@example.org", "Ivan <ivan@example.org>", "ivan@example.org, petr@example.org",
		"iv an@example.org", strings.Repeat("a", 250) + "@example.org"} {
		if _, err := CheckContactEmail(bad); err == nil {
			t.Errorf("CheckContactEmail(%q) passed", bad)
		}
		if _, err := users.Create(SubUserCreate{Name: "ivan", ContactEmail: bad}); err == nil {
			t.Errorf("Create with contact email %q succeeded", bad)
		}
	}
	if _, err := users.Find("ivan"); err == nil {
		t.Error("a refused create left the user")
	}
	for in, want := range map[string]string{"ivan@example.org": "ivan@example.org", " i.v+tag@mail.example.co ": "i.v+tag@mail.example.co", "": ""} {
		if got, err := CheckContactEmail(in); err != nil || got != want {
			t.Errorf("CheckContactEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// TestCreateUserTelegramIsOneToOne: a Telegram id is one user's (map #178):
// a create with the id of another user, or of another user's client, is
// refused, and nothing is made.
func TestCreateUserTelegramIsOneToOne(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 42, InboundIds: []int{2}})
	if err := users.CheckNewTgId(42); err == nil || !strings.Contains(err.Error(), "ivan") {
		t.Errorf("CheckNewTgId(42) = %v, want a refusal naming ivan", err)
	}
	if _, err := users.Create(SubUserCreate{Name: "petr", TgId: 42, InboundIds: []int{1}}); err == nil {
		t.Error("Create with ivan's Telegram id succeeded")
	}
	if _, err := users.Find("petr"); err == nil {
		t.Error("the refused user was made")
	}

	// A client of another user carrying the id counts as well.
	usersInbound(t, 3, model.VMESS, "vm", model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "legacy", SubID: "s-legacy", TgID: 77, Enable: true})
	if err := users.CheckNewTgId(77); err == nil {
		t.Error("CheckNewTgId(77) passed: a client of user legacy has it")
	}
	for _, free := range []int64{0, 43} {
		if err := users.CheckNewTgId(free); err != nil {
			t.Errorf("CheckNewTgId(%d): %v", free, err)
		}
	}
	if _, err := users.Create(SubUserCreate{Name: "petr", TgId: 43, InboundIds: []int{1}}); err != nil {
		t.Errorf("Create with a free Telegram id: %v", err)
	}
}

// TestCheckNewName: the name step's check is Create's — empty, reserved and
// taken names are refused, ignoring case.
func TestCheckNewName(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	mustCreateUser(t, SubUserCreate{Name: "ivan"})
	for _, name := range []string{"", "  ", "IVAN", "robot", "Monitoring", "probe-x", strings.Repeat("n", 65)} {
		if err := users.CheckNewName(name); err == nil {
			t.Errorf("CheckNewName(%q) passed", name)
		}
	}
	if err := users.CheckNewName(" petr "); err != nil {
		t.Errorf("CheckNewName(petr): %v", err)
	}
}
