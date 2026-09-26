package sub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// writeIPCert installs a certificate for ip where install.sh puts the box's
// IP certificate, in a directory of the test's own.
func writeIPCert(t *testing.T, ip string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ip")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(5 * 24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.UseIPCertDirForTest(dir))
}

// TestBuildURLsFollowTheIPOnlyFront (#145): a panel in only443 with no domain
// publishes its subscriptions on 443 by address when its IP certificate is on
// the HTTP side, and the links have to say so — the sub port they used to
// name is closed. An active edge still wins: the links name it, and 443 only
// when the edge itself is behind its front.
func TestBuildURLsFollowTheIPOnlyFront(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		subsBehind string
		domain     string
		ipCert     bool
		edgeFront  *bool // nil: no active edge
		wantSub    string
		wantJSON   string
	}{
		{name: "domain", mode: "only443", subsBehind: "true", domain: "vpn.example.com",
			wantSub: "https://vpn.example.com/sub/abc", wantJSON: "https://vpn.example.com/json/abc"},
		{name: "IP certificate only", mode: "only443", subsBehind: "true", ipCert: true,
			wantSub: "https://203.0.113.5/sub/abc", wantJSON: "https://203.0.113.5/json/abc"},
		{name: "neither", mode: "only443", subsBehind: "true",
			wantSub: "https://panel.example.com:2053/sub/abc", wantJSON: "https://panel.example.com:2053/json/abc"},
		{name: "front-end off", mode: "off", subsBehind: "true", ipCert: true,
			wantSub: "https://panel.example.com:2053/sub/abc", wantJSON: "https://panel.example.com:2053/json/abc"},
		{name: "subscriptions on their own port", mode: "only443", subsBehind: "false", ipCert: true,
			wantSub: "https://panel.example.com:2053/sub/abc", wantJSON: "https://panel.example.com:2053/json/abc"},
		{name: "active edge without a front wins", mode: "only443", subsBehind: "true", ipCert: true, edgeFront: new(bool),
			wantSub: "http://a.example.net:2096/sub/abc", wantJSON: "http://a.example.net:2096/json/abc"},
		{name: "active edge behind its front wins", mode: "only443", subsBehind: "true", ipCert: true, edgeFront: ptr(true),
			wantSub: "https://a.example.net/sub/abc", wantJSON: "https://a.example.net/json/abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
				t.Fatalf("InitDB: %v", err)
			}
			t.Cleanup(func() { database.CloseDB() })
			t.Cleanup(service.UseIPCertDirForTest(filepath.Join(t.TempDir(), "no-ip-cert")))
			if tc.ipCert {
				writeIPCert(t, "203.0.113.5")
			}
			db := database.GetDB()
			for key, value := range map[string]string{
				"subPort": "2096", "nginxMode": tc.mode, "nginxDomain": tc.domain, "nginxSubsBehind443": tc.subsBehind,
			} {
				db.Where("key = ?", key).Delete(&model.Setting{})
				if err := db.Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.edgeFront != nil {
				edge := &model.ChainHop{Name: "edge-a", Host: "a.example.net", Role: model.ChainRoleEdge,
					State: model.ChainStateJoined, IsActive: true, SubPort: 2096, SubScheme: "http"}
				if *tc.edgeFront {
					edge.FrontMode, edge.SubPort, edge.SubScheme = "only443", 443, "https"
				}
				if err := db.Create(edge).Error; err != nil {
					t.Fatal(err)
				}
			}

			svc := NewSubService(false, "", "")
			svc.overrideHost, svc.overrideOn = svc.settingService.GetProxyOverride()
			subURL, jsonURL, _ := svc.BuildURLs("https", "panel.example.com:2053", "/sub/", "/json/", "/clash/", "abc")
			if subURL != tc.wantSub || jsonURL != tc.wantJSON {
				t.Errorf("URLs = %q, %q; want %q, %q", subURL, jsonURL, tc.wantSub, tc.wantJSON)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
