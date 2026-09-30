package service

import (
	"strconv"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// telegramClients are the clients that carry a Telegram id (#201): the xray
// clients of every inbound by email, the AWG/WG clients as their rows.
//
// The xray clients are read out of the parsed settings JSON by SQLite
// (json_each over settings.clients), so neither the spacing a serializer, an
// import or a hand edit left nor the type of tgId matters: the panel's form
// stores it as a number, sometimes as a string ("777", or "" for none). One
// query for all inbounds; malformed settings and non-object clients are
// skipped rather than failing it.
type telegramClients struct {
	xrayEmails []string
	tunnels    []model.TunnelClient
}

const telegramXrayClientsSQL = `
SELECT DISTINCT COALESCE(json_extract(c.value, '$.email'), '')
FROM inbounds,
	json_each(CASE WHEN json_valid(inbounds.settings) THEN inbounds.settings ELSE '{}' END, '$.clients') AS c
WHERE CASE WHEN c.type = 'object' THEN json_extract(c.value, '$.tgId') END IN (?, ?)`

// clientsOfTelegram finds the clients whose tgId is tgId; 0 is no account.
func clientsOfTelegram(tgId int64) (telegramClients, error) {
	var out telegramClients
	if tgId == 0 {
		return out, nil
	}
	db := database.GetDB()
	var emails []string
	if err := db.Raw(telegramXrayClientsSQL, tgId, strconv.FormatInt(tgId, 10)).Scan(&emails).Error; err != nil {
		return out, err
	}
	out.xrayEmails = uniqueNonEmptyStrings(emails)
	if err := db.Where("tg_id = ?", tgId).Order("id asc").Find(&out.tunnels).Error; err != nil {
		return out, err
	}
	return out, nil
}

// tunnelClientTraffics are the AWG/WG clients as the traffic records the bot
// shows for xray clients, their subscription included.
func tunnelClientTraffics(peers []model.TunnelClient) []*xray.ClientTraffic {
	if len(peers) == 0 {
		return nil
	}
	uuids := make([]string, 0, len(peers))
	for _, p := range peers {
		uuids = append(uuids, p.UUID)
	}
	subIds, err := (&TunnelSubscriptionService{}).SubIdsByUUIDs(uuids)
	if err != nil {
		logger.Warning("tunnel subscriptions:", err)
	}
	out := make([]*xray.ClientTraffic, 0, len(peers))
	for _, p := range peers {
		out = append(out, &xray.ClientTraffic{Email: p.Email, Enable: p.Enable, UUID: p.UUID, SubId: subIds[p.UUID],
			Up: p.Upload, Down: p.Download, AllTime: p.AllTime, Total: p.TotalGB, ExpiryTime: p.ExpiryTime,
			Reset: p.Reset, LastOnline: p.LastOnline})
	}
	return out
}
