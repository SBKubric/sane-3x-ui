package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// setNginxFront stores a front-end configuration without applying it, which is
// what Apply leaves behind once it has succeeded.
func setNginxFront(t *testing.T, mode string, subsBehind443 bool, domain string) {
	t.Helper()
	set := SettingService{}
	pairs := map[string]string{
		"nginxMode":          mode,
		"nginxDomain":        domain,
		"nginxSubsBehind443": map[bool]string{true: "true", false: "false"}[subsBehind443],
	}
	for key, value := range pairs {
		if err := set.setString(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
}

// TestPublicSubBase covers the three ways a box can publish its subscriptions
// on 443 or not: under its own domain, by address with the IP certificate on
// the HTTP side (#145), and not at all. With neither there is nothing on 443
// that would answer for them, so the sub server's own address stands.
func TestPublicSubBase(t *testing.T) {
	type ipCert struct {
		ips     []string
		expired bool
	}
	cases := []struct {
		name          string
		mode          string
		subsBehind443 bool
		domain        string
		ipCert        *ipCert
		subDomain     string
		panelHost     string
		wantOk        bool
		wantHost      string
	}{
		{name: "front-end off", mode: string(nginx.ModeOff), subsBehind443: true, domain: "vpn.example.com"},
		{name: "subscriptions left on their own port", mode: string(nginx.ModeShared), domain: "vpn.example.com"},
		{name: "no domain to publish under", mode: string(nginx.ModeShared), subsBehind443: true},
		{
			name: "published", mode: string(nginx.ModeShared), subsBehind443: true,
			domain: "vpn.example.com", wantOk: true, wantHost: "vpn.example.com",
		},
		{
			name: "published, only 443", mode: string(nginx.ModeOnly443), subsBehind443: true,
			domain: "vpn.example.com", wantOk: true, wantHost: "vpn.example.com",
		},
		{
			name: "the domain wins over the IP certificate", mode: string(nginx.ModeOnly443), subsBehind443: true,
			domain: "vpn.example.com", ipCert: &ipCert{ips: []string{"203.0.113.5"}},
			wantOk: true, wantHost: "vpn.example.com",
		},
		{
			name: "IP certificate only, only 443", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5"}}, wantOk: true, wantHost: "203.0.113.5",
		},
		{
			name: "IP certificate only, shared", mode: string(nginx.ModeShared), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5"}}, wantOk: true, wantHost: "203.0.113.5",
		},
		{
			name: "IP certificate, front-end off", mode: string(nginx.ModeOff), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5"}},
		},
		{
			name: "IP certificate, subscriptions on their own port", mode: string(nginx.ModeOnly443),
			ipCert: &ipCert{ips: []string{"203.0.113.5"}},
		},
		{
			name: "expired IP certificate", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5"}, expired: true},
		},
		{
			name: "IPv4 before IPv6", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"2001:db8::5", "203.0.113.5"}}, wantOk: true, wantHost: "203.0.113.5",
		},
		{
			name: "IPv6 only, bracketed for a URL", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"2001:db8::5"}}, wantOk: true, wantHost: "[2001:db8::5]",
		},
		{
			name: "the sub domain picks among the certificate's addresses", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5", "198.51.100.9"}}, subDomain: "198.51.100.9",
			wantOk: true, wantHost: "198.51.100.9",
		},
		{
			name: "the chain panel host picks among the certificate's addresses", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5", "2001:db8::5"}}, panelHost: "2001:db8::5",
			wantOk: true, wantHost: "[2001:db8::5]",
		},
		{
			name: "an address the certificate does not name is not used", mode: string(nginx.ModeOnly443), subsBehind443: true,
			ipCert: &ipCert{ips: []string{"203.0.113.5"}}, subDomain: "192.0.2.1", panelHost: "subs.example.com",
			wantOk: true, wantHost: "203.0.113.5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
				t.Fatalf("InitDB: %v", err)
			}
			t.Cleanup(func() { database.CloseDB() })
			dir := useIPCertDir(t)
			if tc.ipCert != nil {
				notAfter := time.Now().Add(5 * 24 * time.Hour)
				if tc.ipCert.expired {
					notAfter = time.Now().Add(-time.Hour)
				}
				writeIPCert(t, dir, notAfter, tc.ipCert.ips, nil)
			}
			setNginxFront(t, tc.mode, tc.subsBehind443, tc.domain)
			setSetting(t, "subDomain", tc.subDomain)
			setSetting(t, "chainPanelHost", tc.panelHost)

			scheme, host, ok := PublicSubBase()
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if !ok {
				return
			}
			if scheme != "https" {
				t.Errorf("scheme = %q, want https", scheme)
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
		})
	}
}

// TestSubURIFollowsTheFrontEnd is the reason PublicSubBase exists: the address
// the panel shows and hands to clients has to be the one nginx answers on, not
// the subscription server's own port. That port is an unadvertised second way
// in at best, and closed at worst.
func TestSubURIFollowsTheFrontEnd(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	// GetDefaultSettings asks Xray where its access log is, which means reading
	// the generated config; give it one so the whole call does not fail here.
	bin := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", bin)
	if err := os.WriteFile(filepath.Join(bin, "config.json"), []byte(`{"log":{}}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	set := SettingService{}
	for key, value := range map[string]string{
		"subEnable": "true",
		"subDomain": "subs.example.com",
		"subPort":   "2096",
		"subPath":   "/sub/",
	} {
		if err := set.setString(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	// Without the front-end the subscription server's own address stands.
	setNginxFront(t, string(nginx.ModeOff), false, "")
	got, err := set.GetDefaultSettings("panel.example.com:2053")
	if err != nil {
		t.Fatalf("GetDefaultSettings: %v", err)
	}
	if uri := got.(map[string]any)["subURI"].(string); uri != "http://subs.example.com:2096/sub/" {
		t.Errorf("subURI with the front-end off = %q", uri)
	}

	// With it, the site's domain on the public port — and a URL says 443 by
	// saying nothing.
	setNginxFront(t, string(nginx.ModeShared), true, "vpn.example.com")
	got, err = set.GetDefaultSettings("panel.example.com:2053")
	if err != nil {
		t.Fatalf("GetDefaultSettings: %v", err)
	}
	if uri := got.(map[string]any)["subURI"].(string); uri != "https://vpn.example.com/sub/" {
		t.Errorf("subURI with the front-end on = %q, want https://vpn.example.com/sub/", uri)
	}

	// No domain and no IP certificate: nothing on 443 answers for the
	// subscriptions, so the link cannot pretend otherwise (#145).
	dir := useIPCertDir(t)
	setNginxFront(t, string(nginx.ModeOnly443), true, "")
	got, err = set.GetDefaultSettings("panel.example.com:2053")
	if err != nil {
		t.Fatalf("GetDefaultSettings: %v", err)
	}
	if uri := got.(map[string]any)["subURI"].(string); uri != "http://subs.example.com:2096/sub/" {
		t.Errorf("subURI with neither a domain nor an IP certificate = %q", uri)
	}

	// The IP certificate alone: the HTTP side answers by address on 443, and
	// the link names the address the certificate is for (#145).
	writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
	got, err = set.GetDefaultSettings("panel.example.com:2053")
	if err != nil {
		t.Fatalf("GetDefaultSettings: %v", err)
	}
	if uri := got.(map[string]any)["subURI"].(string); uri != "https://203.0.113.5/sub/" {
		t.Errorf("subURI with the IP certificate only = %q, want https://203.0.113.5/sub/", uri)
	}
}

// TestPlanSaysWhenTheSubscriptionAddressMoves: nothing about links may change
// silently. The plan already names every inbound whose port moves; publishing
// the subscriptions moves an address too, and has to be said out loud before
// the operator confirms.
func TestPlanSaysWhenTheSubscriptionAddressMoves(t *testing.T) {
	svc := newNginxTestServer(t)
	set := SettingService{}
	for key, value := range map[string]string{
		"subEnable": "true",
		"subDomain": "subs.example.com",
		"subPort":   "2096",
	} {
		if err := set.setString(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	subsLine := func(plan NginxPlan) *NginxChange {
		for i := range plan.Changes {
			if plan.Changes[i].Kind == "subs" {
				return &plan.Changes[i]
			}
		}
		return nil
	}

	// Leaving the subscriptions where they are says nothing.
	if line := subsLine(svc.Plan(NginxSettings{Mode: string(nginx.ModeShared), Domain: "vpn.example.com"})); line != nil {
		t.Errorf("unexpected subscription line: %+v", line)
	}

	line := subsLine(svc.Plan(NginxSettings{
		Mode: string(nginx.ModeShared), Domain: "vpn.example.com", SubsBehind443: true,
	}))
	if line == nil {
		t.Fatal("publishing the subscriptions was not mentioned in the plan")
	}
	if line.From != "http://subs.example.com:2096" || line.To != "https://vpn.example.com" {
		t.Errorf("subscription line = %q → %q", line.From, line.To)
	}

	// Without a domain the IP certificate publishes them by address, and the
	// plan says the address the links will carry (#145).
	writeIPCert(t, useIPCertDir(t), time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
	line = subsLine(svc.Plan(NginxSettings{Mode: string(nginx.ModeOnly443), SubsBehind443: true}))
	if line == nil {
		t.Fatal("publishing the subscriptions by address was not mentioned in the plan")
	}
	if line.From != "http://subs.example.com:2096" || line.To != "https://203.0.113.5" {
		t.Errorf("subscription line = %q → %q", line.From, line.To)
	}
}

// TestStatusAndPlanWarnWhenNothingServesTheSubscriptions (#145): in only443
// with the subscriptions behind 443 the sub port is closed, and without a
// domain or an IP certificate nothing on 443 answers for them either. The
// links and the chain's poll then lead nowhere, and the operator has to hear
// it from the panel rather than from the clients.
func TestStatusAndPlanWarnWhenNothingServesTheSubscriptions(t *testing.T) {
	cases := []struct {
		name          string
		mode          string
		subsBehind443 bool
		domain        string
		ipCert        bool
		want          bool
	}{
		{name: "neither", mode: string(nginx.ModeOnly443), subsBehind443: true, want: true},
		{name: "domain", mode: string(nginx.ModeOnly443), subsBehind443: true, domain: "vpn.example.com"},
		{name: "IP certificate", mode: string(nginx.ModeOnly443), subsBehind443: true, ipCert: true},
		{name: "subscriptions on their own port", mode: string(nginx.ModeOnly443)},
		{name: "shared keeps the sub port open", mode: string(nginx.ModeShared), subsBehind443: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newNginxTestServer(t)
			t.Cleanup(func() { database.CloseDB() })
			dir := useIPCertDir(t)
			useCertDirs(t, t.TempDir())
			if tc.ipCert {
				writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
			}
			setSetting(t, "subEnable", "true")
			setNginxFront(t, tc.mode, tc.subsBehind443, tc.domain)

			if got := hasWarning(svc.GetStatus().Warnings, "subsUnreachable"); got != tc.want {
				t.Errorf("status warns = %v, want %v", got, tc.want)
			}
			plan := svc.Plan(NginxSettings{Mode: tc.mode, SubsBehind443: tc.subsBehind443, Domain: tc.domain, RealityPort: 8443})
			if got := hasWarning(plan.Warnings, "subsUnreachable"); got != tc.want {
				t.Errorf("plan warns = %v, want %v", got, tc.want)
			}
			if hasWarning(plan.Blockers, "subsUnreachable") {
				t.Error("an unreachable subscription is a warning, not a blocker: the operator may mean it")
			}
		})
	}
}
