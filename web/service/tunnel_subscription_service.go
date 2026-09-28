package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"

	"gorm.io/gorm/clause"
)

// TunnelSubscriptionService keeps the link between a tunnel client and its
// subscription (docs/spec/tunnel-subscription.md §2–§3): an xray client
// carries its subId in its own settings, a tunnel client gets it from the
// tunnel_client_subs table. The /tun route reads a subscription's configs
// through ClientsBySubId.
type TunnelSubscriptionService struct{}

// TunnelSubEntry is one tunnel client of a subscription with its .conf text,
// the host override already applied to its Endpoint.
type TunnelSubEntry struct {
	Kind   string
	Client model.TunnelClient
	Conf   string
}

// Set links the tunnel client to subId, replacing any link it had. An empty
// subId removes the link.
func (s *TunnelSubscriptionService) Set(clientUUID, kind, subId string) error {
	if subId == "" {
		return s.Clear(clientUUID)
	}
	defer InvalidateTunnelSubCache()
	link := &model.TunnelClientSub{ClientUUID: clientUUID, Kind: kind, SubId: subId}
	return database.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_uuid"}},
		DoUpdates: clause.AssignmentColumns([]string{"kind", "sub_id", "updated_at"}),
	}).Create(link).Error
}

// Clear removes the tunnel client's link, if it has one.
func (s *TunnelSubscriptionService) Clear(clientUUID string) error {
	defer InvalidateTunnelSubCache()
	return database.GetDB().Where("client_uuid = ?", clientUUID).Delete(&model.TunnelClientSub{}).Error
}

// SubIdsByUUIDs returns the subId of every linked client among uuids; an
// unlinked client is absent from the map.
func (s *TunnelSubscriptionService) SubIdsByUUIDs(uuids []string) (map[string]string, error) {
	out := make(map[string]string, len(uuids))
	if len(uuids) == 0 {
		return out, nil
	}
	var links []model.TunnelClientSub
	if err := database.GetDB().Where("client_uuid IN ?", uuids).Find(&links).Error; err != nil {
		return nil, err
	}
	for _, l := range links {
		out[l.ClientUUID] = l.SubId
	}
	return out, nil
}

// allLinks returns every link keyed by client uuid.
func (s *TunnelSubscriptionService) allLinks() (map[string]string, error) {
	var links []model.TunnelClientSub
	if err := database.GetDB().Find(&links).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(links))
	for _, l := range links {
		out[l.ClientUUID] = l.SubId
	}
	return out, nil
}

// ClientsBySubId returns the tunnel clients linked to subId, AmneziaWG before
// WireGuard and oldest first, each with its .conf. An unknown subId, or one
// with no tunnel clients, is an empty list.
//
// The route that calls it is public, so the answer is cached for
// tunnelSubCacheTTL (docs/spec/tunnel-subscription.md §3).
func (s *TunnelSubscriptionService) ClientsBySubId(subId string) ([]TunnelSubEntry, error) {
	if cached, ok := cachedTunnelSub(subId); ok {
		return cached, nil
	}
	entries, err := s.clientsBySubId(subId)
	if err != nil {
		return nil, err
	}
	storeTunnelSub(subId, entries)
	return entries, nil
}

func (s *TunnelSubscriptionService) clientsBySubId(subId string) ([]TunnelSubEntry, error) {
	// The join keeps an orphaned link out even if foreign keys were ever off,
	// and the kind comes from the client's server rather than the link.
	type row struct {
		model.TunnelClient
		ServerKind string
	}
	var rows []row
	err := database.GetDB().Table("tunnel_client_subs").
		Select("tunnel_clients.*, tunnel_servers.kind AS server_kind").
		Joins("JOIN tunnel_clients ON tunnel_clients.uuid = tunnel_client_subs.client_uuid").
		Joins("JOIN tunnel_servers ON tunnel_servers.id = tunnel_clients.server_id").
		Where("tunnel_client_subs.sub_id = ?", subId).
		Order("tunnel_servers.kind, tunnel_clients.id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	entries := make([]TunnelSubEntry, 0, len(rows))
	for _, r := range rows {
		var conf string
		switch r.ServerKind {
		case model.TunnelKindAwg:
			conf, err = (&AwgService{}).GetClientConfigByUUID(r.UUID)
		case model.TunnelKindWg:
			conf, err = (&WgService{}).GetClientConfigByUUID(r.UUID)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, TunnelSubEntry{Kind: r.ServerKind, Client: r.TunnelClient, Conf: conf})
	}
	return entries, nil
}

// Traffic adds the entries up the way /sub adds a subscription's xray clients
// (sub/subService.go buildSubs): upload and download are summed; the limit is
// summed too, but one unlimited client makes it unlimited; the expiry is the
// one every client shares, else none; Enable is whether any client is on.
func (s *TunnelSubscriptionService) Traffic(entries []TunnelSubEntry) xray.ClientTraffic {
	var traffic xray.ClientTraffic
	for i, e := range entries {
		c := e.Client
		if c.Enable {
			traffic.Enable = true
		}
		if c.LastOnline > traffic.LastOnline {
			traffic.LastOnline = c.LastOnline
		}
		if i == 0 {
			traffic.Up, traffic.Down, traffic.Total = c.Upload, c.Download, c.TotalGB
			if c.ExpiryTime > 0 {
				traffic.ExpiryTime = c.ExpiryTime
			}
			continue
		}
		traffic.Up += c.Upload
		traffic.Down += c.Download
		if traffic.Total == 0 || c.TotalGB == 0 {
			traffic.Total = 0
		} else {
			traffic.Total += c.TotalGB
		}
		if c.ExpiryTime != traffic.ExpiryTime {
			traffic.ExpiryTime = 0
		}
	}
	return traffic
}

// Userinfo is the Subscription-Userinfo header of the entries, in the format
// /sub sends, and whether any of them is enabled.
func (s *TunnelSubscriptionService) Userinfo(entries []TunnelSubEntry) (header string, enable bool) {
	t := s.Traffic(entries)
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", t.Up, t.Down, t.Total, t.ExpiryTime/1000), t.Enable
}

// The tunnel subscription cache, after sub/inbound_cache.go but without its
// datagen generation, which tunnel_clients writes do not bump. Set and Clear
// drop it, and so do the AWG/WG client handlers; a change through any other
// path (toggle, traffic reset, delete) shows within the TTL, as with /sub.
const tunnelSubCacheMaxEntries = 1024

// var, not const, so tests can shorten it.
var tunnelSubCacheTTL = 10 * time.Second

type tunnelSubCacheEntry struct {
	entries   []TunnelSubEntry
	expiresAt time.Time
}

var (
	tunnelSubCacheMu sync.Mutex
	tunnelSubCache   = make(map[string]tunnelSubCacheEntry)
)

// InvalidateTunnelSubCache drops every cached subscription.
func InvalidateTunnelSubCache() {
	tunnelSubCacheMu.Lock()
	defer tunnelSubCacheMu.Unlock()
	tunnelSubCache = make(map[string]tunnelSubCacheEntry)
}

// cachedTunnelSub returns a copy of the cached entries, if still fresh.
func cachedTunnelSub(subId string) ([]TunnelSubEntry, bool) {
	tunnelSubCacheMu.Lock()
	defer tunnelSubCacheMu.Unlock()
	entry, ok := tunnelSubCache[subId]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(tunnelSubCache, subId)
		return nil, false
	}
	return append([]TunnelSubEntry(nil), entry.entries...), true
}

// storeTunnelSub caches a copy of entries. The map is bounded: a flood of
// unknown subIds drops the expired entries first, then everything.
func storeTunnelSub(subId string, entries []TunnelSubEntry) {
	now := time.Now()
	tunnelSubCacheMu.Lock()
	defer tunnelSubCacheMu.Unlock()
	if len(tunnelSubCache) >= tunnelSubCacheMaxEntries {
		for key, e := range tunnelSubCache {
			if now.After(e.expiresAt) {
				delete(tunnelSubCache, key)
			}
		}
		if len(tunnelSubCache) >= tunnelSubCacheMaxEntries {
			tunnelSubCache = make(map[string]tunnelSubCacheEntry)
		}
	}
	tunnelSubCache[subId] = tunnelSubCacheEntry{
		entries:   append([]TunnelSubEntry(nil), entries...),
		expiresAt: now.Add(tunnelSubCacheTTL),
	}
}
