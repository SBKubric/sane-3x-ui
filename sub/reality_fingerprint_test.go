package sub

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// realityFingerprintCases: a Reality inbound whose client half names no uTLS
// fingerprint hands its clients firefox (#230); one that names a fingerprint
// keeps it — existing inbounds are not migrated.
var realityFingerprintCases = []struct {
	name     string
	settings string // realitySettings.settings
	want     string
}{
	{"no fingerprint key", `{"publicKey":"pub-1","spiderX":"/"}`, "firefox"},
	{"empty fingerprint", `{"publicKey":"pub-1","fingerprint":"","spiderX":"/"}`, "firefox"},
	{"explicit chrome kept", `{"publicKey":"pub-1","fingerprint":"chrome","spiderX":"/"}`, "chrome"},
	{"explicit safari kept", `{"publicKey":"pub-1","fingerprint":"safari","spiderX":"/"}`, "safari"},
}

// seedRealityInbound stores one VLESS Reality inbound with one client on
// subscription "sub-fp" and returns nothing: the subscription services read it
// back from the database the way /sub, /json and /clash do.
func seedRealityInbound(t *testing.T, realityClientSettings string) {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	resetInboundsCache()
	ib := &model.Inbound{
		UserId: 1, Remark: "vless-reality", Enable: true, Port: 443,
		Protocol: "vless", Tag: "inbound-443",
		Settings: `{"clients":[{"id":"a5899a9e-dc44-4ec4-bba1-72b6e29bd2d1","email":"fp-client",` +
			`"subId":"sub-fp","enable":true,"flow":"xtls-rprx-vision"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"reality","tcpSettings":{},` +
			`"realitySettings":{"show":false,"target":"example.com:443","serverNames":["example.com"],` +
			`"privateKey":"priv-1","shortIds":["ab12"],"settings":` + realityClientSettings + `}}`,
		Sniffing: `{"enabled":false}`,
	}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
}

func TestRealityFingerprint_ShareLinkDefaultsToFirefox(t *testing.T) {
	for _, tc := range realityFingerprintCases {
		t.Run(tc.name, func(t *testing.T) {
			seedRealityInbound(t, tc.settings)
			links, _, _, err := NewSubService(false, "-ieo", "").GetSubs("sub-fp", "example.com")
			if err != nil {
				t.Fatalf("GetSubs: %v", err)
			}
			if len(links) != 1 {
				t.Fatalf("want one link, got %d: %v", len(links), links)
			}
			u, err := url.Parse(links[0])
			if err != nil {
				t.Fatalf("parse %q: %v", links[0], err)
			}
			if got := u.Query().Get("fp"); got != tc.want {
				t.Fatalf("fp = %q, want %q in %s", got, tc.want, links[0])
			}
		})
	}
}

func TestRealityFingerprint_JsonSubDefaultsToFirefox(t *testing.T) {
	for _, tc := range realityFingerprintCases {
		t.Run(tc.name, func(t *testing.T) {
			seedRealityInbound(t, tc.settings)
			svc := NewSubJsonService("", "", "", "", NewSubService(false, "-ieo", ""))
			out, _, err := svc.GetJson("sub-fp", "example.com")
			if err != nil {
				t.Fatalf("GetJson: %v", err)
			}
			var cfg struct {
				Outbounds []struct {
					Protocol       string `json:"protocol"`
					StreamSettings struct {
						RealitySettings map[string]any `json:"realitySettings"`
					} `json:"streamSettings"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal([]byte(out), &cfg); err != nil {
				t.Fatalf("subscription is not a single JSON config: %v\n%s", err, out)
			}
			for _, ob := range cfg.Outbounds {
				if ob.Protocol != "vless" {
					continue
				}
				if got := ob.StreamSettings.RealitySettings["fingerprint"]; got != tc.want {
					t.Fatalf("realitySettings.fingerprint = %v, want %q\n%s", got, tc.want, out)
				}
				return
			}
			t.Fatalf("no vless outbound in subscription:\n%s", out)
		})
	}
}

func TestRealityFingerprint_ClashSubDefaultsToFirefox(t *testing.T) {
	for _, tc := range realityFingerprintCases {
		t.Run(tc.name, func(t *testing.T) {
			seedRealityInbound(t, tc.settings)
			out, _, err := NewSubClashService(NewSubService(false, "-ieo", "")).GetClash("sub-fp", "example.com")
			if err != nil {
				t.Fatalf("GetClash: %v", err)
			}
			want := "client-fingerprint: " + tc.want
			if !strings.Contains(out, want) {
				t.Fatalf("clash config lacks %q:\n%s", want, out)
			}
		})
	}
}

// The monitoring probe configs are rendered by the same link code, so a probe
// through a Reality inbound without a fingerprint imitates firefox too.
func TestRealityFingerprint_ProbeLinkDefaultsToFirefox(t *testing.T) {
	seedRealityInbound(t, `{"publicKey":"pub-1","spiderX":"/"}`)
	var ib model.Inbound
	if err := database.GetDB().First(&ib).Error; err != nil {
		t.Fatalf("load inbound: %v", err)
	}
	link := NewSubService(false, "", "").ProbeLink(&ib, "fp-client", "192.0.2.10", "")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse %q: %v", link, err)
	}
	if got := u.Query().Get("fp"); got != DefaultRealityFingerprint {
		t.Fatalf("fp = %q, want %q in %s", got, DefaultRealityFingerprint, link)
	}
}
