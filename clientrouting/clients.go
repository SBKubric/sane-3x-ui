package clientrouting

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Client is an app that takes routing from the subscription's routing
// header, each in its own format.
type Client int

const (
	// None is every other app: v2rayNG and the rest read no routing header
	// (2dust/v2rayNG#4280) and get none.
	None Client = iota
	// Happ takes a routing profile link, happ://routing/onadd/<base64>
	// (happ.su/main/dev-docs/routing).
	Happ
	// V2RayTun takes its routing object in base64, as the app exports it
	// (docs.v2raytun.com/overview/supported-headers).
	V2RayTun
	// Incy reads Happ's profile from the same header, as base64 or as a
	// link of any scheme (docs.incy.cc/en/routing): it gets Happ's link.
	Incy
)

// The apps' User-Agents: Happ sends Happ/<version> (Happ/3.13.0, and
// Happ/<version>/<os>/<id> seen in the wild), v2RayTun v2raytun/<version>
// (<os>), v2raytun/android; the case varies. A whole word anywhere in the
// User-Agent, so a suffix or a prefix the app adds later still matches.
// Incy sends INCY/<version>/<platform>.
var (
	happUA     = regexp.MustCompile(`(?i)\bhapp\b`)
	v2rayTunUA = regexp.MustCompile(`(?i)\bv2raytun\b`)
	incyUA     = regexp.MustCompile(`(?i)^incy/`)
)

// ClientOf tells the app by its User-Agent.
func ClientOf(userAgent string) Client {
	switch {
	case happUA.MatchString(userAgent):
		return Happ
	case v2rayTunUA.MatchString(userAgent):
		return V2RayTun
	case incyUA.MatchString(userAgent):
		return Incy
	}
	return None
}

// Header is the value of the routing header for the app with this
// User-Agent, "" for an app that takes none or with no routing. lastUpdated
// is when the routing last changed, in Unix seconds: Happ downloads its geo
// files again when it grows, so it must not move between two fetches of the
// same routing.
func Header(userAgent string, r *Routing, lastUpdated int64) string {
	if r == nil {
		return ""
	}
	switch ClientOf(userAgent) {
	case Happ, Incy:
		return HappLink(r, lastUpdated)
	case V2RayTun:
		return base64.StdEncoding.EncodeToString(V2RayTunObject(r))
	}
	return ""
}

// Happ's defaults for the geo files (happ.su/main/dev-docs/routing, the
// profile generator routing.happ.su): named outright, so a profile never
// leans on how Happ fills an empty field.
const (
	happDefaultGeoip   = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat"
	happDefaultGeosite = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat"
)

// happRouteOrder checks the VPN's sites before the blocked and both before
// the direct ones, as V2RayTunObject orders its rules: domain:acomics.ru
// goes through the VPN although geosite:category-ru holds it. One of the six
// orders of routing.happ.su, whose default is block-proxy-direct.
const happRouteOrder = "proxy-block-direct"

// happProfile is Happ's routing profile, with the field names of Happ's own
// profile generator (routing.happ.su) and of the JSON examples of its
// documentation; flags are the strings "true"/"false", as Happ writes them.
// The DNS fields are left to Happ's defaults.
type happProfile struct {
	Name           string   `json:"Name"`
	GlobalProxy    string   `json:"GlobalProxy"`
	RouteOrder     string   `json:"RouteOrder"`
	DomainStrategy string   `json:"DomainStrategy"`
	Geositeurl     string   `json:"Geositeurl"`
	Geoipurl       string   `json:"Geoipurl"`
	LastUpdated    string   `json:"LastUpdated"`
	UseChunkFiles  string   `json:"UseChunkFiles"`
	DirectSites    []string `json:"DirectSites"`
	DirectIp       []string `json:"DirectIp"`
	ProxySites     []string `json:"ProxySites"`
	ProxyIp        []string `json:"ProxyIp"`
	BlockSites     []string `json:"BlockSites"`
	BlockIp        []string `json:"BlockIp"`
}

// HappProfile is the routing as Happ's profile, JSON. Everything no rule
// takes goes through the VPN (GlobalProxy); the geo files are cut down to
// the categories the profile names (UseChunkFiles: Happ's core on iOS has
// 50 MB). Protocol rules have no place in it and are left out.
func HappProfile(r *Routing, lastUpdated int64) []byte {
	p := happProfile{
		Name:           r.Name,
		GlobalProxy:    "true",
		RouteOrder:     happRouteOrder,
		DomainStrategy: r.DomainStrategy,
		Geositeurl:     orDefault(r.GeositeURL, happDefaultGeosite),
		Geoipurl:       orDefault(r.GeoipURL, happDefaultGeoip),
		UseChunkFiles:  "true",
		DirectSites:    orEmpty(r.DirectSites),
		DirectIp:       orEmpty(r.DirectIP),
		ProxySites:     orEmpty(r.ProxySites),
		ProxyIp:        orEmpty(r.ProxyIP),
		BlockSites:     orEmpty(r.BlockSites),
		BlockIp:        orEmpty(r.BlockIP),
	}
	if lastUpdated > 0 {
		p.LastUpdated = strconv.FormatInt(lastUpdated, 10)
	}
	return compactJSON(p)
}

// HappLink is the profile as the link Happ's routing header carries: onadd
// adds it and turns it on; a profile of the same Name is replaced (by Incy
// only for a greater LastUpdated). Incy takes the same link.
func HappLink(r *Routing, lastUpdated int64) string {
	return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(HappProfile(r, lastUpdated))
}

// v2RayTun's outbound tags: its documentation's example and the routing
// people import into it name them proxy, direct and block (the app's own
// export of 2024 in v2RayTun/sub-import-routing has freedom for direct —
// the device check of #263 settles which the app keeps).
const (
	v2rayTunProxy  = "proxy"
	v2rayTunDirect = "direct"
	v2rayTunBlock  = "block"
)

// v2rayTunRouting is v2RayTun's routing object, the shape of its export
// (v2RayTun/sub-import-routing, docs.v2raytun.com).
type v2rayTunRouting struct {
	Name           string         `json:"name"`
	DomainStrategy string         `json:"domainStrategy"`
	DomainMatcher  string         `json:"domainMatcher"`
	Balancers      []any          `json:"balancers"`
	ID             string         `json:"id"`
	Rules          []v2rayTunRule `json:"rules"`
}

// v2rayTunRule is an Xray field rule with the app's own keys: id (the
// documentation's example) and __id__ (the export) carry the same UUID,
// __name__ the name the app shows.
type v2rayTunRule struct {
	Type          string   `json:"type"`
	OutboundTag   string   `json:"outboundTag"`
	Domain        []string `json:"domain,omitempty"`
	DomainMatcher string   `json:"domainMatcher,omitempty"`
	IP            []string `json:"ip,omitempty"`
	Protocol      []string `json:"protocol,omitempty"`
	ID            string   `json:"id"`
	IDExport      string   `json:"__id__"`
	Name          string   `json:"__name__"`
}

// idSpace is the UUID namespace of the ids in v2RayTun's object: the same
// routing gives the same ids on every fetch, so the app sees one routing
// and the same rules, not new ones.
var idSpace = uuid.MustParse("6f0c3b0e-2f7c-5f4e-9d1a-263c11e2a263")

func ruleID(parts ...string) string {
	return strings.ToUpper(uuid.NewSHA1(idSpace, []byte(strings.Join(parts, "\x00"))).String())
}

// V2RayTunObject is the routing as v2RayTun's routing object, JSON. Each
// list is a rule of its own — sites, addresses and protocols — because an
// Xray rule matches only when all its conditions do: a rule with domain and
// ip both never takes a request by domain under AsIs. The rules run proxy,
// block, direct, so the VPN's sites win over a direct category. The
// object's id comes from the name alone: a changed routing replaces the
// same one.
func V2RayTunObject(r *Routing) []byte {
	o := v2rayTunRouting{
		Name:           r.Name,
		DomainStrategy: r.DomainStrategy,
		DomainMatcher:  "hybrid",
		Balancers:      []any{},
		ID:             ruleID("routing", r.Name),
		Rules:          []v2rayTunRule{},
	}
	add := func(tag, title, kind string, values []string) {
		if len(values) == 0 {
			return
		}
		rule := v2rayTunRule{Type: "field", OutboundTag: tag, Name: title}
		switch kind {
		case "domain":
			rule.Domain, rule.DomainMatcher = values, "hybrid"
		case "ip":
			rule.IP = values
		case "protocol":
			rule.Protocol = values
		}
		id := ruleID(append([]string{"rule", tag, kind}, values...)...)
		rule.ID, rule.IDExport = id, id
		o.Rules = append(o.Rules, rule)
	}
	add(v2rayTunProxy, "VPN: sites", "domain", r.ProxySites)
	add(v2rayTunProxy, "VPN: IP", "ip", r.ProxyIP)
	add(v2rayTunBlock, "Block: sites", "domain", r.BlockSites)
	add(v2rayTunBlock, "Block: IP", "ip", r.BlockIP)
	add(v2rayTunDirect, "Direct: sites", "domain", r.DirectSites)
	add(v2rayTunDirect, "Direct: IP", "ip", r.DirectIP)
	add(v2rayTunDirect, "Direct: protocols", "protocol", r.DirectProtocols)
	return compactJSON(o)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
