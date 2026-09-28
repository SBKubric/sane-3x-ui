package service

import (
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// TestBuildConfig_HTTPSidePublishesTheTunnelSubscription: with the
// subscriptions behind 443, /tun is published beside /sub and /json, and not
// while the tunnel subscription is off (docs/spec/tunnel-subscription.md §6).
func TestBuildConfig_HTTPSidePublishesTheTunnelSubscription(t *testing.T) {
	s := newNginxTestServer(t)
	seedInbounds(t)
	dir := useIPCertDir(t)
	useCertDirs(t, t.TempDir())
	writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)

	paths := func() []string {
		t.Helper()
		cfg, err := s.buildConfig(NginxSettings{Mode: string(nginx.ModeOnly443), SubsBehind443: true, RealityPort: 8443})
		if err != nil {
			t.Fatalf("buildConfig: %v", err)
		}
		if cfg.Site == nil || cfg.Site.Sub == nil {
			t.Fatalf("no subscriptions on the HTTP side: %+v", cfg.Site)
		}
		return cfg.Site.Sub.Paths
	}
	if got := paths(); !slicesContains(got, "/tun/") {
		t.Errorf("sub paths = %v, want /tun/ among them", got)
	}
	if err := s.settingService.setString("subTunEnable", "false"); err != nil {
		t.Fatal(err)
	}
	if got := paths(); slicesContains(got, "/tun/") {
		t.Errorf("sub paths = %v with the tunnel subscription off", got)
	}
}
