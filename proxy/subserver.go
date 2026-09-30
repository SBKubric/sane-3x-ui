package proxy

import (
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"

	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"
)

//go:embed subpage.html
var subpageHTML string

// Fallback subscription paths, used only to tell "the client asked for a
// subscription while this hop has no document" (503) from "this is not a
// route at all" (404). The real paths always come from the document.
const (
	fallbackSubPath  = "/sub/"
	fallbackJsonPath = "/json/"
	// fallbackTunPath is also the path of a document from a panel that did
	// not name tunPath yet: the panel's default.
	fallbackTunPath = "/tun/"
)

// app is one recommended client app shown on the proxy subscription page.
type app struct {
	Name     string
	Platform string
	URL      string
}

// recommendedApps is the curated client list shown on the proxy page.
var recommendedApps = []app{
	{Name: "Amnezia", Platform: "Android", URL: "https://github.com/amnezia-vpn/amnezia-client/releases"},
	// For the tunnels: AmneziaVPN and AmneziaWG import a .conf (spec §8).
	{Name: "AmneziaVPN", Platform: "all platforms", URL: "https://amnezia.org/downloads"},
	{Name: "AmneziaWG", Platform: "Android", URL: "https://github.com/amnezia-vpn/amneziawg-android/releases"},
	{Name: "DefaultVPN", Platform: "iOS", URL: "https://apps.apple.com/ru/app/defaultvpn/id6744725017"},
	{Name: "SongBird", Platform: "Windows", URL: "https://github.com/o3ku/SongBird/releases/"},
}

// storeLink is one of the large install buttons at the top of the proxy
// subscription page.
type storeLink struct {
	Platform string
	Store    string
	URL      string
}

// installLinks are the store pages of v2RayTun, the app the page points
// clients to first: it reads the subscription headers, including
// Profile-Update-Interval (#217).
var installLinks = []storeLink{
	{Platform: "Android", Store: "Google Play", URL: "https://play.google.com/store/apps/details?id=com.v2raytun.android"},
	{Platform: "iPhone / iPad", Store: "App Store", URL: "https://apps.apple.com/us/app/v2ray-vpn-client/id6752994543"},
}

// headers copied through from the next hop to subscription clients.
var passthroughHeaders = []string{
	"Subscription-Userinfo", "Profile-Update-Interval", "Profile-Title",
	"Profile-Web-Page-Url", "Support-Url", "Announce", "Routing-Enable", "Routing",
}

// SubServer is the hop's sub port: subscriptions proxied from the next hop,
// the wave endpoints, and — until this box has joined — the join page. One
// port carries all three (§3.3): the hop is open to the world anyway, the
// wave is bearer-protected, and a second port would mean another field in the
// registry, the document and the installer.
//
// Which upstream it fetches from and under which paths is not configuration:
// both come from the current chain document, so a revision that moves them
// takes effect without a restart (§3.5).
type SubServer struct {
	cfg   *Config
	state *State
	chain *ChainHandler
	join  *JoinPage

	tmpl   *template.Template
	client *http.Client

	// public is the sub port as proxy.json names it; loopback is the same
	// handler behind the front (#140), plain HTTP on the address nginx
	// passes the sub paths to. Both run while the outer neighbours move to
	// 443, and public closes once they have.
	mu           sync.Mutex
	public       *http.Server
	loopback     *http.Server
	loopbackAddr string
}

// NewSubServer builds the hop's sub server (does not start it). join may be
// nil for a box that has already joined.
func NewSubServer(cfg *Config, state *State, chainHandler *ChainHandler, join *JoinPage) (*SubServer, error) {
	tmpl, err := template.New("subpage").Parse(subpageHTML)
	if err != nil {
		return nil, fmt.Errorf("parse proxy subpage template: %w", err)
	}
	return &SubServer{
		cfg:   cfg,
		state: state,
		chain: chainHandler,
		join:  join,
		tmpl:  tmpl,
		client: &http.Client{
			Timeout: 15 * time.Second,
			// A hop reaches its next hop by a hidden address (often a bare
			// IP and/or a self-signed cert), so TLS verification is skipped
			// for this hop-to-hop leg; trust rests on the hop secret.
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}, nil
}

// Handler is the whole sub port, exposed for tests.
func (s *SubServer) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	if s.chain != nil {
		engine.Any(ChainPathPrefix+"/*endpoint", gin.WrapH(s.chain.Handler()))
	}
	if s.join != nil {
		engine.Any("/join/*token", gin.WrapH(s.join.Handler()))
	}
	// Subscription paths travel in the document and can change under a
	// running server, so they are matched per request rather than registered.
	engine.NoRoute(s.route)
	return engine
}

// Start binds and serves the sub port in a background goroutine.
func (s *SubServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startPublicLocked()
}

// startPublicLocked binds the public sub port, TLS as proxy.json says.
func (s *SubServer) startPublicLocked() error {
	addr := net.JoinHostPort(s.cfg.SubListen, strconv.Itoa(s.cfg.SubPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy sub server listen %s: %w", addr, err)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.public = server

	go func() {
		var serr error
		if s.cfg.TLS() {
			serr = server.ServeTLS(ln, s.cfg.CertFile, s.cfg.KeyFile)
		} else {
			serr = server.Serve(ln)
		}
		if serr != nil && serr != http.ErrServerClosed {
			logger.Error("proxy sub server:", serr)
		}
	}()

	logger.Infof("proxy-front: sub port %s (subscriptions, %s/*, join page)", addr, ChainPathPrefix)
	return nil
}

// ServeLoopback serves the sub server on addr for the front's HTTP side:
// plain HTTP, since nginx has terminated TLS, and with the client address nginx
// saw. Serving the address already served is a no-op.
func (s *SubServer) ServeLoopback(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loopback != nil && s.loopbackAddr == addr {
		return nil
	}
	s.stopLoopbackLocked()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy sub server listen %s behind the front: %w", addr, err)
	}
	server := &http.Server{Handler: trustFront(s.Handler()), ReadHeaderTimeout: 10 * time.Second}
	s.loopback, s.loopbackAddr = server, addr
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("proxy sub server behind the front:", err)
		}
	}()
	logger.Infof("proxy-front: sub server behind the front on %s", addr)
	return nil
}

// StopLoopback takes the listener behind the front down.
func (s *SubServer) StopLoopback() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLoopbackLocked()
}

func (s *SubServer) stopLoopbackLocked() {
	if s.loopback != nil {
		_ = s.loopback.Close()
	}
	s.loopback, s.loopbackAddr = nil, ""
}

// ClosePublic closes the public sub port: the outer neighbours all reach this
// box through the front now.
func (s *SubServer) ClosePublic() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.public == nil {
		return nil
	}
	err := s.public.Close()
	s.public = nil
	logger.Info("proxy-front: the old sub port is closed, everything comes through the front now")
	return err
}

// OpenPublic brings the public sub port back, for a front that went away.
func (s *SubServer) OpenPublic() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.public != nil {
		return nil
	}
	return s.startPublicLocked()
}

// Stop shuts the sub server down.
func (s *SubServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLoopbackLocked()
	if s.public != nil {
		err := s.public.Close()
		s.public = nil
		return err
	}
	return nil
}

// trustFront takes the client address from X-Real-IP, which the front's HTTP
// side sets from the connection it accepted: behind nginx every request
// comes from 127.0.0.1, and the address a join arrived from is evidence the
// owner reads (§4.4). Only the loopback listener is wrapped — on the public
// port the header is whatever the client chose to send.
func trustFront(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
		}
		next.ServeHTTP(w, r)
	})
}

// nextHop is where subscriptions are fetched from and under which paths: the
// document's next hop, falling back to proxy.json before the first document.
func (s *SubServer) nextHop() (base string, sub string, json string) {
	doc := s.state.Document()
	base, sub, json = s.cfg.NextHopBase(), fallbackSubPath, fallbackJsonPath
	if doc == nil {
		return base, sub, json
	}
	if doc.NextHop.Host != "" {
		scheme := doc.NextHop.SubScheme
		if scheme == "" {
			scheme = DefaultSubScheme
		}
		port := doc.NextHop.SubPort
		if port == 0 {
			port = DefaultSubPort
		}
		base = scheme + "://" + net.JoinHostPort(doc.NextHop.Host, strconv.Itoa(port))
	}
	if doc.NextHop.SubPath != "" {
		sub = doc.NextHop.SubPath
	}
	if doc.NextHop.JsonPath != "" {
		json = doc.NextHop.JsonPath
	}
	return base, sub, json
}

// route dispatches a request against the paths of the current document. While
// this hop has no document it knows neither its upstream nor its paths, so a
// subscription request is a 503 — a hop that answered 404 would look like a
// wrong link instead of a box still waiting for the wave (§5.4).
func (s *SubServer) route(c *gin.Context) {
	path := c.Request.URL.Path
	_, subPath, jsonPath := s.nextHop()
	doc := s.state.Document()

	// The captcha before a request (#220) sits under the subscription path:
	// matched first, or «captcha» would be taken for a subscription id.
	if part, ok := captcha.Route(path, subPath); ok {
		if doc == nil {
			c.String(http.StatusServiceUnavailable, "this box has not joined the chain yet")
			return
		}
		s.handleCaptcha(c, part)
		return
	}
	if id, ok := subscriptionID(path, subPath, fallbackSubPath); ok {
		if doc == nil {
			c.String(http.StatusServiceUnavailable, "this box has not joined the chain yet")
			return
		}
		s.handleSub(c, id)
		return
	}
	if id, ok := subscriptionID(path, jsonPath, fallbackJsonPath); ok {
		if doc == nil {
			c.String(http.StatusServiceUnavailable, "this box has not joined the chain yet")
			return
		}
		s.handleJson(c, id)
		return
	}
	if id, ok := subscriptionID(path, s.tunPath(), fallbackTunPath); ok {
		if doc == nil {
			c.String(http.StatusServiceUnavailable, "this box has not joined the chain yet")
			return
		}
		s.handleTun(c, id)
		return
	}
	c.Status(http.StatusNotFound)
}

// subscriptionID matches a request path against the document's path and the
// built-in fallback, returning the subscription id.
func subscriptionID(path, documentPath, fallback string) (string, bool) {
	for _, prefix := range []string{documentPath, fallback} {
		if prefix == "" {
			continue
		}
		if id, found := strings.CutPrefix(path, prefix); found && id != "" && !strings.Contains(id, "/") {
			return id, true
		}
	}
	return "", false
}

// handleSub serves the raw subscription to apps and the custom page to browsers.
func (s *SubServer) handleSub(c *gin.Context, subid string) {
	base, subPath, _ := s.nextHop()
	body, header, status, err := s.fetchUpstream(base, subPath, subid)
	if err == nil && isRefusal(status) {
		// A subscription of tunnels alone has no xray links for the panel to
		// answer with, but still a page (#167 Q7).
		if wantsHTML(c) && s.renderTunnelsOnlyPage(c, subid) {
			return
		}
		passRefusal(c, status, header, body)
		return
	}
	if err != nil || status != http.StatusOK || len(body) == 0 {
		logger.Warningf("proxy-front: next hop sub fetch failed (status %d): %v", status, err)
		c.String(http.StatusBadGateway, "subscription unavailable")
		return
	}
	if wantsHTML(c) {
		s.renderPage(c, subid, body, header)
		return
	}
	copyHeaders(c, header)
	c.Header("Profile-Web-Page-Url", s.publicURL(c, s.publicSubPath(), subid))
	c.String(http.StatusOK, string(body))
}

// handleJson proxies the JSON subscription straight through (the copy-JSON action).
func (s *SubServer) handleJson(c *gin.Context, subid string) {
	base, _, jsonPath := s.nextHop()
	body, header, status, err := s.fetchUpstream(base, jsonPath, subid)
	if err == nil && isRefusal(status) {
		passRefusal(c, status, header, body)
		return
	}
	if err != nil || status != http.StatusOK || len(body) == 0 {
		c.String(http.StatusBadGateway, "subscription unavailable")
		return
	}
	copyHeaders(c, header)
	c.Header("Profile-Web-Page-Url", s.publicURL(c, s.publicSubPath(), subid))
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// publicSubPath is the path clients use on this hop — the same one it fetches
// under, because the chain forwards subscriptions one to one.
func (s *SubServer) publicSubPath() string {
	_, subPath, _ := s.nextHop()
	return subPath
}

// publicJsonPath is the JSON subscription path on this hop.
func (s *SubServer) publicJsonPath() string {
	_, _, jsonPath := s.nextHop()
	return jsonPath
}

// publicURL is the address of this hop's own subscription endpoint as a client
// outside sees it: the configured Domain, else the Host the request came in
// on. The panel builds its Profile-Web-Page-Url from the Host it was fetched
// by — an address deeper in the chain — so every hop must replace that header
// with its own identity, or subscription apps would carry a link inward.
func (s *SubServer) publicURL(c *gin.Context, path, subid string) string {
	scheme := s.cfg.PublicScheme()
	host := c.Request.Host
	if s.cfg.Domain != "" {
		host = PublicHostPort(scheme, s.cfg.Domain, s.cfg.PublicSubPort())
	}
	return scheme + "://" + host + path + subid
}

// fetchUpstream GETs the raw subscription (not the HTML page) for the given
// path+id from the next hop.
func (s *SubServer) fetchUpstream(base, path, subid string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequest(http.MethodGet, base+path+subid, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return body, resp.Header, resp.StatusCode, err
}

// isRefusal is the next hop turning the request down — the panel's 400 for a
// subscription it does not know — as opposed to failing to serve it.
func isRefusal(status int) bool {
	return status >= 400 && status < 500
}

// passRefusal answers the client with the next hop's refusal as it came:
// status, body and content type (#152). The hop then answers an unknown
// subscription as the panel does, so the front's HTTP side logs it as a miss
// (a 502 was none, and sub brute force through a hop went unbanned), and the
// answer does not tell a hop from the panel. Not logged: a prober would fill
// the log.
func passRefusal(c *gin.Context, status int, header http.Header, body []byte) {
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	c.Data(status, contentType, body)
}

func copyHeaders(c *gin.Context, header http.Header) {
	for _, h := range passthroughHeaders {
		if v := header.Get(h); v != "" {
			c.Header(h, v)
		}
	}
}

func wantsHTML(c *gin.Context) bool {
	if c.Query("html") == "1" || strings.EqualFold(c.Query("view"), "html") {
		return true
	}
	return strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/html")
}

// pageData is the view model for subpage.html.
type pageData struct {
	Title   string
	SubURL  string
	JsonURL string
	QR      template.URL
	Configs []string
	// Tunnels are the subscription's AmneziaWG/WireGuard configs, shown when
	// TunnelSection is set: the next hop answered /tun (spec §7).
	Tunnels       []pageTunnel
	TunnelSection bool
	// TunnelsOnly is a page of a subscription with no xray links.
	TunnelsOnly bool
	Used        string
	Total       string
	Expire      string
	// Install are the store buttons at the top: set only on a page with a
	// subscription an app can import.
	Install []storeLink
	Apps    []app
}

func (s *SubServer) renderPage(c *gin.Context, subid string, body []byte, header http.Header) {
	subURL := s.publicURL(c, s.publicSubPath(), subid)
	jsonURL := s.publicURL(c, s.publicJsonPath(), subid)

	used, total, expire := parseUserinfo(header.Get("Subscription-Userinfo"))

	var qr template.URL
	if png, err := qrcode.Encode(subURL, qrcode.Medium, 256); err == nil {
		qr = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	}

	tunnels, _, tunnelSection := s.pageTunnels(subid)
	c.Header("Content-Type", "text/html; charset=utf-8")
	// NoRoute leaves 404 on the writer; the page is a success.
	c.Status(http.StatusOK)
	if err := s.tmpl.Execute(c.Writer, pageData{
		Title:         "Subscription",
		SubURL:        subURL,
		JsonURL:       jsonURL,
		QR:            qr,
		Configs:       decodeConfigs(body),
		Tunnels:       tunnels,
		TunnelSection: tunnelSection,
		Used:          used,
		Total:         total,
		Expire:        expire,
		Install:       installLinks,
		Apps:          recommendedApps,
	}); err != nil {
		logger.Warning("proxy-front: render page:", err)
	}
}

// decodeConfigs turns the raw subscription body (base64 or a plain newline list)
// into individual config links.
func decodeConfigs(body []byte) []string {
	raw := strings.TrimSpace(string(body))
	if dec, err := base64.StdEncoding.DecodeString(raw); err == nil {
		raw = string(dec)
	} else if dec, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		raw = string(dec)
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// parseUserinfo extracts human-readable used/total/expiry from a
// Subscription-Userinfo header ("upload=..; download=..; total=..; expire=..").
func parseUserinfo(h string) (used, total, expire string) {
	if h == "" {
		return "", "", ""
	}
	var up, down, tot, exp int64
	for _, part := range strings.Split(h, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		switch strings.TrimSpace(kv[0]) {
		case "upload":
			up = n
		case "download":
			down = n
		case "total":
			tot = n
		case "expire":
			exp = n
		}
	}
	used = common.FormatTraffic(up + down)
	total = "∞"
	if tot > 0 {
		total = common.FormatTraffic(tot)
	}
	if exp > 0 {
		expire = time.Unix(exp, 0).Format("2006-01-02")
	}
	return used, total, expire
}
