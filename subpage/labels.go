package subpage

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// A protocol label names what a configuration needs from a client app, such
// as "VLESS + XHTTP" or "AWG 3" (#235). The page puts one on every
// configuration and the same labels on the app cards, so a user can match the
// two: an app whose card lacks the label may import the configuration and
// still pass no traffic. LinkLabel and TunnelLabel are the only producers;
// an app's labels are checked against the same set (KnownLabel).

// protocolNames are the xray share-link schemes the page labels.
var protocolNames = map[string]string{
	"vless":     "VLESS",
	"vmess":     "VMess",
	"trojan":    "Trojan",
	"ss":        "Shadowsocks",
	"hysteria2": "Hysteria2",
	"hy2":       "Hysteria2",
}

// transportNames are the transports a VLESS, VMess or Trojan label names,
// keyed by the link's type parameter. raw is xray's newer name for tcp and
// splithttp the older one for xhttp: the same transport to a client.
var transportNames = map[string]string{
	"tcp":         "TCP",
	"raw":         "TCP",
	"xhttp":       "XHTTP",
	"splithttp":   "XHTTP",
	"ws":          "WS",
	"grpc":        "gRPC",
	"httpupgrade": "HTTPUpgrade",
	"kcp":         "mKCP",
	"quic":        "QUIC",
	"http":        "HTTP/2",
	"h2":          "HTTP/2",
}

// The tunnel labels: AmneziaWG by generation, and plain WireGuard.
const (
	LabelAWG1      = "AWG 1"
	LabelAWG2      = "AWG 2"
	LabelAWG3      = "AWG 3"
	LabelWireGuard = "WireGuard"
)

// LinkLabel is the protocol label of an xray share link: the protocol and,
// for VLESS, VMess and Trojan, the transport ("VLESS + XHTTP"). "" for a
// link the page does not know.
func LinkLabel(link string) string {
	scheme, rest, ok := strings.Cut(link, "://")
	if !ok {
		return ""
	}
	scheme = strings.ToLower(scheme)
	protocol, ok := protocolNames[scheme]
	if !ok {
		return ""
	}
	if scheme != "vless" && scheme != "vmess" && scheme != "trojan" {
		return protocol
	}
	network := ""
	if scheme == "vmess" {
		var v struct {
			Net string `json:"net"`
		}
		raw, err := decodeBase64(strings.SplitN(rest, "#", 2)[0])
		if err != nil || json.Unmarshal(raw, &v) != nil {
			return protocol
		}
		network = v.Net
	} else {
		rest, _, _ = strings.Cut(rest, "#")
		if _, query, found := strings.Cut(rest, "?"); found {
			if values, err := url.ParseQuery(query); err == nil {
				network = values.Get("type")
			}
		}
	}
	return protocol + " + " + transportName(network)
}

func transportName(network string) string {
	network = strings.ToLower(strings.TrimSpace(network))
	if network == "" {
		network = "tcp"
	}
	if name, ok := transportNames[network]; ok {
		return name
	}
	return strings.ToUpper(network)
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(s); err == nil {
			return raw, nil
		}
	}
	return nil, base64.CorruptInputError(0)
}

// awg3Keys are the [Interface] parameters AmneziaWG 3.0 and 3.1 added
// (tunnel/obfuscation.go): a config that names one is a 3.x config, which an
// app older than 3.0 refuses or misreads.
var awg3Keys = []string{
	"headerprotectionkey", "contentpaddingaddition", "rekeyaftertime", "rekeytimeout",
	"rejectaftertime", "keepalivetimeout", "maxhandshakeattempts", "randomtrailers", "disablecookies",
}

// awg2Keys are the parameters AmneziaWG 2.0 (and the 1.5 signature packets)
// added; a range in H1-H4 is 2.0 as well.
var awg2Keys = []string{"s3", "s4", "i1", "i2", "i3", "i4", "i5"}

// TunnelLabel is the protocol label of a tunnel config: WireGuard, or
// AmneziaWG by the newest generation its [Interface] uses.
func TunnelLabel(kind, conf string) string {
	switch kind {
	case "wg":
		return LabelWireGuard
	case "awg":
	default:
		return ""
	}
	params := interfaceParams(conf)
	for _, key := range awg3Keys {
		if _, ok := params[key]; ok {
			return LabelAWG3
		}
	}
	for _, key := range awg2Keys {
		if _, ok := params[key]; ok {
			return LabelAWG2
		}
	}
	for _, key := range []string{"h1", "h2", "h3", "h4"} {
		if strings.Contains(params[key], "-") {
			return LabelAWG2
		}
	}
	return LabelAWG1
}

// interfaceParams are the key = value lines of a config's [Interface]
// section, keys lowercased.
func interfaceParams(conf string) map[string]string {
	params := map[string]string{}
	inInterface := false
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inInterface = strings.EqualFold(line, "[Interface]")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if inInterface && ok {
			params[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	return params
}

// knownLabels is every label LinkLabel and TunnelLabel can give a
// configuration of a known protocol and transport, lowercased to its
// spelling.
var knownLabels = func() map[string]string {
	labels := map[string]string{}
	add := func(label string) { labels[strings.ToLower(label)] = label }
	for _, protocol := range []string{"VLESS", "VMess", "Trojan"} {
		for _, transport := range transportNames {
			add(protocol + " + " + transport)
		}
	}
	for _, label := range []string{"Shadowsocks", "Hysteria2", LabelAWG1, LabelAWG2, LabelAWG3, LabelWireGuard} {
		add(label)
	}
	return labels
}()

// KnownLabel reports whether a configuration can carry this label.
func KnownLabel(label string) bool {
	_, ok := knownLabels[strings.ToLower(strings.TrimSpace(label))]
	return ok
}

// canonicalLabel is a known label in the page's own spelling.
func canonicalLabel(label string) (string, bool) {
	canonical, ok := knownLabels[strings.ToLower(strings.TrimSpace(label))]
	return canonical, ok
}

// LinkName is the name a link goes by on the page: the remark of a VMess
// link, the fragment of any other, else its position ("#3").
func LinkName(link string, idx int) string {
	if rest, ok := strings.CutPrefix(link, "vmess://"); ok {
		var v struct {
			PS string `json:"ps"`
		}
		if raw, err := decodeBase64(strings.SplitN(rest, "#", 2)[0]); err == nil && json.Unmarshal(raw, &v) == nil && v.PS != "" {
			return v.PS
		}
	}
	if _, fragment, ok := strings.Cut(link, "#"); ok && fragment != "" {
		if name, err := url.PathUnescape(fragment); err == nil {
			return name
		}
		return fragment
	}
	return "#" + strconv.Itoa(idx+1)
}
