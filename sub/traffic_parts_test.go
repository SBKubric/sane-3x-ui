package sub

import (
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/subpage"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// The breakdown of the total limit (#247): the panel sends the clients
// behind Subscription-Userinfo's sum in subpage.TrafficPartsHeader for the
// hops' pages, and its own page lists them.

// storeClientTraffic writes the traffic row of an xray client: used and
// limit in GB.
func storeClientTraffic(t *testing.T, inboundId int, email string, usedGB, limitGB int64) {
	t.Helper()
	ct := &xray.ClientTraffic{InboundId: inboundId, Email: email, Enable: true, Down: usedGB << 30, Total: limitGB << 30}
	if err := database.GetDB().Create(ct).Error; err != nil {
		t.Fatal(err)
	}
}

// limitTunnelPeer gives a tunnel client its traffic and limit in GB.
func limitTunnelPeer(t *testing.T, c model.TunnelClient, usedGB, limitGB int64) {
	t.Helper()
	err := database.GetDB().Model(&model.TunnelClient{}).Where("uuid = ?", c.UUID).
		Updates(map[string]any{"download": usedGB << 30, "total_gb": limitGB << 30}).Error
	if err != nil {
		t.Fatal(err)
	}
	service.InvalidateTunnelSubCache()
}

// TestSubAnswerCarriesTheTrafficBreakdown: an app's raw subscription carries
// every client of the subscription — the xray ones, then the tunnels — with
// Subscription-Userinfo as it was: the sum of the xray clients.
func TestSubAnswerCarriesTheTrafficBreakdown(t *testing.T) {
	engine := newTunTestServer(t, nil)
	storeXhttpClient(t, 1, "ivan-xhttp", "quota-ivan")
	storeVlessClient(t, 2, "ivan-tcp", "quota-ivan")
	storeClientTraffic(t, 1, "ivan-xhttp", 3, 50)
	storeClientTraffic(t, 2, "ivan-tcp", 0, 50)
	limitTunnelPeer(t, addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "quota-ivan"), 1, 20)

	rec := getTun(t, engine, "/sub/quota-ivan", nil)
	if rec.Code != 200 {
		t.Fatalf("raw: %d %s", rec.Code, rec.Body.String())
	}
	want := []subpage.TrafficPart{
		{Protocol: "vless", Remark: "nl", Used: 3 << 30, Limit: 50 << 30},
		{Protocol: "vless", Remark: "nl", Limit: 50 << 30},
		{Protocol: "amneziawg", Used: 1 << 30, Limit: 20 << 30},
	}
	if got := subpage.ParseTrafficParts(rec.Header().Get(subpage.TrafficPartsHeader)); !reflect.DeepEqual(got, want) {
		t.Errorf("breakdown %+v\nwant %+v", got, want)
	}
	if got := rec.Header().Get("Subscription-Userinfo"); got != "upload=0; download=3221225472; total=107374182400; expire=0" {
		t.Errorf("Subscription-Userinfo = %q", got)
	}

	// The tunnels alone: /tun carries theirs.
	tun := getTun(t, engine, "/tun/quota-ivan", nil)
	if got := subpage.ParseTrafficParts(tun.Header().Get(subpage.TrafficPartsHeader)); !reflect.DeepEqual(got, want[2:]) {
		t.Errorf("/tun breakdown %+v", got)
	}
}

// TestSubPageShowsTheTotalLimit: the panel's own page words the total limit
// of all the clients and lists each.
func TestSubPageShowsTheTotalLimit(t *testing.T) {
	engine := newTunTestServer(t, nil)
	storeVlessClient(t, 1, "anna-tcp", "quota-anna")
	storeClientTraffic(t, 1, "anna-tcp", 2, 50)
	limitTunnelPeer(t, addTunnelPeer(t, model.TunnelKindAwg, "anna-phone", "quota-anna"), 1, 50)

	page := html.UnescapeString(getTun(t, engine, "/sub/quota-anna", browser).Body.String())
	for _, want := range []string{"Total limit: 100.00GB (50.00GB × 2 protocols)", "<span>VLESS</span><span>2.00GB / 50.00GB</span>",
		"<span>AmneziaWG</span><span>1.00GB / 50.00GB</span>"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
