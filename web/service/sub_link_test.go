package service

import (
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The detectors of the link broadcast (#222): the link a person was last
// known to have against the one the panel hands out now.

const (
	linkPerson  = int64(501) // ivan's Telegram
	linkPerson2 = int64(502) // maria's
)

// linkFixture is the users fixture with ivan (Telegram 501) and anna (no
// Telegram) on trojan "de", and an edge that has entered the chain but is
// not active; the links as the panel hands them out now are known.
func linkFixture(t *testing.T) (*Tgbot, *SubUserView, *SubUserView) {
	t.Helper()
	tg := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: linkPerson, InboundIds: []int{2}})
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", InboundIds: []int{2}})
	edge := &model.ChainHop{Name: "edge-b", Host: "b.example.net", Role: model.ChainRoleEdge,
		State: model.ChainStateJoined, SubPort: 2096, SubScheme: "http"}
	if err := database.GetDB().Create(edge).Error; err != nil {
		t.Fatal(err)
	}
	if changes := mustScan(t, tg); len(changes.users) != 0 {
		t.Fatalf("the first look found changes: %+v", changes)
	}
	return tg, ivan, anna
}

func mustScan(t *testing.T, tg *Tgbot) subLinkChanges {
	t.Helper()
	changes, err := tg.subLinkScan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return changes
}

// TestSubLinkDetectors: each change that kills the old link is found, with
// its reason, and nothing else is.
func TestSubLinkDetectors(t *testing.T) {
	cases := []struct {
		name    string
		change  func(t *testing.T, ivan *SubUserView)
		reason  string
		newLink string
	}{
		{"active edge", func(t *testing.T, _ *SubUserView) {
			var edge model.ChainHop
			database.GetDB().Where("name = ?", "edge-b").First(&edge)
			if err := (&ChainService{}).SetActive(edge.Id); err != nil {
				t.Fatal(err)
			}
		}, model.SubLinkReasonEdge, "http://b.example.net:2096/sub/"},
		{"subscriptions path", func(t *testing.T, _ *SubUserView) { setSetting(t, "subPath", "/feed/") },
			model.SubLinkReasonSubPath, "http://localhost:2096/feed/"},
		{"front address", func(t *testing.T, _ *SubUserView) { setNginxFront(t, "only443", true, "vpn.example.com") },
			model.SubLinkReasonFront, "https://vpn.example.com/sub/"},
		{"front port", func(t *testing.T, _ *SubUserView) { setSetting(t, "subPort", "2443") },
			model.SubLinkReasonFront, "http://localhost:2443/sub/"},
		// The public subscription address (#224) is a reason of its own,
		// even with the active edge switched in the same look: the link
		// names the address, not the edge.
		{"public address", func(t *testing.T, _ *SubUserView) {
			setSetting(t, "subPublicURL", "https://sub.example.com")
			var edge model.ChainHop
			database.GetDB().Where("name = ?", "edge-b").First(&edge)
			if err := (&ChainService{}).SetActive(edge.Id); err != nil {
				t.Fatal(err)
			}
		}, model.SubLinkReasonPublic, "https://sub.example.com/sub/"},
		{"subId", func(t *testing.T, ivan *SubUserView) {
			// ivan's Telegram moves to petr: the person's subscription is petr's now.
			petr := mustCreateUser(t, SubUserCreate{Name: "petr", SubId: "petr-sub", InboundIds: []int{2}})
			if _, err := (&SubUserService{}).MoveTelegram(petr.SubId, ivan.TgId); err != nil {
				t.Fatal(err)
			}
		}, model.SubLinkReasonSubId, "http://localhost:2096/sub/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg, ivan, anna := linkFixture(t)
			tc.change(t, ivan)
			changes := mustScan(t, tg)
			if len(changes.users) != 1 {
				t.Fatalf("changes: %+v", changes)
			}
			c := changes.users[0]
			if c.TgId != linkPerson || strings.Join(c.Reasons, ",") != tc.reason || !strings.HasPrefix(c.NewURL, tc.newLink) ||
				c.OldURL != "http://localhost:2096/sub/"+ivan.SubId {
				t.Errorf("change: %+v", c)
			}
			// The link of the whole panel moved: anna, with no Telegram, is
			// left for manual sending; a subId is one person's.
			wantNoTg := tc.reason != model.SubLinkReasonSubId
			got := false
			for _, v := range changes.noTelegram {
				got = got || v.SubId == anna.SubId
			}
			if got != wantNoTg {
				t.Errorf("without Telegram: %+v, want anna: %v", changes.noTelegram, wantNoTg)
			}
			// Unanswered, the change stays.
			if again := mustScan(t, tg); len(again.users) != 1 {
				t.Errorf("a second look: %+v", again)
			}
		})
	}
}

// TestSubLinkQuietChanges: what leaves the link as it is — a new user, a
// Telegram bound, the panel's own port — is no change; a new person is
// remembered with the link they got.
func TestSubLinkQuietChanges(t *testing.T) {
	tg, _, anna := linkFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "maria", TgId: linkPerson2, InboundIds: []int{2}})
	if _, err := (&SubUserService{}).SetTelegram(anna.SubId, 503); err != nil {
		t.Fatal(err)
	}
	setSetting(t, "webPort", "8443")
	if changes := mustScan(t, tg); len(changes.users) != 0 || len(changes.noTelegram) != 0 {
		t.Errorf("changes: %+v", changes)
	}
	var known model.SubLinkKnown
	if err := database.GetDB().First(&known, "tg_id = ?", linkPerson2).Error; err != nil ||
		!strings.HasPrefix(known.URL, "http://localhost:2096/sub/") {
		t.Errorf("maria's link is not remembered: %+v %v", known, err)
	}
}
