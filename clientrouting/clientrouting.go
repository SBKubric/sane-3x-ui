// Package clientrouting is the routing a subscription hands its client apps
// (#263): which sites and addresses the device reaches directly, which
// through the VPN and which not at all. The owner writes it once, in a
// neutral form (setting subClientRouting); the apps that read routing from
// a subscription header get it in their own format, chosen by User-Agent —
// Happ and Incy a routing profile link, v2RayTun its routing object. Other apps get
// nothing: the header is all there is, the subscription body stays the same
// for every app.
package clientrouting

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Routing is the neutral routing of setting subClientRouting. Sites take
// Xray's domain forms (geosite:, domain:, full:, regexp:, keyword:, ext: or
// a bare keyword), addresses Xray's IP forms (geoip:, ext:, an IP or a
// CIDR). Rules are checked proxy first, then block, then direct, so a site
// sent through the VPN wins over a category that would send it directly.
type Routing struct {
	Name           string `json:"name"`
	DomainStrategy string `json:"domainStrategy"`

	DirectSites []string `json:"directSites"`
	DirectIP    []string `json:"directIp"`
	// DirectProtocols are sniffed protocols (bittorrent, …): v2RayTun only,
	// Happ's profile has no protocol rules.
	DirectProtocols []string `json:"directProtocols"`
	ProxySites      []string `json:"proxySites"`
	ProxyIP         []string `json:"proxyIp"`
	BlockSites      []string `json:"blockSites"`
	BlockIP         []string `json:"blockIp"`

	// GeositeURL and GeoipURL are the geo files Happ downloads for the
	// profile; "" for Happ's own default (Loyalsoldier's v2ray-rules-dat).
	// v2RayTun has its geo files built in.
	GeositeURL string `json:"geositeUrl"`
	GeoipURL   string `json:"geoipUrl"`
}

// DomainStrategies are the domain strategies Xray's routing knows.
var DomainStrategies = []string{"AsIs", "IPIfNonMatch", "IPOnDemand"}

// protocols are the protocols Xray's sniffing tells apart.
var protocols = []string{"http", "tls", "quic", "bittorrent"}

// sitePrefixes are Xray's domain rule prefixes.
var sitePrefixes = []string{"geosite", "domain", "full", "regexp", "keyword", "ext"}

const (
	maxNameLen = 64
	maxEntries = 1000
	// MaxHeaderBytes bounds the routing header of either app. Every nginx
	// between the app and the sub server — the front of the hop or the
	// panel, the subscription showcase — reads the response headers into
	// proxy_buffer_size, one memory page (4 KB) by default, and answers 502
	// to headers that do not fit; the other subscription headers take about
	// 1 KB. The rule set of #263 makes 0.9 KB for Happ and 1.4 KB for
	// v2RayTun.
	MaxHeaderBytes = 2800
)

// Parse reads setting subClientRouting: nil for "" (routing off), an error
// for a value the apps could not use.
func Parse(value string) (*Routing, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	var r Routing
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("not a routing object: %w", err)
	}
	if dec.More() {
		return nil, errors.New("not a routing object: data after the object")
	}
	if err := r.normalize(); err != nil {
		return nil, err
	}
	if err := r.checkHeaderSize(); err != nil {
		return nil, err
	}
	return &r, nil
}

// checkHeaderSize refuses a routing whose header would not pass the nginx
// in front of the subscription (MaxHeaderBytes).
func (r *Routing) checkHeaderSize() error {
	for _, h := range []struct{ app, value string }{
		{"Happ", HappLink(r, 1<<40)},
		{"v2RayTun", base64.StdEncoding.EncodeToString(V2RayTunObject(r))},
	} {
		if len(h.value) > MaxHeaderBytes {
			return fmt.Errorf("the routing header for %s would be %d bytes, more than %d: nginx in front of the subscription (proxy_buffer_size, 4 KB by default) would answer 502; use fewer or shorter rules, geosite categories instead of lists of domains", h.app, len(h.value), MaxHeaderBytes)
		}
	}
	return nil
}

// NormalizeSetting checks a subClientRouting value and returns it as it is
// stored: the object re-indented with every field, or "" for none.
func NormalizeSetting(value string) (string, error) {
	r, err := Parse(value)
	if err != nil || r == nil {
		return "", err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// normalize trims every value, puts [] for a missing list and checks it all.
func (r *Routing) normalize() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("name is empty")
	}
	if utf8.RuneCountInString(r.Name) > maxNameLen {
		return fmt.Errorf("name is longer than %d characters", maxNameLen)
	}
	if strings.IndexFunc(r.Name, unicode.IsControl) >= 0 {
		return errors.New("name has control characters")
	}
	r.DomainStrategy = strings.TrimSpace(r.DomainStrategy)
	if !slices.Contains(DomainStrategies, r.DomainStrategy) {
		return fmt.Errorf("domainStrategy %q is none of %s", r.DomainStrategy, strings.Join(DomainStrategies, ", "))
	}
	lists := []struct {
		name  string
		list  *[]string
		check func(string) error
	}{
		{"directSites", &r.DirectSites, checkSite},
		{"directIp", &r.DirectIP, checkIP},
		{"directProtocols", &r.DirectProtocols, checkProtocol},
		{"proxySites", &r.ProxySites, checkSite},
		{"proxyIp", &r.ProxyIP, checkIP},
		{"blockSites", &r.BlockSites, checkSite},
		{"blockIp", &r.BlockIP, checkIP},
	}
	for _, l := range lists {
		out := make([]string, 0, len(*l.list))
		for _, v := range *l.list {
			v = strings.TrimSpace(v)
			if err := l.check(v); err != nil {
				return fmt.Errorf("%s: %w", l.name, err)
			}
			out = append(out, v)
		}
		if len(out) > maxEntries {
			return fmt.Errorf("%s: more than %d entries", l.name, maxEntries)
		}
		*l.list = out
	}
	for _, u := range []struct {
		name  string
		value *string
	}{{"geositeUrl", &r.GeositeURL}, {"geoipUrl", &r.GeoipURL}} {
		*u.value = strings.TrimSpace(*u.value)
		if err := checkURL(*u.value); err != nil {
			return fmt.Errorf("%s: %w", u.name, err)
		}
	}
	return nil
}

func checkToken(v string) error {
	if v == "" {
		return errors.New("an empty entry")
	}
	if strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == ',' }) >= 0 {
		return fmt.Errorf("%q has spaces, commas or control characters", v)
	}
	return nil
}

func checkSite(v string) error {
	if err := checkToken(v); err != nil {
		return err
	}
	prefix, rest, found := strings.Cut(v, ":")
	if !found {
		return nil // a bare keyword
	}
	if !slices.Contains(sitePrefixes, prefix) {
		return fmt.Errorf("%q: unknown prefix %q (%s)", v, prefix, strings.Join(sitePrefixes, ", "))
	}
	if rest == "" {
		return fmt.Errorf("%q: nothing after the prefix", v)
	}
	return nil
}

func checkIP(v string) error {
	if err := checkToken(v); err != nil {
		return err
	}
	for _, prefix := range []string{"geoip:", "ext:"} {
		if rest, ok := strings.CutPrefix(v, prefix); ok {
			if rest == "" {
				return fmt.Errorf("%q: nothing after the prefix", v)
			}
			return nil
		}
	}
	if _, err := netip.ParsePrefix(v); err == nil {
		return nil
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return nil
	}
	return fmt.Errorf("%q is neither geoip:, ext:, an IP nor a CIDR", v)
}

func checkProtocol(v string) error {
	if !slices.Contains(protocols, v) {
		return fmt.Errorf("%q is none of %s", v, strings.Join(protocols, ", "))
	}
	return nil
}

func checkURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) address", v)
	}
	return nil
}

// compactJSON is v marshalled without HTML escaping: a regexp: site keeps
// its < and > as they are.
func compactJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // the types here always marshal
	return bytes.TrimRight(b.Bytes(), "\n")
}
