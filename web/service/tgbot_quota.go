package service

import (
	"html"

	"github.com/coinman-dev/3ax-ui/v2/subpage"
)

// The total limit in the bot (#247): «📊 Total limit: 100 GB (50 GB × 2
// protocols) · used 3.3 GB» on «📄 My configs» and on an admin's user card.
// The limit is a client's, one per protocol; the line says how the sum is
// made, by the rule the subscription page words it with too
// (subpage.QuotaText). The lists keep the sum alone («3.3/100 GB»).

// userTrafficParts are the user's clients as parts of the total limit: all
// of them, as SubUserView sums them.
func userTrafficParts(clients []SubUserClient) []subpage.TrafficPart {
	parts := make([]subpage.TrafficPart, 0, len(clients))
	for _, c := range clients {
		parts = append(parts, subpage.TrafficPart{Protocol: c.Protocol, Remark: c.InboundRemark, Used: c.Up + c.Down, Limit: c.TotalGB})
	}
	return parts
}

// usersGBText words bytes in GB as the bot's screens do: «3.3 GB».
func (t *Tgbot) usersGBText(bytes int64) string {
	return usersGB(bytes) + " " + t.I18nBot("tgbot.screen.gb")
}

// usersQuotaLine is the line of the user's total limit and the traffic
// used; "" for a user without clients.
func (t *Tgbot) usersQuotaLine(v *SubUserView) string {
	parts := userTrafficParts(v.Clients)
	if len(parts) == 0 {
		return ""
	}
	limit := subpage.QuotaText(parts, subpage.QuotaWords{
		Size:      t.usersGBText,
		Unlimited: t.I18nBot("tgbot.users.quotaUnlimited"),
		Protocols: [3]string{t.I18nBot("tgbot.users.protocolsOne"), t.I18nBot("tgbot.users.protocolsFew"),
			t.I18nBot("tgbot.users.protocolsMany")},
	})
	return t.I18nBot("tgbot.users.quota", "Limit=="+html.EscapeString(limit), "Used=="+t.usersGBText(v.Up+v.Down))
}

// usersPartLabels name the user's clients as the total limit does: the
// protocol, and the inbound after a protocol two of them share, «VLESS
// (xhttp)».
func usersPartLabels(clients []SubUserClient) []string {
	return subpage.PartLabels(userTrafficParts(clients))
}
