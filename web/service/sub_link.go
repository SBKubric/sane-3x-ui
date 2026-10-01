package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The detectors of the link broadcast (#214, #222, docs/spec/users.md §13).
// A subscription link dies when the active edge changes (the host
// override), when the subscriptions' path changes, when the front's address
// or port changes, and when a person's subscription is another subId. All
// four show in one place: the link the panel hands out now
// (subscriptionURLs). So the detector does not hook the places that change
// them; it compares, for every person — a Telegram account that is a user's
// Telegram — the link they were last known to have (sub_link_knowns) with
// the link of their user now, and tells the reason from how the two differ.
// A change that the apps pick up on their own (keys, SNI, the XHTTP path)
// leaves the link as it is and is no change here.
//
// The known link moves only when an admin answers the question about the
// change (either way) or the link is sent: until then a change stays a
// change, across restarts too. A person seen for the first time is
// remembered with the link they have, silently.

// subLinkMu serialises the reads and writes of the known links: the
// periodic look, an admin's answer and a broadcast's deliveries.
var subLinkMu sync.Mutex

// subLinkChange is one person whose link changed.
type subLinkChange struct {
	TgId   int64
	SubId  string // the user's subId now
	Name   string
	Enable bool
	OldURL string
	NewURL string
	// Reasons are how the link changed (model.SubLinkReason*): its server,
	// its path, its subId — one or more.
	Reasons []string
	view    *SubUserView
}

// subLinkChanges is what one look found: the people whose link changed and,
// when the link of the whole panel moved (not just a subId), the users
// without Telegram, who need the new link by hand.
type subLinkChanges struct {
	users      []subLinkChange
	noTelegram []*SubUserView
	base       string // the links' base now: the link is base + subId
}

// recipients are the changed people a broadcast goes to: enabled users.
func (c subLinkChanges) recipients() []subLinkChange {
	var out []subLinkChange
	for _, u := range c.users {
		if u.Enable {
			out = append(out, u)
		}
	}
	return out
}

// reasons are the changes' reasons, each once, in a fixed order.
func (c subLinkChanges) reasons() []string {
	var out []string
	for _, r := range []string{model.SubLinkReasonEdge, model.SubLinkReasonSubPath, model.SubLinkReasonFront, model.SubLinkReasonSubId} {
		for _, u := range c.users {
			if slices.Contains(u.Reasons, r) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// signature tells one set of changes from another: the debounce waits for it
// to stay the same.
func (c subLinkChanges) signature() string {
	parts := []string{c.base}
	for _, u := range c.users {
		parts = append(parts, u.NewURL)
	}
	for _, v := range c.noTelegram {
		parts = append(parts, "-"+v.SubId)
	}
	slices.Sort(parts[1:])
	return strings.Join(parts, "\n")
}

// subLinkBase is the start of every subscription link as the panel hands it
// out now: a link is the base and the subId.
func (t *Tgbot) subLinkBase() string {
	base, _ := t.subscriptionURLs("")
	return base
}

// subLinkUsers are the regular users, as the broadcast sees them.
func subLinkUsers() ([]*SubUserView, error) {
	all, err := (&SubUserService{}).List()
	if err != nil {
		return nil, err
	}
	var out []*SubUserView
	for _, v := range all {
		if !v.Technical {
			out = append(out, v)
		}
	}
	return out, nil
}

// subLinkScan compares the known links with the links now. People seen for
// the first time are remembered, and those who are no user's Telegram any
// more are forgotten; the changes are returned, not written.
func (t *Tgbot) subLinkScan() (subLinkChanges, error) {
	subLinkMu.Lock()
	defer subLinkMu.Unlock()
	base := t.subLinkBase()
	changes := subLinkChanges{base: base}
	users, err := subLinkUsers()
	if err != nil {
		return changes, err
	}
	var rows []model.SubLinkKnown
	db := database.GetDB()
	if err := db.Find(&rows).Error; err != nil {
		return changes, err
	}
	known := map[int64]model.SubLinkKnown{}
	baseMoved := false
	for _, r := range rows {
		known[r.TgId] = r
		if strings.TrimSuffix(r.URL, r.SubId) != base {
			baseMoved = true
		}
	}
	edges := subLinkEdgeHosts()
	seen := map[int64]bool{}
	now := time.Now().UnixMilli()
	for _, v := range users {
		if v.TgId == 0 {
			if v.Enable {
				changes.noTelegram = append(changes.noTelegram, v)
			}
			continue
		}
		if seen[v.TgId] {
			continue // a legacy second user with the same Telegram: the first one is theirs
		}
		seen[v.TgId] = true
		link := base + v.SubId
		r, ok := known[v.TgId]
		if !ok {
			row := model.SubLinkKnown{TgId: v.TgId, SubId: v.SubId, URL: link, ConfHash: t.subLinkConfHash(v), UpdatedAt: now}
			if err := db.Create(&row).Error; err != nil {
				return changes, err
			}
			continue
		}
		if r.URL != link {
			changes.users = append(changes.users, subLinkChange{TgId: v.TgId, SubId: v.SubId, Name: v.Name, Enable: v.Enable,
				OldURL: r.URL, NewURL: link, Reasons: subLinkReasons(r.URL, link, edges), view: v})
		}
	}
	for tgId := range known {
		if !seen[tgId] {
			if err := db.Delete(&model.SubLinkKnown{}, "tg_id = ?", tgId).Error; err != nil {
				return changes, err
			}
		}
	}
	if !baseMoved {
		changes.noTelegram = nil
	}
	return changes, nil
}

// subLinkAcknowledge makes the new links of changes the known ones: the
// admin answered, or the links went out.
func subLinkAcknowledge(users []subLinkChange) error {
	subLinkMu.Lock()
	defer subLinkMu.Unlock()
	now := time.Now().UnixMilli()
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		for _, u := range users {
			if err := tx.Model(&model.SubLinkKnown{}).Where("tg_id = ?", u.TgId).
				Updates(map[string]any{"sub_id": u.SubId, "url": u.NewURL, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// subLinkDelivered records that the person got link, and the configs whose
// hash is confHash ("" leaves the known hash as it is).
func subLinkDelivered(tgId int64, subId, link, confHash string) error {
	subLinkMu.Lock()
	defer subLinkMu.Unlock()
	row := model.SubLinkKnown{TgId: tgId, SubId: subId, URL: link, ConfHash: confHash, UpdatedAt: time.Now().UnixMilli()}
	columns := []string{"sub_id", "url", "updated_at"}
	if confHash != "" {
		columns = append(columns, "conf_hash")
	}
	return database.GetDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tg_id"}},
		DoUpdates: clause.AssignmentColumns(columns)}).Create(&row).Error
}

// subLinkKnownConf is the hash of the configs the person last got; "" for
// none known.
func subLinkKnownConf(tgId int64) string {
	subLinkMu.Lock()
	defer subLinkMu.Unlock()
	var row model.SubLinkKnown
	if err := database.GetDB().Where("tg_id = ?", tgId).Limit(1).Find(&row).Error; err != nil {
		return ""
	}
	return row.ConfHash
}

// subLinkEdgeHosts are the hosts a host override can name: the edges of the
// chain and the legacy override host.
func subLinkEdgeHosts() map[string]bool {
	hosts := map[string]bool{}
	var hops []model.ChainHop
	if err := database.GetDB().Where("role = ?", model.ChainRoleEdge).Find(&hops).Error; err == nil {
		for _, h := range hops {
			hosts[strings.ToLower(h.Host)] = true
		}
	}
	if host, err := (&SettingService{}).GetProxyOverrideHost(); err == nil && strings.TrimSpace(host) != "" {
		hosts[strings.ToLower(strings.TrimSpace(host))] = true
	}
	return hosts
}

// subLinkReasons tell how a link changed: another host that is (or was) an
// edge's — the active edge; another address, port or scheme otherwise — the
// front; another path before the subId — the subscriptions' path; another
// last part — the subId.
func subLinkReasons(oldURL, newURL string, edges map[string]bool) []string {
	o, errOld := url.Parse(oldURL)
	n, errNew := url.Parse(newURL)
	if errOld != nil || errNew != nil {
		return []string{model.SubLinkReasonFront}
	}
	var reasons []string
	if o.Scheme != n.Scheme || !strings.EqualFold(o.Host, n.Host) {
		if edges[strings.ToLower(o.Hostname())] || edges[strings.ToLower(n.Hostname())] {
			reasons = append(reasons, model.SubLinkReasonEdge)
		} else {
			reasons = append(reasons, model.SubLinkReasonFront)
		}
	}
	oldDir, oldLast := path.Split(o.Path)
	newDir, newLast := path.Split(n.Path)
	if oldDir != newDir {
		reasons = append(reasons, model.SubLinkReasonSubPath)
	}
	if oldLast != newLast {
		reasons = append(reasons, model.SubLinkReasonSubId)
	}
	return reasons
}

// subLinkConfFiles are the .conf files (with their QR) of the user's tunnel
// clients, as the tunnel client card and SendAwgConfigsToClients render them,
// and the hash of the configs; none for a user without enabled ones.
func (t *Tgbot) subLinkConfFiles(v *SubUserView) ([]tunnelFile, string) {
	var files []tunnelFile
	h := sha256.New()
	for _, c := range v.Clients {
		if c.Kind == SubUserClientXray || !c.Enable {
			continue
		}
		conf, err := tunnelClientsOf(c.Kind).GetClientConfigByUUID(c.Key)
		if err != nil || conf == "" {
			continue
		}
		h.Write([]byte(c.Key + "\n" + conf + "\n"))
		files = append(files, tunnelConfigFiles(c.Kind, c.Name, conf)...)
	}
	if len(files) == 0 {
		return nil, ""
	}
	return files, hex.EncodeToString(h.Sum(nil))
}

// subLinkConfHash is the hash of the user's tunnel configs; "" for none.
func (t *Tgbot) subLinkConfHash(v *SubUserView) string {
	_, hash := t.subLinkConfFiles(v)
	return hash
}
