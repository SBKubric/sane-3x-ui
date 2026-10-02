package sub

import (
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/subpage"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// The breakdown of the total limit (#247): the clients behind the sum
// Subscription-Userinfo carries, each with its traffic and limit. The panel's
// page lists them, and the raw subscription answer carries them in
// subpage.TrafficPartsHeader for a hop to list them on its page.

// xrayTrafficParts are the subscription's xray clients, the ones /sub makes
// links of (buildSubs): those carrying subId in the enabled inbounds, with
// the traffic and limit of their stats row, as Subscription-Userinfo adds
// them up.
func (s *SubService) xrayTrafficParts(subId string) []subpage.TrafficPart {
	inbounds, err := s.getInboundsBySubId(subId)
	if err != nil {
		return nil
	}
	var parts []subpage.TrafficPart
	for _, inbound := range inbounds {
		clients, err := s.inboundService.GetClients(inbound)
		if err != nil {
			continue
		}
		for _, client := range clients {
			if client.SubID != subId {
				continue
			}
			ct := s.getClientTraffics(inbound.ClientStats, client.Email)
			parts = append(parts, subpage.TrafficPart{Protocol: string(inbound.Protocol), Remark: inbound.Remark,
				Used: ct.Up + ct.Down, Limit: ct.Total})
		}
	}
	return parts
}

// tunnelTrafficParts are the subscription's tunnel clients, as /tun and the
// page have them.
func tunnelTrafficParts(entries []service.TunnelSubEntry) []subpage.TrafficPart {
	parts := make([]subpage.TrafficPart, 0, len(entries))
	for _, e := range entries {
		protocol := model.AmneziaWG
		if e.Kind == model.TunnelKindWg {
			protocol = model.NativeWG
		}
		parts = append(parts, subpage.TrafficPart{Protocol: string(protocol), Used: e.Client.Upload + e.Client.Download,
			Limit: e.Client.TotalGB})
	}
	return parts
}

// trafficParts are all the subscription's clients: the xray ones, then its
// tunnels.
func (a *SUBController) trafficParts(subId string, tunnels []service.TunnelSubEntry) []subpage.TrafficPart {
	return append(a.subService.xrayTrafficParts(subId), tunnelTrafficParts(tunnels)...)
}

// answerTunnels are the subscription's tunnels for the breakdown of a raw
// answer, none while the tunnel subscription is off.
func (a *SUBController) answerTunnels(subId string) []service.TunnelSubEntry {
	if a.tunnels == nil {
		return nil
	}
	return a.tunnels.pageTunnels(subId)
}

// setTrafficParts puts the breakdown beside Subscription-Userinfo; nothing
// when there is none or it is past the header's bounds.
func setTrafficParts(c *gin.Context, parts []subpage.TrafficPart) {
	if value := subpage.EncodeTrafficParts(parts); value != "" {
		c.Writer.Header().Set(subpage.TrafficPartsHeader, value)
	}
}
