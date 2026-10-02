package service

import (
	"net"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/coinman-dev/3ax-ui/v2/util/random"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"

	"gorm.io/gorm"
)

// The bot's own path on the 443 front (#188 point 11 amended, #220,
// docs/spec/users.md §12): /third-party/<secret>/, where the bot's Mini App
// — the captcha — is served. It has nothing to do with the subscriptions:
// not under subPath, not on the sub server. The front of real passes it to
// the panel; every hop passes it to its next hop, having learned it from
// the chain document (nextHop.thirdPartyPath), as it learns the
// subscription paths.
//
// <secret> is a random URL-safe string kept in the settings, made the first
// time it is asked for (at the panel's start, EnsureTgThirdPartySecret).
// The Telegram tab shows it with «Перевыпустить», which makes a new one: the
// old path stops answering at once, and the chain moves to a new revision
// so the hops take the new one.

// ThirdPartyRoot is where the bot's paths start.
const ThirdPartyRoot = "/third-party/"

// tgThirdPartySecretKey is the setting the secret lives in; "" until the
// first time it is asked for.
const tgThirdPartySecretKey = "tgThirdPartySecret"

// thirdPartySecretLength is the secret's length: 24 letters and digits, about
// 143 bits.
const thirdPartySecretLength = 24

// GetTgThirdPartySecret is the secret of the bot's path, made and stored
// the first time it is asked for. Not to be called inside a transaction:
// making it writes.
func (s *SettingService) GetTgThirdPartySecret() (string, error) {
	secret, _, err := s.tgThirdPartySecret()
	return secret, err
}

// EnsureTgThirdPartySecret makes the secret at the panel's start when there
// is none yet, and then moves a panel with a chain to a new revision: the
// path is new in every document, and a hop holding the last revision would
// otherwise never fetch it. The web server runs this before the sub server
// serves its first document.
func (s *SettingService) EnsureTgThirdPartySecret() error {
	_, made, err := s.tgThirdPartySecret()
	if err != nil || !made {
		return err
	}
	return database.GetDB().Transaction(func(tx *gorm.DB) error { return bumpIfChained(tx) })
}

// tgThirdPartySecret is the stored secret, made and stored when there is
// none; made says it was.
func (s *SettingService) tgThirdPartySecret() (secret string, made bool, err error) {
	secret, err = s.getString(tgThirdPartySecretKey)
	if err != nil {
		return "", false, err
	}
	if secret = strings.TrimSpace(secret); secret != "" {
		return secret, false, nil
	}
	secret = random.Seq(thirdPartySecretLength)
	if err := s.setString(tgThirdPartySecretKey, secret); err != nil {
		return "", false, err
	}
	return secret, true, nil
}

// RenewTgThirdPartySecret is «Перевыпустить»: a new secret, the old path
// gone. A panel with a chain moves to a new revision in the same
// transaction: the path travels in every document (§3.4), and a hop must
// not keep the old one under an unchanged ETag.
func (s *SettingService) RenewTgThirdPartySecret() (string, error) {
	secret := random.Seq(thirdPartySecretLength)
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := saveSettingTx(tx, tgThirdPartySecretKey, secret); err != nil {
			return err
		}
		return bumpIfChained(tx)
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

// bumpIfChained moves a panel that has hops to a new revision; a panel with
// no chain keeps its counter still.
func bumpIfChained(tx *gorm.DB) error {
	var hops int64
	if err := tx.Model(&model.ChainHop{}).Count(&hops).Error; err != nil {
		return err
	}
	if hops == 0 {
		return nil
	}
	return bumpRevisionTx(tx)
}

// ThirdPartyPath is the bot's path with its secret, both slashes on:
// /third-party/<secret>/.
func (s *SettingService) ThirdPartyPath() (string, error) {
	secret, err := s.GetTgThirdPartySecret()
	if err != nil {
		return "", err
	}
	return ThirdPartyRoot + secret + "/", nil
}

// The captcha's host (#243): tgCaptchaHost chooses where the Mini App
// opens — the active edge ("" or edge, the default), the panel's own front
// (panel), or one hop of the chain by its name. Every hop serves the bot's
// path, a standby edge and an inner one as well: each learns it from its
// document and passes it inwards (proxy/subserver_thirdparty.go), so no hop
// needs to know which of them the bot names. Users open the Mini App while
// connected to the VPN, so a captcha on an edge is reached through the
// tunnel and back; the panel's front has no such way round, at the price of
// its address in the link.
//
// The choice touches the captcha's link only. A chosen host that cannot
// serve it — a hop gone from the registry, not yet joined or leaving, one
// without https, a panel whose front is off or has no domain nor IP
// certificate — gives no link and a warning: never another host, which the
// owner did not choose.

// tgCaptchaHostKey is the setting the choice lives in.
const tgCaptchaHostKey = "tgCaptchaHost"

// GetTgCaptchaHost is the captcha's host as stored: "", edge, panel or a
// hop's name.
func (s *SettingService) GetTgCaptchaHost() (string, error) {
	return s.getString(tgCaptchaHostKey)
}

// SetTgCaptchaHost stores the captcha's host, "" for the default; the CLI
// calls it. It refuses a value that is neither edge, panel nor a hop name,
// as the settings form does; a hop name not in the registry is stored, and
// gives no link until such a hop exists.
func (s *SettingService) SetTgCaptchaHost(value string) error {
	value = strings.TrimSpace(value)
	if !entity.ValidTgCaptchaHost(value) {
		return common.NewError("captcha host must be edge, panel or a hop name ([a-z0-9-]{1,32}):", value)
	}
	return s.setString(tgCaptchaHostKey, value)
}

// BotPublicBase is the https address Telegram opens the bot's Mini App at,
// with no path, by tgCaptchaHost: the active edge's — and, on a panel with
// no active edge, the panel's own front —, the panel's own front, or the
// named hop's. ok is false when there is none: Telegram opens a Mini App on
// https only.
func BotPublicBase() (string, bool) {
	db := database.GetDB()
	if db == nil {
		return "", false
	}
	choice, err := (&SettingService{}).GetTgCaptchaHost()
	if err != nil {
		return "", false
	}
	switch choice = strings.TrimSpace(choice); choice {
	case "", entity.TgCaptchaHostEdge:
		var hops []model.ChainHop
		if err := db.Where("is_active = ?", true).Limit(1).Find(&hops).Error; err != nil {
			return "", false
		}
		if len(hops) > 0 {
			return hopPublicBase(hops[0])
		}
		return panelPublicBase()
	case entity.TgCaptchaHostPanel:
		base, ok := panelPublicBase()
		if !ok {
			logger.Warning("captcha: the captcha's host is the panel, and its front has no https address (nginx off, or neither a domain nor an IP certificate): the bot gives no captcha link")
		}
		return base, ok
	}
	var hops []model.ChainHop
	if err := db.Where("name = ?", choice).Limit(1).Find(&hops).Error; err != nil {
		return "", false
	}
	if len(hops) == 0 {
		logger.Warningf("captcha: the captcha's host is hop %q, which is not in the chain: the bot gives no captcha link", choice)
		return "", false
	}
	hop := hops[0]
	if hop.State == chain.StatePending || hop.State == chain.StateDraining {
		logger.Warningf("captcha: the captcha's host is hop %q, which is %s: the bot gives no captcha link", choice, hop.State)
		return "", false
	}
	base, ok := hopPublicBase(hop)
	if !ok {
		logger.Warningf("captcha: the captcha's host is hop %q, which serves no https (neither its front on 443 nor an https sub port): the bot gives no captcha link", choice)
	}
	return base, ok
}

// hopPublicBase is a hop's https address: its front on 443, else its own
// port when that serves https.
func hopPublicBase(hop model.ChainHop) (string, bool) {
	host := strings.Trim(strings.TrimSpace(hop.Host), "[]")
	switch {
	case host == "":
		return "", false
	case hop.FrontMode == chain.FrontOnly443:
		return "https://" + urlHostname(host), true
	case hop.SubScheme == "https" && hop.SubPort > 0:
		if hop.SubPort == 443 {
			return "https://" + urlHostname(host), true
		}
		return "https://" + net.JoinHostPort(host, strconv.Itoa(hop.SubPort)), true
	}
	return "", false
}

// panelPublicBase is the panel's own front on 443: its domain, else the
// address its IP certificate names.
func panelPublicBase() (string, bool) {
	var ns NginxService
	set := ns.GetSettings()
	if nginx.Mode(set.Mode) == nginx.ModeOff {
		return "", false
	}
	if set.Domain != "" {
		return "https://" + set.Domain, true
	}
	if host, ok := ns.ipCertHost(); ok {
		return "https://" + host, true
	}
	return "", false
}

// urlHostname writes a host the way it goes into a URL with no port: an
// IPv6 address in brackets.
func urlHostname(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return urlHost(ip)
	}
	return host
}
