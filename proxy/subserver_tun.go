package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/subpage"

	"github.com/gin-gonic/gin"
)

// The tunnel subscription on a hop (docs/spec/tunnel-subscription.md §7):
// /tun proxied from the next hop like /json, and the tunnels on the hop's
// own subscription page, fetched server-side since the page has no way to
// the upstream of its own.

// tunnelItem is one element of the panel's /tun answer.
type tunnelItem struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UUID       string `json:"uuid"`
	Filename   string `json:"filename"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"`
	Conf       string `json:"conf"`
}

// tunPath is the tunnel subscription path of the current document, the
// default for a document that names none.
func (s *SubServer) tunPath() string {
	if doc := s.state.Document(); doc != nil && doc.NextHop.TunPath != "" {
		return doc.NextHop.TunPath
	}
	return fallbackTunPath
}

// handleTun proxies /tun straight through, the page address rewritten to
// this hop's. An empty answer is an answer, not a failure.
func (s *SubServer) handleTun(c *gin.Context, subid string) {
	base, _, _ := s.nextHop()
	body, header, status, err := s.fetchUpstream(base, s.tunPath(), subid)
	if err == nil && isRefusal(status) {
		passRefusal(c, status, header, body)
		return
	}
	if err != nil || status != http.StatusOK || len(body) == 0 {
		c.String(http.StatusBadGateway, "subscription unavailable")
		return
	}
	copyHeaders(c, header)
	if header.Get("Profile-Web-Page-Url") != "" {
		c.Header("Profile-Web-Page-Url", s.publicURL(c, s.publicSubPath(), subid))
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// pageTunnels asks the next hop for the subscription's tunnels. ok is false
// when it could not say — no /tun there, or a failure — and the page then
// goes without them.
func (s *SubServer) pageTunnels(subid string) (tunnels []subpage.Tunnel, header http.Header, ok bool) {
	base, _, _ := s.nextHop()
	body, header, status, err := s.fetchUpstream(base, s.tunPath(), subid)
	if err != nil || status != http.StatusOK {
		return nil, nil, false
	}
	var items []tunnelItem
	if err := json.Unmarshal(body, &items); err != nil {
		logger.Warning("proxy-front: next hop /tun answer is not a list:", err)
		return nil, nil, false
	}
	tunnels = make([]subpage.Tunnel, 0, len(items))
	for _, item := range items {
		tunnels = append(tunnels, subpage.Tunnel{Kind: item.Kind, Name: item.Name, Filename: item.Filename, Enable: item.Enable, Conf: item.Conf})
	}
	return tunnels, header, true
}

// renderTunnelsOnlyPage renders the page of a subscription that has tunnels
// and no xray links, and reports whether it did.
func (s *SubServer) renderTunnelsOnlyPage(c *gin.Context, subid string) bool {
	tunnels, header, ok := s.pageTunnels(subid)
	if !ok || len(tunnels) == 0 {
		return false
	}
	c.Status(http.StatusOK)
	subpage.Render(c.Writer, c.Request, subpage.Page{
		Title:   profileTitle(header),
		Usage:   pageUsage(header),
		Tunnels: tunnels,
		Apps:    s.pageApps(subid),
	})
	return true
}
