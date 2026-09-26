package service

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// guardedPanel is a panel ready for only443 on the HTTP side: a Reality
// inbound to route, an IP certificate, a secret base path.
func guardedPanel(t *testing.T) *NginxService {
	t.Helper()
	s := newNginxTestServer(t)
	resetMonContactForTest()
	t.Cleanup(resetMonContactForTest)
	seedInbounds(t)
	dir := useIPCertDir(t)
	useCertDirs(t, t.TempDir())
	writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
	if err := s.settingService.setString("webBasePath", "/uS2J19TzcfZuEAPyNH/"); err != nil {
		t.Fatal(err)
	}
	return s
}

func only443Behind() NginxSettings {
	return NginxSettings{Mode: string(nginx.ModeOnly443), PanelBehind443: true, SubsBehind443: true, RealityPort: 8443}
}

// TestBuildConfig_Only443PublishesOnlyTheLoginAndTheAPI (#141): the panel's
// UI is reached through an SSH tunnel; on the HTTP side only what an API
// client needs to log in and call the API is published, and the rest of the
// base path is the stub.
func TestBuildConfig_Only443PublishesOnlyTheLoginAndTheAPI(t *testing.T) {
	s := guardedPanel(t)
	cfg, err := s.buildConfig(only443Behind())
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	site := cfg.Site
	if site == nil || site.Panel == nil || site.Login == nil {
		t.Fatalf("the panel is not published: %+v", site)
	}
	if want := []string{"/uS2J19TzcfZuEAPyNH/panel/api/"}; !slices.Equal(site.Panel.Paths, want) {
		t.Errorf("panel paths = %v, want %v", site.Panel.Paths, want)
	}
	if want := []string{"/uS2J19TzcfZuEAPyNH/login"}; !slices.Equal(site.Login.Paths, want) {
		t.Errorf("login paths = %v, want %v", site.Login.Paths, want)
	}
	if want := []string{"/uS2J19TzcfZuEAPyNH/"}; !slices.Equal(site.Decoy, want) {
		t.Errorf("decoy = %v, want the base path", site.Decoy)
	}
	if site.Guard == nil || site.Guard.MissLog != nginx.MissLogPath {
		t.Errorf("guard = %+v, want one writing to %s", site.Guard, nginx.MissLogPath)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the config does not validate: %v", err)
	}
}

// TestBuildConfig_GuardExemptsTheChainAndMonServer (#141): the hops fetch
// subscriptions and the wave for every client behind them and mon-server
// calls in bursts; none of them may be limited or banned. A hop entered by
// name counts by the address its join came from; the name is not resolved.
func TestBuildConfig_GuardExemptsTheChainAndMonServer(t *testing.T) {
	s := guardedPanel(t)
	monHop(t, "bridge", "inner", "joined", 1, false, "198.51.100.4")
	monHop(t, "edge-a", "edge", "joined", 0, true, "edge-a.example.net")
	monHopUpdate(t, "edge-a", map[string]any{"observed_addr": "[2001:db8::a]:51234"})
	monHop(t, "edge-b", "edge", "pending", 0, false, "edge-b.example.net")
	(&MonitoringService{}).NoteMonServerAddr("203.0.113.40")

	cfg, err := s.buildConfig(only443Behind())
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	want := []string{"198.51.100.4", "2001:db8::a", "203.0.113.40"}
	if got := cfg.Site.Guard.Exempt; !slices.Equal(got, want) {
		t.Errorf("exempt = %v, want %v", got, want)
	}
}

// TestBuildConfig_SharedModeIsNotGuarded: the protection comes with only443
// (#141); shared mode and a panel not behind 443 render as before.
func TestBuildConfig_SharedModeIsNotGuarded(t *testing.T) {
	s := guardedPanel(t)
	cfg, err := s.buildConfig(NginxSettings{Mode: string(nginx.ModeShared), SubsBehind443: true, RealityPort: 8443})
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Site.Guard != nil || cfg.Site.Login != nil || len(cfg.Site.Decoy) > 0 {
		t.Errorf("shared mode is guarded: %+v", cfg.Site)
	}
}

// fakeJails records what the panel asks of fail2ban.
type fakeJails struct {
	applied []nginx.Jails
	removed int
	err     error
}

func useFakeJails(t *testing.T, installed bool) *fakeJails {
	t.Helper()
	fake := &fakeJails{}
	prevApply, prevRemove, prevInstalled := applyJails, removeJails, fail2banInstalled
	applyJails = func(j nginx.Jails) error { fake.applied = append(fake.applied, j); return fake.err }
	removeJails = func() error { fake.removed++; return nil }
	fail2banInstalled = func() bool { return installed }
	jailsProblem.set(nil)
	t.Cleanup(func() {
		applyJails, removeJails, fail2banInstalled = prevApply, prevRemove, prevInstalled
		jailsProblem.set(nil)
	})
	return fake
}

// TestSyncJailsFollowsTheMode (#141): the jails go in with only443 — the
// login jail only where the login is published — and come out without it.
func TestSyncJailsFollowsTheMode(t *testing.T) {
	s := guardedPanel(t)
	fake := useFakeJails(t, true)
	(&MonitoringService{}).NoteMonServerAddr("203.0.113.40")

	cfg, err := s.buildConfig(only443Behind())
	if err != nil {
		t.Fatal(err)
	}
	s.syncJails(cfg)
	if len(fake.applied) != 1 {
		t.Fatalf("only443: %d applies", len(fake.applied))
	}
	got := fake.applied[0]
	if got.MissLog != nginx.MissLogPath || got.LoginLog != filepath.Join(config.GetLogFolder(), "3xui.log") ||
		!slices.Equal(got.IgnoreIP, []string{"203.0.113.40"}) {
		t.Errorf("jails = %+v", got)
	}

	// The panel kept on its own port: no login on the HTTP side to guard.
	set := only443Behind()
	set.PanelBehind443 = false
	cfg, _ = s.buildConfig(set)
	s.syncJails(cfg)
	if got := fake.applied[len(fake.applied)-1]; got.LoginLog != "" {
		t.Errorf("a login jail without a published login: %+v", got)
	}

	for _, mode := range []nginx.Mode{nginx.ModeShared, nginx.ModeOff} {
		cfg, _ := s.buildConfig(NginxSettings{Mode: string(mode), RealityPort: 8443})
		before := fake.removed
		s.syncJails(cfg)
		if fake.removed != before+1 {
			t.Errorf("%s: the jails were not taken out", mode)
		}
	}
}

// TestStatusWarnsAboutFail2ban (#141): only443 without fail2ban still limits,
// but bans nobody — the Nginx page says so; and a fail2ban that would not
// take the jails says why.
func TestStatusWarnsAboutFail2ban(t *testing.T) {
	s := guardedPanel(t)
	fake := useFakeJails(t, false)
	if err := s.SaveSettings(only443Behind()); err != nil {
		t.Fatal(err)
	}
	if !hasWarning(s.GetStatus().Warnings, "noFail2ban") {
		t.Error("only443 without fail2ban went unreported")
	}
	if err := s.SaveSettings(NginxSettings{Mode: string(nginx.ModeShared), RealityPort: 8443}); err != nil {
		t.Fatal(err)
	}
	if hasWarning(s.GetStatus().Warnings, "noFail2ban") {
		t.Error("shared mode, which has no jails, was warned about fail2ban")
	}

	useFakeJails(t, true)
	fake = useFakeJails(t, true)
	fake.err = errors.New("start fail2ban: Have not found any log file for sshd jail")
	cfg, _ := s.buildConfig(only443Behind())
	s.syncJails(cfg)
	if err := s.SaveSettings(only443Behind()); err != nil {
		t.Fatal(err)
	}
	if !hasWarning(s.GetStatus().Warnings, "fail2banProblem") {
		t.Errorf("a fail2ban that would not start went unreported: %v", s.GetStatus().Warnings)
	}
	fake.err = nil
	s.syncJails(cfg)
	if hasWarning(s.GetStatus().Warnings, "fail2banProblem") {
		t.Error("the fail2ban warning outlived the problem")
	}
}
