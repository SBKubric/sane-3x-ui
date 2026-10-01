package sub

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// TunnelSubController serves the tunnel subscription: GET <subTunPath><subId>
// answers with the subscription's AmneziaWG and WireGuard configs as a JSON
// array (docs/spec/tunnel-subscription.md §6). It is public like /sub, the
// subId being the secret, and always answers 200 with an array, [] included,
// so it tells nobody which subIds exist.
//
// It borrows the xray subscription's controller for the profile headers and
// the page address, and lends the subscription page its tunnels.
type TunnelSubController struct {
	sub *SUBController
	svc service.TunnelSubscriptionService
}

// NewTunnelSubController registers the route on g, the group of subTunPath,
// and hands sub the tunnels for its page.
func NewTunnelSubController(g *gin.RouterGroup, sub *SUBController) *TunnelSubController {
	a := &TunnelSubController{sub: sub}
	sub.tunnels = a
	g.GET(":subid", a.tun)
	return a
}

// tunnelSubItem is one element of the /tun answer.
type tunnelSubItem struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UUID       string `json:"uuid"`
	Filename   string `json:"filename"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"`
	Conf       string `json:"conf"`
}

func (a *TunnelSubController) tun(c *gin.Context) {
	subId := c.Param("subid")
	entries, err := a.svc.ClientsBySubId(subId)
	if err != nil {
		logger.Warning("tunnel subscription:", err)
		c.String(http.StatusInternalServerError, "Error!")
		return
	}
	if len(entries) > 0 {
		header, _ := a.svc.Userinfo(entries)
		s := a.sub
		s.ApplyCommonHeaders(c, header, s.updateInterval, s.subTitle, s.subSupportUrl, a.pageURL(c, subId),
			s.subAnnounce, s.subEnableRouting, s.subRoutingRules)
	}
	c.JSON(http.StatusOK, tunnelSubItems(entries))
}

// pageURL is the subscription page, the Profile-Web-Page-Url of /tun: the
// owner's profile URL when set, as on /sub, else /sub/<subId> on the public
// subscription address (#224) or, without one, on the address the request
// came in by.
func (a *TunnelSubController) pageURL(c *gin.Context, subId string) string {
	if a.sub.subProfileUrl != "" {
		return a.sub.subProfileUrl
	}
	if public, _ := a.sub.subService.settingService.GetSubPublicURL(); public != "" {
		return service.SubPublicLink(public, a.sub.subPath, subId)
	}
	scheme, _, hostWithPort, _ := a.sub.subService.ResolveRequest(c)
	return fmt.Sprintf("%s://%s%s%s", scheme, hostWithPort, a.sub.subPath, subId)
}

// pageTunnels is the subscription's tunnels for its page. A lookup that
// fails is logged and shown as no tunnels: the page still has the xray part
// to show.
func (a *TunnelSubController) pageTunnels(subId string) []service.TunnelSubEntry {
	entries, err := a.svc.ClientsBySubId(subId)
	if err != nil {
		logger.Warning("tunnel subscription page:", err)
		return nil
	}
	return entries
}

// tunnelsOfPage is what the subscription page needs of the tunnels, looked up
// only for a browser: the entries (none with the tunnel subscription off),
// and whether this is a page of tunnels alone — noXray, and tunnels to show.
// Such a subscription still has a page, since the bot answers every user
// with /sub/<subId> (#167 Q7).
func (a *SUBController) tunnelsOfPage(c *gin.Context, subId string, noXray bool) ([]service.TunnelSubEntry, bool) {
	if a.tunnels == nil || !wantsPage(c) {
		return nil, false
	}
	entries := a.tunnels.pageTunnels(subId)
	return entries, noXray && len(entries) > 0
}

// wantsPage is the test subs uses to answer a browser with the page.
func wantsPage(c *gin.Context) bool {
	return strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/html") || c.Query("html") == "1" || strings.EqualFold(c.Query("view"), "html")
}

func tunnelSubItems(entries []service.TunnelSubEntry) []tunnelSubItem {
	names := tunnelFilenames(entries)
	items := make([]tunnelSubItem, 0, len(entries))
	for i, e := range entries {
		items = append(items, tunnelSubItem{
			Kind:       e.Kind,
			Name:       tunnelClientName(e),
			UUID:       e.Client.UUID,
			Filename:   names[i],
			Enable:     e.Client.Enable,
			ExpiryTime: e.Client.ExpiryTime,
			Conf:       e.Conf,
		})
	}
	return items
}

// tunnelClientName is the name the panel shows for a tunnel client: its
// email, which is what the client table calls its name.
func tunnelClientName(e service.TunnelSubEntry) string {
	if e.Client.Email != "" {
		return e.Client.Email
	}
	return e.Client.Name
}

// tunnelFilenameMax is the longest interface name Linux takes (IFNAMSIZ-1):
// the WireGuard tools name the interface after the .conf file.
const tunnelFilenameMax = 15

// tunnelFilenames names a .conf file for every entry: the client name with
// only [a-zA-Z0-9_=+.-] kept and cut to tunnelFilenameMax, <kind><position>
// when nothing is left, and -2, -3 on a name already taken in the answer —
// the suffix taking its room from the name, so the limit still holds.
func tunnelFilenames(entries []service.TunnelSubEntry) []string {
	out := make([]string, len(entries))
	taken := map[string]bool{}
	for i, e := range entries {
		base := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
				r == '_', r == '=', r == '+', r == '.', r == '-':
				return r
			}
			return -1
		}, tunnelClientName(e))
		if base == "" {
			base = e.Kind + strconv.Itoa(i+1)
		}
		name := cut(base, tunnelFilenameMax)
		for n := 2; taken[name]; n++ {
			suffix := "-" + strconv.Itoa(n)
			name = cut(base, tunnelFilenameMax-len(suffix)) + suffix
		}
		taken[name] = true
		out[i] = name
	}
	return out
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
