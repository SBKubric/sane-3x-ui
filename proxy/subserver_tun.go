package proxy

import (
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/coinman-dev/3ax-ui/v2/logger"

	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"
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

// pageTunnel is a tunnel as subpage.html shows it: the .conf ready to be
// saved from a data URI and drawn as a QR code.
type pageTunnel struct {
	Protocol string
	Name     string
	Filename string
	Enable   bool
	Conf     string
	Download template.URL
	QR       template.URL
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
// goes without the section.
func (s *SubServer) pageTunnels(subid string) (tunnels []pageTunnel, header http.Header, ok bool) {
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
	tunnels = make([]pageTunnel, 0, len(items))
	for _, item := range items {
		t := pageTunnel{
			Protocol: "WireGuard",
			Name:     item.Name,
			Filename: item.Filename,
			Enable:   item.Enable,
			Conf:     item.Conf,
			Download: template.URL("data:text/plain;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(item.Conf))),
		}
		if item.Kind == "awg" {
			t.Protocol = "AmneziaWG"
		}
		if t.Filename == "" {
			t.Filename = item.Kind
		}
		// Level L at 4 px a module: an AmneziaWG config is long.
		if png, err := qrcode.Encode(item.Conf, qrcode.Low, -4); err == nil {
			t.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
		}
		tunnels = append(tunnels, t)
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
	used, total, expire := parseUserinfo(header.Get("Subscription-Userinfo"))
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := s.tmpl.Execute(c.Writer, pageData{
		Title:         "Subscription",
		Tunnels:       tunnels,
		TunnelSection: true,
		TunnelsOnly:   true,
		Used:          used,
		Total:         total,
		Expire:        expire,
		Apps:          recommendedApps,
	}); err != nil {
		logger.Warning("proxy-front: render page:", err)
	}
	return true
}
