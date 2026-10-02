package service

import (
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// The bot's path /third-party/<secret>/ (#220, docs/spec/users.md §12): a
// random URL-safe secret kept in the settings, made once; «Перевыпустить»
// makes a new one and moves a chained panel to a new revision, as making the
// first one at the start does. The path travels in every chain document and
// is published on the panel's front, beside the panel; nothing of it is the
// subscriptions'.

var urlSafeSecret = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)

// TestTheBotPathSecretIsMadeOnceAndRenewed: the first ask makes a URL-safe
// secret and keeps it; a renewal makes another, and only a panel with a
// chain moves its revision for it.
func TestTheBotPathSecretIsMadeOnceAndRenewed(t *testing.T) {
	_, registry := newChainDocuments(t)
	settings := &SettingService{}
	revision := func() int64 {
		r, err := settings.GetChainRevision()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	first, err := settings.GetTgThirdPartySecret()
	if err != nil || !urlSafeSecret.MatchString(first) {
		t.Fatalf("the first secret: %q, %v", first, err)
	}
	if again, _ := settings.GetTgThirdPartySecret(); again != first {
		t.Errorf("the secret moved: %q then %q", first, again)
	}
	if path, _ := settings.ThirdPartyPath(); path != "/third-party/"+first+"/" {
		t.Errorf("the path: %q", path)
	}

	before := revision()
	renewed, err := settings.RenewTgThirdPartySecret()
	if err != nil || renewed == first || !urlSafeSecret.MatchString(renewed) {
		t.Fatalf("renewed: %q, %v", renewed, err)
	}
	if got, _ := settings.GetTgThirdPartySecret(); got != renewed {
		t.Errorf("stored after the renewal: %q", got)
	}
	if revision() != before {
		t.Errorf("a panel with no chain moved its revision")
	}

	enteredHop(t, registry, AddHopInput{Name: "edge-a", Host: "a.example.net", Role: chain.RoleEdge})
	before = revision()
	if _, err := settings.RenewTgThirdPartySecret(); err != nil {
		t.Fatal(err)
	}
	if revision() != before+1 {
		t.Errorf("a chained panel's revision after the renewal: %d, want %d", revision(), before+1)
	}
}

// TestEnsureTheBotPathSecretAtTheStart: at the start a panel with no secret
// makes one and, chained, moves to a new revision — the hops then fetch the
// path; a panel that has one changes nothing.
func TestEnsureTheBotPathSecretAtTheStart(t *testing.T) {
	_, registry := newChainDocuments(t)
	enteredHop(t, registry, AddHopInput{Name: "edge-a", Host: "a.example.net", Role: chain.RoleEdge})
	settings := &SettingService{}
	before, _ := settings.GetChainRevision()
	if err := settings.EnsureTgThirdPartySecret(); err != nil {
		t.Fatal(err)
	}
	secret, _ := settings.getString(tgThirdPartySecretKey)
	if after, _ := settings.GetChainRevision(); !urlSafeSecret.MatchString(secret) || after != before+1 {
		t.Fatalf("after the start: secret %q, revision %d (was %d)", secret, after, before)
	}
	if err := settings.EnsureTgThirdPartySecret(); err != nil {
		t.Fatal(err)
	}
	if again, _ := settings.getString(tgThirdPartySecretKey); again != secret {
		t.Errorf("a second start changed the secret: %q", again)
	}
	if after, _ := settings.GetChainRevision(); after != before+1 {
		t.Errorf("a second start moved the revision to %d", after)
	}
}

// TestEveryDocumentCarriesTheBotPath: the innermost hop and the ones
// outside it all learn the bot's path, the same one, from their documents.
func TestEveryDocumentCarriesTheBotPath(t *testing.T) {
	documents, _ := exampleChain(t)
	setSetting(t, tgThirdPartySecretKey, "s3cr3t")
	all, err := documents.BuildAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("documents: %d", len(all))
	}
	for name, doc := range all {
		if doc.NextHop.ThirdPartyPath != "/third-party/s3cr3t/" {
			t.Errorf("%s: thirdPartyPath %q", name, doc.NextHop.ThirdPartyPath)
		}
	}
}

// TestTheFrontPublishesTheBotPathBesideThePanel: the panel's front passes
// the bot's path to the panel's own port, not to the sub server, whether or
// not the subscriptions are behind it.
func TestTheFrontPublishesTheBotPathBesideThePanel(t *testing.T) {
	s := newNginxTestServer(t)
	seedInbounds(t)
	dir := useIPCertDir(t)
	useCertDirs(t, t.TempDir())
	writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
	setSetting(t, tgThirdPartySecretKey, "s3cr3t")
	port, _ := s.settingService.GetPort()
	subPort, _ := s.settingService.GetSubPort()

	for _, subsBehind := range []bool{true, false} {
		cfg, err := s.buildConfig(NginxSettings{Mode: string(nginx.ModeOnly443), SubsBehind443: subsBehind, RealityPort: 8443})
		if err != nil {
			t.Fatalf("buildConfig: %v", err)
		}
		bot := cfg.Site.Bot
		if bot == nil || len(bot.Paths) != 1 || bot.Paths[0] != "/third-party/s3cr3t/" {
			t.Fatalf("subs behind 443 %v: the bot's path is not published: %+v", subsBehind, bot)
		}
		if want := net.JoinHostPort("127.0.0.1", strconv.Itoa(port)); bot.Target != want {
			t.Errorf("the bot's path goes to %q, want the panel at %q (the sub server is on %d)", bot.Target, want, subPort)
		}
		if cfg.Site.Sub != nil && slicesContains(cfg.Site.Sub.Paths, "/third-party/s3cr3t/") {
			t.Errorf("the bot's path is under the subscriptions: %v", cfg.Site.Sub.Paths)
		}
		http, err := cfg.HTTPConf()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`location /third-party/s3cr3t/ \{\n[^}]*proxy_pass https?://127\.0\.0\.1:` + strconv.Itoa(port) + `;`).MatchString(http) {
			t.Errorf("the HTTP side does not pass the bot's path to the panel:\n%s", http)
		}
	}
}

// TestBotPublicBase: the Mini App opens at the active edge's https address —
// its front, or its own https port — and on a panel with no chain at its own
// front; never at an http address, nor — by default — at the panel's own
// address behind a chain.
func TestBotPublicBase(t *testing.T) {
	newNginxTestServer(t)
	if base, ok := BotPublicBase(); ok {
		t.Errorf("no chain, no front: %q", base)
	}
	setSetting(t, "nginxMode", string(nginx.ModeOnly443))
	setSetting(t, "nginxDomain", "panel.example.com")
	if base, ok := BotPublicBase(); !ok || base != "https://panel.example.com" {
		t.Errorf("no chain, the panel's front: %q %v", base, ok)
	}

	for _, c := range []struct {
		name string
		hop  model.ChainHop
		want string
	}{
		{"front", model.ChainHop{Host: "198.51.100.20", FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"}, "https://198.51.100.20"},
		{"v6 front", model.ChainHop{Host: "2001:db8::1", FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"}, "https://[2001:db8::1]"},
		{"https port", model.ChainHop{Host: "edge.example.com", SubPort: 2096, SubScheme: "https"}, "https://edge.example.com:2096"},
		{"http port", model.ChainHop{Host: "edge.example.com", SubPort: 2096, SubScheme: "http"}, ""},
	} {
		if err := database.GetDB().Where("1 = 1").Delete(&model.ChainHop{}).Error; err != nil {
			t.Fatal(err)
		}
		activeEdge(t, c.hop)
		base, ok := BotPublicBase()
		if base != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v, want %q", c.name, base, ok, c.want)
		}
	}
}

// captchaHostWarnings counts the warnings about an unusable captcha host in
// the panel's log buffer.
func captchaHostWarnings() int {
	n := 0
	for _, line := range logger.GetLogs(10000, "WARNING") {
		if strings.Contains(line, "the captcha's host is") {
			n++
		}
	}
	return n
}

// TestBotPublicBaseByCaptchaHost (#243): tgCaptchaHost moves the Mini App —
// "" and edge to the active edge, panel to the panel's own front in a chain
// too, a hop's name to that hop, a standby edge or an inner one, with the
// active edge's rule of address. A chosen host that cannot serve it gives
// no address and a warning, never another host.
func TestBotPublicBaseByCaptchaHost(t *testing.T) {
	newNginxTestServer(t)
	dir := useIPCertDir(t)
	useCertDirs(t, t.TempDir())
	hops := []model.ChainHop{
		{Name: "edge-a", Role: chain.RoleEdge, State: chain.StateJoined, IsActive: true,
			Host: "198.51.100.20", FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"},
		{Name: "edge-b", Role: chain.RoleEdge, State: chain.StateJoined,
			Host: "2001:db8::2", FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"},
		{Name: "inner-a", Role: chain.RoleInner, State: chain.StateJoined,
			Host: "inner.example.net", FrontMode: chain.FrontOff, SubPort: 2096, SubScheme: "https"},
		{Name: "inner-http", Role: chain.RoleInner, State: chain.StateJoined,
			Host: "plain.example.net", FrontMode: chain.FrontOff, SubPort: 2096, SubScheme: "http"},
		{Name: "edge-new", Role: chain.RoleEdge, State: chain.StatePending,
			Host: "new.example.net", SubPort: 2096, SubScheme: "https"},
		{Name: "edge-gone", Role: chain.RoleEdge, State: chain.StateDraining,
			Host: "gone.example.net", FrontMode: chain.FrontOnly443, SubPort: 443, SubScheme: "https"},
	}
	for i := range hops {
		if err := database.GetDB().Create(&hops[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	panelFront := func(mode nginx.Mode, domain string, ipCert bool) {
		setSetting(t, "nginxMode", string(mode))
		setSetting(t, "nginxDomain", domain)
		_ = os.RemoveAll(dir)
		if ipCert {
			writeIPCert(t, dir, time.Now().Add(5*24*time.Hour), []string{"203.0.113.5"}, nil)
		}
	}

	for _, c := range []struct {
		name, choice string
		front        func()
		want         string
		warns        bool
	}{
		{"default", "", nil, "https://198.51.100.20", false},
		{"edge", "edge", nil, "https://198.51.100.20", false},
		{"panel by its domain", "panel", func() { panelFront(nginx.ModeOnly443, "panel.example.com", true) },
			"https://panel.example.com", false},
		{"panel by its IP certificate", "panel", func() { panelFront(nginx.ModeOnly443, "", true) },
			"https://203.0.113.5", false},
		{"panel with its front off", "panel", func() { panelFront(nginx.ModeOff, "panel.example.com", true) }, "", true},
		{"panel with neither a domain nor an IP certificate", "panel", func() { panelFront(nginx.ModeOnly443, "", false) },
			"", true},
		{"a standby edge", "edge-b", nil, "https://[2001:db8::2]", false},
		{"an inner hop on its https port", "inner-a", nil, "https://inner.example.net:2096", false},
		{"a hop without https", "inner-http", nil, "", true},
		{"a hop not yet joined", "edge-new", nil, "", true},
		{"a hop on its way out", "edge-gone", nil, "", true},
		{"a hop not in the chain", "edge-z", nil, "", true},
	} {
		if c.front != nil {
			c.front()
		}
		setSetting(t, tgCaptchaHostKey, c.choice)
		before := captchaHostWarnings()
		base, ok := BotPublicBase()
		if base != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v, want %q", c.name, base, ok, c.want)
		}
		if warned := captchaHostWarnings() > before; warned != c.warns {
			t.Errorf("%s: warned %v, want %v", c.name, warned, c.warns)
		}
	}

	// With no active edge the default is the panel's front, as without a
	// chain; a chosen hop does not need one.
	if err := database.GetDB().Model(&model.ChainHop{}).Where("1 = 1").Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	panelFront(nginx.ModeOnly443, "panel.example.com", false)
	for choice, want := range map[string]string{"": "https://panel.example.com", "edge-b": "https://[2001:db8::2]"} {
		setSetting(t, tgCaptchaHostKey, choice)
		if base, ok := BotPublicBase(); !ok || base != want {
			t.Errorf("no active edge, %q: %q %v, want %q", choice, base, ok, want)
		}
	}
}

// TestSetTgCaptchaHost: the CLI's setter takes edge, panel, a hop name and
// "" (trimmed), and refuses anything else, keeping what was stored.
func TestSetTgCaptchaHost(t *testing.T) {
	newNginxTestServer(t)
	s := &SettingService{}
	if got, _ := s.GetTgCaptchaHost(); got != "" {
		t.Fatalf("the default: %q", got)
	}
	for _, value := range []string{"panel", "edge", "edge-b", " inner-1 ", ""} {
		if err := s.SetTgCaptchaHost(value); err != nil {
			t.Errorf("%q: %v", value, err)
		}
		if got, _ := s.GetTgCaptchaHost(); got != strings.TrimSpace(value) {
			t.Errorf("%q stored as %q", value, got)
		}
	}
	_ = s.SetTgCaptchaHost("panel")
	for _, bad := range []string{"Panel", "edge_b", "https://panel.example.com", "203.0.113.5", strings.Repeat("a", 33)} {
		if err := s.SetTgCaptchaHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, _ := s.GetTgCaptchaHost(); got != "panel" {
		t.Errorf("after the refusals: %q", got)
	}
}
