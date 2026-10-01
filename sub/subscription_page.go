package sub

import (
	"net/http"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/subpage"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// The subscription page (#235): package subpage's template, the one a hop
// of the chain renders too, filled from what the panel has.

// appsListFormat is the query of the subscription path that asks for the
// page's app list instead of the links: a hop takes the owner's list from
// its next hop this way, as it takes the JSON configs (#231).
const appsListFormat = "apps"

// renderSubPage answers a browser with the subscription page. page is the
// upstream page data (traffic, URLs, the links), tunnels the subscription's
// tunnel configs when the tunnel subscription is on.
func (a *SUBController) renderSubPage(c *gin.Context, subId, host string, page PageData, tunnels []service.TunnelSubEntry) {
	links := pageLinks(page.Result)
	if configs := a.pageJSONConfigs(subId, host, len(links)); configs != nil {
		for i := range links {
			links[i].JSON = configs[i]
		}
	}
	var tuns []subpage.Tunnel
	for _, item := range tunnelSubItems(tunnels) {
		tuns = append(tuns, subpage.Tunnel{Kind: item.Kind, Name: item.Name, Filename: item.Filename, Enable: item.Enable, Conf: item.Conf})
	}
	p := subpage.Page{
		Title:   a.subTitle,
		Usage:   subpage.Usage{Known: true, Up: page.UploadByte, Down: page.DownloadByte, Total: page.TotalByte, Expire: page.Expire, Disabled: !page.Enabled},
		Links:   links,
		Tunnels: tuns,
		Apps:    a.pageApps(),
	}
	if len(links) > 0 {
		p.SubURL = page.SubUrl
		if page.SubJsonUrl != "" {
			p.SubURLs = append(p.SubURLs, subpage.NamedURL{Name: "JSON", URL: page.SubJsonUrl})
		}
		if page.SubClashUrl != "" {
			p.SubURLs = append(p.SubURLs, subpage.NamedURL{Name: "Clash / Mihomo", URL: page.SubClashUrl})
		}
	}
	subpage.Render(c.Writer, c.Request, p)
}

// pageLinks are the subscription's links, one per line of its entries.
func pageLinks(subs []string) []subpage.Link {
	var links []subpage.Link
	for _, sub := range subs {
		for _, line := range strings.Split(sub, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				links = append(links, subpage.Link{URL: line})
			}
		}
	}
	return links
}

// pageJSONConfigs is the client JSON config of every link on the page
// (#231): built here rather than linked, so the copy works with the JSON
// subscription off and its path closed. nil when the configs do not line up
// with the links one to one; the page then offers the links alone.
func (a *SUBController) pageJSONConfigs(subId, host string, links int) []string {
	configs, _, err := a.subJsonService.GetConfigs(subId, host)
	if err != nil || len(configs) == 0 || len(configs) != links {
		return nil
	}
	return configs
}

// pageApps is the owner's app list, the built-in one when the stored list
// does not parse.
func (a *SUBController) pageApps() []subpage.App {
	apps, err := a.subService.settingService.GetSubPageApps()
	if err != nil {
		logger.Warning("subscription page: the app list setting:", err)
	}
	return apps
}

// subAppsList answers /sub/<id>?format=apps: the page's app list, for a hop
// rendering the page of a subscription it passes on. Like the page, only for
// a subscription that exists — xray links or tunnels — and the 400 /sub
// answers otherwise.
func (a *SUBController) subAppsList(c *gin.Context, subId string) {
	_, host, _, _ := a.subService.ResolveRequest(c)
	subs, _, _, err := a.subService.GetSubs(subId, host)
	if (err != nil || len(subs) == 0) && !a.hasTunnels(subId) {
		c.String(http.StatusBadRequest, "Error!")
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(subpage.AppsJSON(a.pageApps())))
}

// hasTunnels reports whether the subscription has tunnel configs on the
// tunnel subscription.
func (a *SUBController) hasTunnels(subId string) bool {
	if a.tunnels == nil {
		return false
	}
	entries, err := a.tunnels.svc.ClientsBySubId(subId)
	return err == nil && len(entries) > 0
}
