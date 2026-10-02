package service

import (
	"encoding/hex"
	"hash/fnv"
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
// or port changes, when the public subscription address is set or changed
// (#224), and when a person's subscription is another subId. All five show
// in one place: the link the panel hands out now
// (subscriptionURLs). So the detector does not hook the places that change
// them; it compares, for every person — a Telegram account that is a user's
// Telegram — the link they were last known to have (sub_link_knowns) with
// the link of their user now, and tells the reason from how the two differ.
// A change that the apps pick up on their own (keys, SNI, the XHTTP path)
// leaves the link as it is and is no change here.
//
// Two changes do not show in the link (#225). The VPN name: set, changed or
// cleared, it is another address in every VLESS link — reason vpnName, for
// the whole panel. And the AWG Endpoint, which stays by the active edge's
// address: with the VPN name set (or the public subscription address), a
// switch of the active edge leaves the link and the VLESS links as they are,
// so it is no change for a person with xray clients only, but a person with
// tunnel clients needs the new .conf — reason edge for them. So a known row
// also keeps the VPN name and the host override the person was known with.
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
	// its path, its subId, the VPN name — one or more.
	Reasons []string
	// VPNName and EdgeHost are the VPN name and the host override now, which
	// the known row takes once the change is settled.
	VPNName  string
	EdgeHost string
	view     *SubUserView
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
	for _, r := range []string{model.SubLinkReasonPublic, model.SubLinkReasonVPNName, model.SubLinkReasonEdge, model.SubLinkReasonSubPath, model.SubLinkReasonFront, model.SubLinkReasonSubId} {
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
		parts = append(parts, u.NewURL+" "+strings.Join(u.Reasons, ",")+" "+u.VPNName+" "+u.EdgeHost)
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

// subLinkState is what the links name besides the link itself (#225): the
// VPN name the VLESS links name ("" while the override is off: then they
// name the real server, VPN name or not) and the host override, which the
// AWG Endpoint names ("" for none).
func subLinkState() (vpnName, edgeHost string) {
	settings := &SettingService{}
	edgeHost, on := settings.GetProxyOverride()
	if !on {
		return "", ""
	}
	vpnName, _ = settings.GetVPNName()
	return vpnName, edgeHost
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
	public := subLinkPublicHost()
	vpnName, edgeHost := subLinkState()
	vpnMoved := false
	for _, r := range rows {
		vpnMoved = vpnMoved || r.VpnName != vpnName
	}
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
			row := model.SubLinkKnown{TgId: v.TgId, SubId: v.SubId, URL: link, ConfHash: t.subLinkConfHash(v),
				VpnName: vpnName, EdgeHost: &edgeHost, UpdatedAt: now}
			if err := db.Create(&row).Error; err != nil {
				return changes, err
			}
			continue
		}
		var reasons []string
		if r.URL != link {
			reasons = subLinkReasons(r.URL, link, edges, public)
		}
		if r.VpnName != vpnName {
			reasons = append(reasons, model.SubLinkReasonVPNName)
		}
		if r.EdgeHost == nil || *r.EdgeHost != edgeHost {
			// Another host override: the .conf of a person with tunnel
			// clients names the old edge. A row from before #225 does not
			// know its host and takes the current one silently, as does a
			// person whose configs do not name it.
			if r.EdgeHost != nil && !slices.Contains(reasons, model.SubLinkReasonEdge) && t.subLinkConfHash(v) != "" {
				reasons = append(reasons, model.SubLinkReasonEdge)
			} else if len(reasons) == 0 {
				if err := db.Model(&model.SubLinkKnown{}).Where("tg_id = ?", v.TgId).Update("edge_host", edgeHost).Error; err != nil {
					return changes, err
				}
			}
		}
		if len(reasons) > 0 {
			changes.users = append(changes.users, subLinkChange{TgId: v.TgId, SubId: v.SubId, Name: v.Name, Enable: v.Enable,
				OldURL: r.URL, NewURL: link, Reasons: reasons, VPNName: vpnName, EdgeHost: edgeHost, view: v})
		}
	}
	for tgId := range known {
		if !seen[tgId] {
			if err := db.Delete(&model.SubLinkKnown{}, "tg_id = ?", tgId).Error; err != nil {
				return changes, err
			}
		}
	}
	if !baseMoved && !vpnMoved {
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
				Updates(map[string]any{"sub_id": u.SubId, "url": u.NewURL, "vpn_name": u.VPNName, "edge_host": u.EdgeHost,
					"updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// subLinkDelivered records that the person got link, and was told of the
// configs whose hash is confHash ("" leaves the known hash as it is), as the
// links are now (the VPN name, the host override).
func subLinkDelivered(tgId int64, subId, link, confHash string) error {
	vpnName, edgeHost := subLinkState()
	subLinkMu.Lock()
	defer subLinkMu.Unlock()
	row := model.SubLinkKnown{TgId: tgId, SubId: subId, URL: link, ConfHash: confHash, VpnName: vpnName, EdgeHost: &edgeHost,
		UpdatedAt: time.Now().UnixMilli()}
	columns := []string{"sub_id", "url", "vpn_name", "edge_host", "updated_at"}
	if confHash != "" {
		columns = append(columns, "conf_hash")
	}
	return database.GetDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tg_id"}},
		DoUpdates: clause.AssignmentColumns(columns)}).Create(&row).Error
}

// subLinkKnownConf is the hash of the configs the person was last told of
// (or had when first seen); "" for none known.
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

// subLinkPublicHost is the host of the public subscription address (#224),
// "" while none is set.
func subLinkPublicHost() string {
	public, err := (&SettingService{}).GetSubPublicURL()
	if err != nil || public == "" {
		return ""
	}
	u, err := url.Parse(public)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// subLinkReasons tell how a link changed: another host where the new one is
// the public subscription address — the public address; another host that is
// (or was) an edge's — the active edge; another address, port or scheme
// otherwise — the front; another path before the subId — the subscriptions'
// path; another last part — the subId. A cleared public address is told by
// the server the link names again (the edge or the front): the address it
// had is not kept.
func subLinkReasons(oldURL, newURL string, edges map[string]bool, public string) []string {
	o, errOld := url.Parse(oldURL)
	n, errNew := url.Parse(newURL)
	if errOld != nil || errNew != nil {
		return []string{model.SubLinkReasonFront}
	}
	var reasons []string
	if o.Scheme != n.Scheme || !strings.EqualFold(o.Host, n.Host) {
		if public != "" && strings.EqualFold(n.Hostname(), public) {
			reasons = append(reasons, model.SubLinkReasonPublic)
		} else if edges[strings.ToLower(o.Hostname())] || edges[strings.ToLower(n.Hostname())] {
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

// subLinkConfHash is the hash of the configs of the user's enabled tunnel
// clients; "" for none. The broadcast tells the person when it differs from
// the one they were last told of (#245: the .conf itself is on «📄 My
// configs»).
func (t *Tgbot) subLinkConfHash(v *SubUserView) string {
	h := fnv.New128a()
	found := false
	for _, c := range v.Clients {
		if c.Kind == SubUserClientXray || !c.Enable {
			continue
		}
		conf, err := tunnelClientsOf(c.Kind).GetClientConfigByUUID(c.Key)
		if err != nil || conf == "" {
			continue
		}
		h.Write([]byte(c.Key + "\n" + conf + "\n"))
		found = true
	}
	if !found {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
