package service

import (
	"net"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
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
// front; never at an http address, nor at the panel's own address behind a
// chain.
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
