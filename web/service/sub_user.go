package service

import (
	"sort"
	"strings"
	"sync"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// SubUserService manages users (docs/spec/users.md): a person with one name,
// one subscription and one client per inbound. It is the one place the
// user rules live — the Telegram bot calls it directly, the panel through
// /panel/api/users — and the guards of the ordinary client paths
// (InboundService, the AWG/WG API) call its ValidateClientWrites.
//
// A user owns no client rows of its own: ownership is read off the clients
// every time (an xray client's subId, a tunnel client's link), so a client
// written through a path that knows nothing about users still lands with the
// right user. Sync creates the user of any subId that has none yet.
type SubUserService struct {
	inboundService InboundService
	awgService     AwgService
	wgService      WgService
	links          TunnelSubscriptionService
	settingService SettingService
	xrayService    XrayService
}

// subUserMu serialises every write to users and their clients, and Sync:
// without it two creates could both see a name as free.
var subUserMu sync.Mutex

// Client kinds of SubUserClient.
const (
	SubUserClientXray = "xray"
	SubUserClientAwg  = model.TunnelKindAwg
	SubUserClientWg   = model.TunnelKindWg
)

// SubUserClient is one client of a user: an xray client in an inbound's
// settings, or a tunnel peer. Traffic, limits and expiry are the client's own.
type SubUserClient struct {
	Kind string `json:"kind"` // xray | awg | wg
	// InboundId is the inbound row the client lives in; a tunnel client
	// belongs to the amneziawg or nativewg row, 0 when there is none.
	InboundId     int    `json:"inboundId"`
	InboundRemark string `json:"inboundRemark"`
	Protocol      string `json:"protocol"`
	Name          string `json:"name"` // the email; unique across protocols for new writes
	// Key addresses the client in its own API: the id, password, email or
	// auth of an xray client (as updateClient/:clientId takes it), the uuid
	// of a tunnel client.
	Key        string `json:"key"`
	SubId      string `json:"subId"` // the subId it carries or is linked to; "" for none
	Enable     bool   `json:"enable"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	AllTime    int64  `json:"allTime"`
	TotalGB    int64  `json:"totalGB"` // bytes, 0 = unlimited
	ExpiryTime int64  `json:"expiryTime"`
	LimitIp    int    `json:"limitIp"`
	TgId       int64  `json:"tgId"`
	Reset      int    `json:"reset"`
	LastOnline int64  `json:"lastOnline"`

	owner string // key of the owning user
}

// subUserIndex is everything the user rules need, read in one go.
type subUserIndex struct {
	users      map[string]*model.SubUser // by key
	clients    []SubUserClient           // xray by inbound id and settings order, then awg, then wg
	probeSubId string
	inbounds   map[int]*model.Inbound
	tunnelRow  map[string]*model.Inbound // amneziawg/nativewg inbound row by tunnel kind
}

// xrayClientKey is the id UpdateInboundClient and DelInboundClient address
// an xray client by.
func xrayClientKey(protocol model.Protocol, c model.Client) string {
	switch protocol {
	case model.Trojan:
		return c.Password
	case model.Shadowsocks, model.Mixed, model.HTTP:
		return c.Email
	case model.Hysteria, model.Hysteria2:
		return c.Auth
	default:
		return c.ID
	}
}

// ownerKey is the user a client belongs to: monitoring for a probe account
// and for the probe subId, robot for a client without a subscription, else
// the user under the client's subId.
func (idx *subUserIndex) ownerKey(name, subId string) string {
	switch {
	case IsProbeAccount(name):
		return model.SubUserMonitoringKey
	case subId == "":
		return model.SubUserRobotKey
	case idx.probeSubId != "" && subId == idx.probeSubId:
		return model.SubUserMonitoringKey
	default:
		return subId
	}
}

// loadIndex reads users, inbounds, tunnel clients, links and traffic.
func (s *SubUserService) loadIndex() (*subUserIndex, error) {
	db := database.GetDB()
	probeSubId, err := s.settingService.GetMonProbeSubId()
	if err != nil {
		return nil, err
	}
	idx := &subUserIndex{
		users:      map[string]*model.SubUser{},
		probeSubId: probeSubId,
		inbounds:   map[int]*model.Inbound{},
		tunnelRow:  map[string]*model.Inbound{},
	}

	var users []model.SubUser
	if err := db.Find(&users).Error; err != nil {
		return nil, err
	}
	for i := range users {
		idx.users[users[i].SubId] = &users[i]
	}

	var traffics []xray.ClientTraffic
	if err := db.Find(&traffics).Error; err != nil {
		return nil, err
	}
	trafficByEmail := make(map[string]*xray.ClientTraffic, len(traffics))
	for i := range traffics {
		trafficByEmail[traffics[i].Email] = &traffics[i]
	}

	var inbounds []*model.Inbound
	if err := db.Order("id asc").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	for _, ib := range inbounds {
		idx.inbounds[ib.Id] = ib
		switch ib.Protocol {
		case model.AmneziaWG:
			if idx.tunnelRow[model.TunnelKindAwg] == nil {
				idx.tunnelRow[model.TunnelKindAwg] = ib
			}
			continue
		case model.NativeWG:
			if idx.tunnelRow[model.TunnelKindWg] == nil {
				idx.tunnelRow[model.TunnelKindWg] = ib
			}
			continue
		case model.MTProto:
			// MTProto clients live in their own table and are not part of users.
			continue
		}
		clients, _ := s.inboundService.GetClients(ib)
		for _, c := range clients {
			uc := SubUserClient{
				Kind: SubUserClientXray, InboundId: ib.Id, InboundRemark: ib.Remark, Protocol: string(ib.Protocol),
				Name: c.Email, Key: xrayClientKey(ib.Protocol, c), SubId: c.SubID, Enable: c.Enable,
				TotalGB: c.TotalGB, ExpiryTime: c.ExpiryTime, LimitIp: c.LimitIP, TgId: c.TgID, Reset: c.Reset,
			}
			if tr := trafficByEmail[c.Email]; tr != nil && c.Email != "" {
				uc.Up, uc.Down, uc.AllTime, uc.LastOnline = tr.Up, tr.Down, tr.AllTime, tr.LastOnline
			}
			uc.owner = idx.ownerKey(uc.Name, uc.SubId)
			idx.clients = append(idx.clients, uc)
		}
	}

	links, err := s.links.allLinks()
	if err != nil {
		return nil, err
	}
	for _, kind := range []string{model.TunnelKindAwg, model.TunnelKindWg} {
		var peers []model.TunnelClient
		err := db.Where("server_id IN (?)", db.Model(&model.TunnelServer{}).Select("id").Where("kind = ?", kind)).
			Order("id asc").Find(&peers).Error
		if err != nil {
			return nil, err
		}
		row := idx.tunnelRow[kind]
		for _, p := range peers {
			uc := SubUserClient{
				Kind: kind, Protocol: tunnelProtocol(kind), Name: p.Email, Key: p.UUID, SubId: links[p.UUID], Enable: p.Enable,
				Up: p.Upload, Down: p.Download, AllTime: p.AllTime, TotalGB: p.TotalGB, ExpiryTime: p.ExpiryTime,
				LimitIp: p.LimitIp, TgId: p.TgId, Reset: p.Reset, LastOnline: p.LastOnline,
			}
			if row != nil {
				uc.InboundId, uc.InboundRemark = row.Id, row.Remark
			}
			uc.owner = idx.ownerKey(uc.Name, uc.SubId)
			idx.clients = append(idx.clients, uc)
		}
	}
	return idx, nil
}

// tunnelProtocol is the inbound protocol of a tunnel kind.
func tunnelProtocol(kind string) string {
	if kind == model.TunnelKindWg {
		return string(model.NativeWG)
	}
	return string(model.AmneziaWG)
}

// isReservedUserName reports whether a user may not be called name: the
// technical users' names, and anything a probe account could be named.
func isReservedUserName(name string) bool {
	return strings.EqualFold(name, model.SubUserRobot) || strings.EqualFold(name, model.SubUserMonitoring) || IsProbeAccount(name)
}

// isReservedSubId reports whether subId is a technical user's key.
func isReservedSubId(subId string) bool {
	return strings.EqualFold(subId, model.SubUserRobotKey) || strings.EqualFold(subId, model.SubUserMonitoringKey)
}

// takenNames is the set of user names in lower case.
func (idx *subUserIndex) takenNames() map[string]bool {
	taken := make(map[string]bool, len(idx.users))
	for _, u := range idx.users {
		taken[strings.ToLower(u.Name)] = true
	}
	return taken
}

// userByName finds a user by name, ignoring case.
func (idx *subUserIndex) userByName(name string) *model.SubUser {
	for _, u := range idx.users {
		if strings.EqualFold(u.Name, name) {
			return u
		}
	}
	return nil
}

// clientsOf lists the clients of the user under key, in index order.
func (idx *subUserIndex) clientsOf(key string) []SubUserClient {
	var out []SubUserClient
	for _, c := range idx.clients {
		if c.owner == key {
			out = append(out, c)
		}
	}
	return out
}

// sortedUsers lists the users: the technical ones first, then by name.
func (idx *subUserIndex) sortedUsers() []*model.SubUser {
	out := make([]*model.SubUser, 0, len(idx.users))
	for _, u := range idx.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if ti, tj := out[i].IsTechnical(), out[j].IsTechnical(); ti != tj {
			return ti
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// SubUserView is a user with its clients and their sums, as the bot and the
// users API show it.
type SubUserView struct {
	model.SubUser
	Technical bool            `json:"technical"`
	Enable    bool            `json:"enable"` // at least one client is enabled
	Clients   []SubUserClient `json:"clients"`
	Up        int64           `json:"up"`
	Down      int64           `json:"down"`
	AllTime   int64           `json:"allTime"`
	// Total is the sum of the clients' limits, 0 when any is unlimited;
	// ExpiryTime is the clients' common expiry, 0 when they differ. Both as
	// the subscription's Subscription-Userinfo counts them.
	Total      int64 `json:"total"`
	ExpiryTime int64 `json:"expiryTime"`
	// TgConflict: a client carries a Telegram id other than the user's
	// (#186) — «⚠️ Telegram: конфликт» until an admin resolves it.
	TgConflict bool `json:"tgConflict"`
}

// view builds the view of the user under key.
func (idx *subUserIndex) view(u *model.SubUser) *SubUserView {
	v := &SubUserView{SubUser: *u, Technical: u.IsTechnical(), Clients: idx.clientsOf(u.SubId), TgConflict: idx.tgConflict(u)}
	if v.Clients == nil {
		v.Clients = []SubUserClient{}
	}
	unlimited := false
	for i, c := range v.Clients {
		v.Enable = v.Enable || c.Enable
		v.Up += c.Up
		v.Down += c.Down
		v.AllTime += c.AllTime
		v.Total += c.TotalGB
		unlimited = unlimited || c.TotalGB == 0
		if i == 0 {
			v.ExpiryTime = c.ExpiryTime
		} else if c.ExpiryTime != v.ExpiryTime {
			v.ExpiryTime = 0
		}
	}
	if unlimited {
		v.Total = 0
	}
	return v
}

// Get returns the user under key: its subId, or a technical user's key.
func (s *SubUserService) Get(key string) (*SubUserView, error) {
	idx, err := s.synced()
	if err != nil {
		return nil, err
	}
	u := idx.users[key]
	if u == nil {
		return nil, common.NewErrorf("user with subId %q not found", key)
	}
	return idx.view(u), nil
}

// synced runs Sync and returns the index.
func (s *SubUserService) synced() (*subUserIndex, error) {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	return s.syncLocked()
}
