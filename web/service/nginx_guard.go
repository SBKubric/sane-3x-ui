package service

import (
	"errors"
	"path/filepath"
	"sort"
	"sync"

	"github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// The protection of the panel's HTTP side in only443 (ADR 0005, #141): limits
// per client address, a miss log, and fail2ban's jails reading it and the
// panel's own log of failed logins.

// The fail2ban side, as seams: the tests watch what the panel asks of it.
var (
	applyJails        = nginx.ApplyJails
	removeJails       = nginx.RemoveJails
	fail2banInstalled = nginx.Fail2banInstalled
)

// jailsProblem is what fail2ban last said when it would not take the jails —
// a daemon that would not start, a file of the operator's in our place. The
// reconcile keeps trying; the Nginx page says why it keeps failing.
var jailsProblem problemNote

type problemNote struct {
	mu   sync.Mutex
	text string
}

func (p *problemNote) set(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.text = ""
	if err != nil {
		p.text = err.Error()
	}
}

func (p *problemNote) get() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.text
}

// panelLogFile is the log the panel writes (logger.initFileBackend), where a
// failed login names the client.
func panelLogFile() string { return filepath.Join(config.GetLogFolder(), "3xui.log") }

// guard is the protection of the HTTP side, with the exemptions as they are
// now. The reconcile renders it every half minute, so a hop that joins or
// mon-server calling from a new address is exempt within a tick.
func (s *NginxService) guard() *nginx.Guard {
	return &nginx.Guard{Exempt: s.frontExemptions(), MissLog: nginx.MissLogPath}
}

// frontExemptions are the addresses the HTTP side neither limits nor bans:
// every hop of the chain registry and mon-server. A hop fetches
// subscriptions and the wave on behalf of every client behind it, so to the
// panel all of them are one address; mon-server calls in bursts.
//
// A hop counts by its host when that is an address, and by the address its
// join arrived from. A host name is not resolved: the answer could change
// under the config, and the join's address is the one the hop really uses.
func (s *NginxService) frontExemptions() []string {
	seen := map[string]bool{}
	add := func(value string) {
		if ip, ok := nginx.ExemptAddress(value); ok {
			seen[ip] = true
		}
	}
	var hops []model.ChainHop
	if err := database.GetDB().Find(&hops).Error; err != nil {
		logger.Warning("nginx: cannot read the chain registry for the front's exemptions:", err)
	}
	for _, hop := range hops {
		add(hop.Host)
		add(hop.ObservedAddr)
	}
	add((&MonitoringService{}).MonServerAddr())

	out := make([]string, 0, len(seen))
	for ip := range seen {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

// syncJails puts fail2ban's jails in step with cfg: in with a guarded HTTP
// side, out otherwise. Nothing here fails the front — the limits hold
// without fail2ban — so a problem is logged once and shown on the Nginx page.
func (s *NginxService) syncJails(cfg nginx.Config) {
	var err error
	if cfg.Mode == nginx.ModeOnly443 && cfg.Site != nil && cfg.Site.Guard != nil {
		jails := nginx.Jails{MissLog: cfg.Site.Guard.MissLog, IgnoreIP: cfg.Site.Guard.Exempt}
		// A login jail only where the login is published: banned from 80
		// and 443, a guesser on the panel's own port would not notice.
		if cfg.Site.Login != nil {
			jails.LoginLog = panelLogFile()
		}
		err = applyJails(jails)
	} else {
		err = removeJails()
	}
	if errors.Is(err, nginx.ErrNoFail2ban) {
		// Said on the Nginx page, not in the log every half minute.
		err = nil
	}
	if err != nil && jailsProblem.get() != err.Error() {
		logger.Warning("nginx: fail2ban:", err)
	}
	jailsProblem.set(err)
}

// fail2banWarnings is what the Nginx page says about fail2ban in only443.
func fail2banWarnings(set NginxSettings) []NginxWarning {
	if set.Mode != string(nginx.ModeOnly443) {
		return nil
	}
	if !fail2banInstalled() {
		return []NginxWarning{warn("noFail2ban")}
	}
	if problem := jailsProblem.get(); problem != "" {
		return []NginxWarning{{Code: "fail2banProblem", Text: problem}}
	}
	return nil
}
