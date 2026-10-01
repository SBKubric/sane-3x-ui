package sub

import (
	"net/http"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The VPN name (#225) in what the sub server hands out: with the host
// override on, the VLESS links and the JSON configs name it instead of the
// active edge's address; the AWG Endpoint stays by address; without the
// override it changes nothing.
func TestSubServerConfigsNameTheVPNName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		vpnName  string
		override bool
		want     string // the address the VLESS link and the JSON config name
		endpoint string // the AWG Endpoint's host
	}{
		{"override, no VPN name", "", true, "192.0.2.10", "192.0.2.10"},
		{"override and a VPN name", "vpn.example.com", true, "vpn.example.com", "192.0.2.10"},
		{"a VPN name, no override", "vpn.example.com", false, "panel.example.com", "198.51.100.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newTunTestServer(t, map[string]string{"subEncrypt": "false", "subJsonEnable": "true",
				"subJsonPath": "/json/", "vpnName": tc.vpnName})
			storeVlessClient(t, 1, "ivan-nl", "vpn-ivan")
			addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "vpn-ivan")
			setTunnelEndpoint(t, model.TunnelKindAwg, "198.51.100.5:51820")
			if tc.override {
				edge := &model.ChainHop{Name: "edge-a", Host: "192.0.2.10", Role: model.ChainRoleEdge,
					State: model.ChainStateJoined, IsActive: true, SubPort: 2096, SubScheme: "http"}
				if err := database.GetDB().Create(edge).Error; err != nil {
					t.Fatal(err)
				}
			}

			rec := getTun(t, engine, "/sub/vpn-ivan", nil)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "@"+tc.want+":20001") {
				t.Errorf("/sub: %d %s; want the address %s", rec.Code, rec.Body.String(), tc.want)
			}
			rec = getTun(t, engine, "/json/vpn-ivan", nil)
			if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `"`+tc.want+`"`) ||
				(tc.want != "192.0.2.10" && strings.Contains(body, "192.0.2.10")) {
				t.Errorf("/json: %d %s; want the address %s", rec.Code, body, tc.want)
			}
			rec = getTun(t, engine, "/tun/vpn-ivan", nil)
			if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, "Endpoint = "+tc.endpoint+":51820") {
				t.Errorf("/tun: %d %s; want the Endpoint %s", rec.Code, body, tc.endpoint)
			}
		})
	}
}

// TestProbeLinksIgnoreTheVPNName: a monitoring probe goes where its path
// says — the hop's own address — whatever the VPN name.
func TestProbeLinksIgnoreTheVPNName(t *testing.T) {
	newTunTestServer(t, map[string]string{"vpnName": "vpn.example.com"})
	edge := &model.ChainHop{Name: "edge-a", Host: "192.0.2.10", Role: model.ChainRoleEdge,
		State: model.ChainStateJoined, IsActive: true, SubPort: 2096, SubScheme: "http"}
	if err := database.GetDB().Create(edge).Error; err != nil {
		t.Fatal(err)
	}
	storeVlessClient(t, 1, "probe-x", "probes")
	var ib model.Inbound
	if err := database.GetDB().First(&ib, 1).Error; err != nil {
		t.Fatal(err)
	}
	link := probeLinks{}.ProbeLink(&ib, "probe-x", "panel.example.com", "192.0.2.20")
	if !strings.Contains(link, "@192.0.2.20:20001") {
		t.Errorf("probe link %q", link)
	}
}
