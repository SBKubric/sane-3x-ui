package subpage

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

// The total limit (#247): the traffic limit is a client's, one per protocol,
// and a subscription's total is the sum. The bot's «📄 My configs» and user
// card and the subscription page word it the same way, from the same rule:
//
//   - one client: its limit, «50 GB»;
//   - several with one limit: «100 GB (50 GB × 2 protocols)»;
//   - several with different limits: «70 GB (VLESS 50 GB + AmneziaWG 20 GB)»;
//   - any unlimited: «unlimited».
//
// The panel sends the page the clients behind the sum in TrafficPartsHeader,
// beside Subscription-Userinfo, and every hop passes it on; a page without
// it (an older panel or hop) shows the sum alone, as before.

// TrafficPartsHeader carries a subscription's TrafficParts from the panel
// through the chain: base64 of a JSON array of {"p","r","u","t"} — protocol,
// inbound remark, bytes used, limit in bytes (0 = unlimited). Apps ignore it.
const TrafficPartsHeader = "Sub-Traffic-Breakdown"

// The header's bounds. A hop's front is nginx, whose proxy buffer holds
// every header of an answer in 4 KB by default; past that it answers 502.
// A breakdown longer than MaxTrafficPartsHeader is not sent, and the page
// shows the sum alone.
const (
	MaxTrafficPartsHeader = 2048
	maxTrafficParts       = 32
	maxRemarkRunes        = 32
)

// TrafficPart is one client of a subscription: what it used of its limit.
type TrafficPart struct {
	// Protocol is the inbound protocol: vless, vmess, trojan, shadowsocks,
	// hysteria2, amneziawg, nativewg.
	Protocol string `json:"p"`
	// Remark is the inbound's remark, which tells two clients of one
	// protocol apart.
	Remark string `json:"r,omitempty"`
	Used   int64  `json:"u"`
	// Limit is in bytes, 0 for none.
	Limit int64 `json:"t"`
}

// protocolTitles are the inbound protocols as the total limit names them.
var protocolTitles = map[string]string{
	"vless":       "VLESS",
	"vmess":       "VMess",
	"trojan":      "Trojan",
	"shadowsocks": "Shadowsocks",
	"hysteria":    "Hysteria",
	"hysteria2":   "Hysteria2",
	"amneziawg":   "AmneziaWG",
	"nativewg":    "WireGuard",
	"wireguard":   "WireGuard",
}

// ProtocolTitle is an inbound protocol's name: «VLESS», «AmneziaWG».
func ProtocolTitle(protocol string) string {
	if title, ok := protocolTitles[strings.ToLower(protocol)]; ok {
		return title
	}
	return strings.ToUpper(protocol)
}

// PartLabels names the parts: the protocol, and the inbound's remark after a
// protocol two parts share, «VLESS (xhttp)».
func PartLabels(parts []TrafficPart) []string {
	count := map[string]int{}
	for _, p := range parts {
		count[ProtocolTitle(p.Protocol)]++
	}
	labels := make([]string, len(parts))
	for i, p := range parts {
		labels[i] = ProtocolTitle(p.Protocol)
		if count[labels[i]] > 1 && p.Remark != "" {
			labels[i] += " (" + p.Remark + ")"
		}
	}
	return labels
}

// QuotaUsed is the traffic the parts used together.
func QuotaUsed(parts []TrafficPart) int64 {
	var used int64
	for _, p := range parts {
		used += p.Used
	}
	return used
}

// QuotaTotal is the parts' total limit, 0 when any of them has none — the
// sum Subscription-Userinfo carries.
func QuotaTotal(parts []TrafficPart) int64 {
	var total int64
	for _, p := range parts {
		if p.Limit <= 0 {
			return 0
		}
		total += p.Limit
	}
	return total
}

// QuotaWords are a language's words for QuotaText.
type QuotaWords struct {
	// Size words a number of bytes: «50 GB».
	Size func(bytes int64) string
	// Unlimited is the total of parts any of which has no limit.
	Unlimited string
	// Protocols are the noun after a count, «× 2 protocols», by
	// PluralForm: one, few, many.
	Protocols [3]string
}

// QuotaText words the parts' total limit by the rule at the top of this
// file; "" for no parts.
func QuotaText(parts []TrafficPart, w QuotaWords) string {
	if len(parts) == 0 {
		return ""
	}
	total := QuotaTotal(parts)
	if total == 0 {
		return w.Unlimited
	}
	if len(parts) == 1 {
		return w.Size(total)
	}
	equal := true
	for _, p := range parts[1:] {
		equal = equal && p.Limit == parts[0].Limit
	}
	if equal {
		return w.Size(total) + " (" + w.Size(parts[0].Limit) + " × " + strconv.Itoa(len(parts)) + " " +
			w.Protocols[PluralForm(len(parts))] + ")"
	}
	labels := PartLabels(parts)
	terms := make([]string, len(parts))
	for i, p := range parts {
		terms[i] = labels[i] + " " + w.Size(p.Limit)
	}
	return w.Size(total) + " (" + strings.Join(terms, " + ") + ")"
}

// PluralForm picks the form of a noun after n by the Russian rule, the one
// language with all three: 0 for one (1, 21, 31), 1 for few (2-4, 22-24), 2
// for many (5-20, 25). A language with fewer forms repeats them; the count in
// QuotaText is always 2 or more, so English has «protocols» in all three.
func PluralForm(n int) int {
	switch {
	case n%10 == 1 && n%100 != 11:
		return 0
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return 1
	}
	return 2
}

// EncodeTrafficParts is the TrafficPartsHeader value of the parts; "" for
// none, and for a breakdown past the header's bounds. A remark is cut to
// maxRemarkRunes.
func EncodeTrafficParts(parts []TrafficPart) string {
	if len(parts) == 0 || len(parts) > maxTrafficParts {
		return ""
	}
	out := make([]TrafficPart, len(parts))
	for i, p := range parts {
		if r := []rune(p.Remark); len(r) > maxRemarkRunes {
			p.Remark = string(r[:maxRemarkRunes])
		}
		out[i] = p
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	value := base64.StdEncoding.EncodeToString(raw)
	if len(value) > MaxTrafficPartsHeader {
		return ""
	}
	return value
}

// ParseTrafficParts reads a TrafficPartsHeader value; nil for none and for
// one that does not parse or breaks the bounds.
func ParseTrafficParts(value string) []TrafficPart {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > MaxTrafficPartsHeader {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil
	}
	var parts []TrafficPart
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 || len(parts) > maxTrafficParts {
		return nil
	}
	for _, p := range parts {
		if p.Protocol == "" || p.Used < 0 || p.Limit < 0 {
			return nil
		}
	}
	return parts
}
