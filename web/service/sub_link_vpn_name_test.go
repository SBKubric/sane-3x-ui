package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The link broadcast's detector and the VPN name (#225): setting, changing
// and clearing it is a change of its own; with it, a switch of the active
// edge leaves the VLESS links as they are and is no change for a person
// with xray clients only, but still one for a person with an AWG .conf.

const linkAwgPerson = int64(506) // maria, with an AWG client

// vpnLinkFixture is linkFixture with edge a active and edge b standby, and
// maria (Telegram 506) on trojan "de" and the AWG row; the links are known.
func vpnLinkFixture(t *testing.T) (*Tgbot, *SubUserView, *SubUserView, *SubUserView) {
	t.Helper()
	tg, ivan, anna := linkFixture(t)
	maria := mustCreateUser(t, SubUserCreate{Name: "maria", TgId: linkAwgPerson, InboundIds: []int{2, 5}})
	a := &model.ChainHop{Name: "edge-a", Host: "192.0.2.10", Role: model.ChainRoleEdge,
		State: model.ChainStateJoined, SubPort: 2096, SubScheme: "http"}
	if err := database.GetDB().Create(a).Error; err != nil {
		t.Fatal(err)
	}
	switchEdge(t, "edge-a")
	settleLinks(t, tg)
	return tg, ivan, anna, maria
}

func switchEdge(t *testing.T, name string) {
	t.Helper()
	var hop model.ChainHop
	if err := database.GetDB().Where("name = ?", name).First(&hop).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&ChainService{}).SetActive(hop.Id); err != nil {
		t.Fatal(err)
	}
}

// settleLinks answers whatever changed, as an admin's «No» does.
func settleLinks(t *testing.T, tg *Tgbot) {
	t.Helper()
	if err := subLinkAcknowledge(mustScan(t, tg).users); err != nil {
		t.Fatal(err)
	}
	if changes := mustScan(t, tg); len(changes.users) != 0 {
		t.Fatalf("still changed after the answer: %+v", changes.users)
	}
}

// changed is who changed, with their reasons: "ivan:vpnName maria:edge".
func changed(changes subLinkChanges) string {
	var out []string
	for _, c := range changes.users {
		out = append(out, c.Name+":"+strings.Join(c.Reasons, ","))
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// TestSubLinkVPNNameIsAChange: setting the VPN name, changing it and
// clearing it each change everyone's VLESS links — reason vpnName, the
// subscription link itself the same — and leave the users without Telegram
// for manual sending.
func TestSubLinkVPNNameIsAChange(t *testing.T) {
	tg, ivan, anna, _ := vpnLinkFixture(t)
	for _, name := range []string{"vpn.example.com", "vpn2.example.com", ""} {
		setSetting(t, "vpnName", name)
		changes := mustScan(t, tg)
		if got := changed(changes); got != "ivan:vpnName maria:vpnName" {
			t.Fatalf("VPN name %q: %q", name, got)
		}
		for _, c := range changes.users {
			if c.TgId == ivan.TgId && (c.OldURL != c.NewURL || c.VPNName != name) {
				t.Errorf("VPN name %q: %+v", name, c)
			}
		}
		if !slices.ContainsFunc(changes.noTelegram, func(v *SubUserView) bool { return v.SubId == anna.SubId }) {
			t.Errorf("VPN name %q: anna is not left for manual sending: %+v", name, changes.noTelegram)
		}
		if got := changes.reasons(); strings.Join(got, ",") != model.SubLinkReasonVPNName {
			t.Errorf("reasons %q", got)
		}
		settleLinks(t, tg)
	}
}

// TestSubLinkVPNNameWithoutOverride: with the override off the links name
// the real server, VPN name or not: no change.
func TestSubLinkVPNNameWithoutOverride(t *testing.T) {
	tg, _, _, _ := vpnLinkFixture(t)
	if err := (&ChainService{}).ClearActive(); err != nil {
		t.Fatal(err)
	}
	settleLinks(t, tg)
	setSetting(t, "vpnName", "vpn.example.com")
	if got := changed(mustScan(t, tg)); got != "" {
		t.Errorf("changes: %q", got)
	}
}

// TestSubLinkEdgeSwitchWithVPNName: with the VPN name and the public
// subscription address set, a switch of the active edge changes neither the
// link nor the VLESS links — ivan is not asked about — while maria's .conf
// names the new edge: reason edge, for her alone.
func TestSubLinkEdgeSwitchWithVPNName(t *testing.T) {
	tg, _, _, _ := vpnLinkFixture(t)
	setSetting(t, "vpnName", "vpn.example.com")
	setSetting(t, "subPublicURL", "https://sub.example.com")
	settleLinks(t, tg)

	switchEdge(t, "edge-b")
	changes := mustScan(t, tg)
	if got := changed(changes); got != "maria:edge" {
		t.Fatalf("changes: %q", got)
	}
	if c := changes.users[0]; c.OldURL != c.NewURL || c.EdgeHost != "b.example.net" || len(changes.noTelegram) != 0 {
		t.Errorf("change: %+v, without Telegram %d", c, len(changes.noTelegram))
	}
	settleLinks(t, tg)
	// ivan, never asked, knows the new edge too: a later AWG client of his
	// is no change of the edge.
	var row model.SubLinkKnown
	if err := database.GetDB().First(&row, "tg_id = ?", linkPerson).Error; err != nil || row.EdgeHost == nil ||
		*row.EdgeHost != "b.example.net" {
		t.Errorf("ivan's row: %+v %v", row, err)
	}
}

// TestSubLinkEdgeSwitchWithVPNNameNoPublicAddress: without the public
// address the subscription link itself names the edge, so a switch changes
// it for everyone, VPN name or not.
func TestSubLinkEdgeSwitchWithVPNNameNoPublicAddress(t *testing.T) {
	tg, _, _, _ := vpnLinkFixture(t)
	setSetting(t, "vpnName", "vpn.example.com")
	settleLinks(t, tg)
	switchEdge(t, "edge-b")
	if got := changed(mustScan(t, tg)); got != "ivan:edge maria:edge" {
		t.Errorf("changes: %q", got)
	}
}

// TestSubLinkKnownRowsFromBeforeTheVPNName: a row written before #225 has no
// host override; the detector fills it in silently and asks nothing.
func TestSubLinkKnownRowsFromBeforeTheVPNName(t *testing.T) {
	tg, _, _, _ := vpnLinkFixture(t)
	if err := database.GetDB().Model(&model.SubLinkKnown{}).Where("1 = 1").Update("edge_host", nil).Error; err != nil {
		t.Fatal(err)
	}
	if got := changed(mustScan(t, tg)); got != "" {
		t.Fatalf("changes: %q", got)
	}
	var row model.SubLinkKnown
	if err := database.GetDB().First(&row, "tg_id = ?", linkAwgPerson).Error; err != nil || row.EdgeHost == nil ||
		*row.EdgeHost != "192.0.2.10" {
		t.Errorf("maria's row: %+v %v", row, err)
	}
}
