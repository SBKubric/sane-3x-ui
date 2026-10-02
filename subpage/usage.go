package subpage

import (
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// Usage is the subscription's traffic and term, as the panel counts them and
// a hop reads them from Subscription-Userinfo.
type Usage struct {
	// Known is false for a page with nothing to say about usage: a hop whose
	// next hop sent no Subscription-Userinfo.
	Known bool
	Up    int64
	Down  int64
	// Total is the traffic quota in bytes, 0 for none.
	Total int64
	// Expire is the end of the term in Unix seconds, 0 for none; negative,
	// it is a term that starts at the first connection, -Expire seconds long.
	Expire int64
	// Disabled is a subscription the panel switched off.
	Disabled bool
	// Parts are the clients behind the sum, from TrafficPartsHeader (#247);
	// nil when the panel or a hop on the way did not send them. With them,
	// the traffic used and the quota are theirs, and the page words the
	// total limit and lists each.
	Parts []TrafficPart
}

// ParseUserinfo reads a Subscription-Userinfo header
// ("upload=1; download=2; total=3; expire=4").
func ParseUserinfo(header string) Usage {
	if strings.TrimSpace(header) == "" {
		return Usage{}
	}
	u := Usage{Known: true}
	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		switch strings.TrimSpace(key) {
		case "upload":
			u.Up = n
		case "download":
			u.Down = n
		case "total":
			u.Total = n
		case "expire":
			u.Expire = n
		}
	}
	return u
}

// usageView is Usage as the page prints it.
type usageView struct {
	Used     string
	Total    string
	Remained string
	// Expire is the date the term ends; ExpireDays the length of a term that
	// starts at the first connection. Both empty: no term.
	Expire     string
	ExpireDays int
	Active     bool
	// Quota words the total limit, Parts list the clients behind it; both
	// empty without Usage.Parts. Render fills them, in the visitor's
	// language.
	Quota string
	Parts []partView
}

// partView is one client of the total limit as the page lists it.
type partView struct {
	Label string
	Used  string
	Limit string
}

func (u Usage) view(now time.Time) *usageView {
	if !u.Known {
		return nil
	}
	used, total := u.Up+u.Down, u.Total
	if len(u.Parts) > 0 {
		used, total = QuotaUsed(u.Parts), QuotaTotal(u.Parts)
	}
	v := &usageView{Used: common.FormatTraffic(used), Total: "∞", Active: !u.Disabled}
	if total > 0 {
		v.Total = common.FormatTraffic(total)
		v.Remained = common.FormatTraffic(max(total-used, 0))
		if used >= total {
			v.Active = false
		}
	}
	switch {
	case u.Expire > 0:
		end := time.Unix(u.Expire, 0).UTC()
		v.Expire = end.Format("2006-01-02")
		if end.Before(now) {
			v.Active = false
		}
	case u.Expire < 0:
		v.ExpireDays = int((-u.Expire + 86399) / 86400)
	}
	return v
}

// quota fills the total limit and its parts into v, in tr's language.
func (u Usage) quota(v *usageView, tr translator) {
	if v == nil || len(u.Parts) == 0 {
		return
	}
	v.Quota = QuotaText(u.Parts, QuotaWords{
		Size:      common.FormatTraffic,
		Unlimited: tr.T("page.quotaUnlimited", nil),
		Protocols: [3]string{tr.T("page.protocolsOne", nil), tr.T("page.protocolsFew", nil), tr.T("page.protocolsMany", nil)},
	})
	for i, label := range PartLabels(u.Parts) {
		limit := "∞"
		if p := u.Parts[i]; p.Limit > 0 {
			limit = common.FormatTraffic(p.Limit)
		}
		v.Parts = append(v.Parts, partView{Label: label, Used: common.FormatTraffic(u.Parts[i].Used), Limit: limit})
	}
}
