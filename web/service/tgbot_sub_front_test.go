package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestTgbotSubLinksFollowTheFront (#145): the bot hands out the same
// subscription address as the sub page — https on 443 under the domain or, with
// no domain, the address of the IP certificate; the sub port otherwise. An
// active edge wins over the front, as it does on the sub page: the link names
// the edge, and 443 only when the edge itself is behind its front.
func TestTgbotSubLinksFollowTheFront(t *testing.T) {
	cases := []struct {
		name          string
		mode          string
		subsBehind443 bool
		domain        string
		ipCert        bool
		edgeFront     *bool // nil: no active edge
		wantSub       string
		wantJSON      string
	}{
		{name: "domain", mode: "only443", subsBehind443: true, domain: "vpn.example.com",
			wantSub: "https://vpn.example.com/sub/abc", wantJSON: "https://vpn.example.com/json/abc"},
		{name: "IP certificate only", mode: "only443", subsBehind443: true, ipCert: true,
			wantSub: "https://203.0.113.5/sub/abc", wantJSON: "https://203.0.113.5/json/abc"},
		{name: "neither", mode: "only443", subsBehind443: true,
			wantSub: "http://localhost:2096/sub/abc", wantJSON: "http://localhost:2096/json/abc"},
		{name: "front-end off", mode: "off", subsBehind443: true, ipCert: true,
			wantSub: "http://localhost:2096/sub/abc", wantJSON: "http://localhost:2096/json/abc"},
		{name: "subscriptions on their own port", mode: "only443", ipCert: true,
			wantSub: "http://localhost:2096/sub/abc", wantJSON: "http://localhost:2096/json/abc"},
		{name: "active edge without a front wins over the domain", mode: "only443", subsBehind443: true,
			domain: "vpn.example.com", edgeFront: new(bool),
			wantSub: "http://a.example.net:2096/sub/abc", wantJSON: "http://a.example.net:2096/json/abc"},
		{name: "active edge without a front wins over the IP certificate", mode: "only443", subsBehind443: true,
			ipCert: true, edgeFront: new(bool),
			wantSub: "http://a.example.net:2096/sub/abc", wantJSON: "http://a.example.net:2096/json/abc"},
		{name: "active edge behind its front", mode: "only443", subsBehind443: true, ipCert: true, edgeFront: boolRef(true),
			wantSub: "https://a.example.net/sub/abc", wantJSON: "https://a.example.net/json/abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
				t.Fatalf("InitDB: %v", err)
			}
			t.Cleanup(func() { database.CloseDB() })
			dir := useIPCertDir(t)
			if tc.ipCert {
				writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
			}
			setNginxFront(t, tc.mode, tc.subsBehind443, tc.domain)
			for key, value := range map[string]string{
				"subPort": "2096", "subPath": "/sub/", "subJsonPath": "/json/", "subJsonEnable": "true",
			} {
				setSetting(t, key, value)
			}
			monInbound(t, 1, model.VLESS, true, model.Client{ID: "11111111-1111-1111-1111-111111111111",
				Email: "alice", SubID: "abc", Enable: true})
			if tc.edgeFront != nil {
				edge := &model.ChainHop{Name: "edge-a", Host: "a.example.net", Role: model.ChainRoleEdge,
					State: model.ChainStateJoined, IsActive: true, SubPort: 2096, SubScheme: "http"}
				if *tc.edgeFront {
					edge.FrontMode, edge.SubPort, edge.SubScheme = "only443", 443, "https"
				}
				if err := database.GetDB().Create(edge).Error; err != nil {
					t.Fatal(err)
				}
			}

			subURL, jsonURL, err := (&Tgbot{}).buildSubscriptionURLs("alice")
			if err != nil {
				t.Fatalf("buildSubscriptionURLs: %v", err)
			}
			if subURL != tc.wantSub || jsonURL != tc.wantJSON {
				t.Errorf("URLs = %q, %q; want %q, %q", subURL, jsonURL, tc.wantSub, tc.wantJSON)
			}
		})
	}
}
func boolRef(value bool) *bool { return &value }
