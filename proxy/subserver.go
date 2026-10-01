package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/subpage"

	"github.com/gin-gonic/gin"
)

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
	return &SubServer{
		cfg:   cfg,
		state: state,
		chain: chainHandler,
		join:  join,
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

	// The bot's own path (#220): its Mini App, passed on to the next hop.
	if prefix := s.thirdPartyPath(); prefix != "" && strings.HasPrefix(path, prefix) {
		s.handleThirdParty(c)
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
	if format := c.Query("format"); (format == jsonListFormat || format == appsListFormat) && !wantsHTML(c) {
		s.handleJSONList(c, base, subPath, subid, format)
		return
	}
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

// jsonListFormat is the query of the subscription path that asks for the
// client JSON config of every link instead of the links (#231): the panel
// answers it with or without its JSON subscription on. appsListFormat asks
// for the page's app list (#235).
const (
	jsonListFormat = "json"
	appsListFormat = "apps"
)

// handleJSONList passes the next hop's answer to a JSON format of the
// subscription path on — the config list or the app list — so a hop nearer
// the clients can fetch it for its page through this one.
func (s *SubServer) handleJSONList(c *gin.Context, base, subPath, subid, format string) {
	body, header, status, err := s.fetchUpstream(base, subPath, subid+"?format="+format)
	if err == nil && isRefusal(status) {
		passRefusal(c, status, header, body)
		return
	}
	if err != nil || status != http.StatusOK || len(body) == 0 {
		c.String(http.StatusBadGateway, "subscription unavailable")
		return
	}
	copyHeaders(c, header)
	c.Header("Profile-Web-Page-Url", s.publicURL(c, subPath, subid))
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// pageJSONConfigs fetches the client JSON config of every link from the next
// hop for the page, one per link in the links' order and indented as the
// JSON subscription answers for one link. nil when the next hop has none —
// an older panel answers the links again — or they do not line up with the
// links; the page then offers the links alone.
func (s *SubServer) pageJSONConfigs(subid string, links int) []string {
	base, subPath, _ := s.nextHop()
	body, _, status, err := s.fetchUpstream(base, subPath, subid+"?format="+jsonListFormat)
	if err != nil || status != http.StatusOK {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(body, &list) != nil || len(list) != links {
		return nil
	}
	configs := make([]string, 0, len(list))
	for _, config := range list {
		var text bytes.Buffer
		if json.Indent(&text, config, "", "  ") != nil {
			return nil
		}
		configs = append(configs, text.String())
	}
	return configs
}

// handleJson proxies the JSON subscription straight through.
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

// publicURL is the address of this hop's own subscription endpoint as a client
// outside sees it: the configured Domain, else the Host the request came in
// on. The panel builds its Profile-Web-Page-Url from the Host it was fetched
// by — an address deeper in the chain — so every hop must replace that header
// with its own identity, or subscription apps would carry a link inward.
// With a public subscription address in the document (#224) clients come
// through the subscription showcase, and that address is the identity.
func (s *SubServer) publicURL(c *gin.Context, path, subid string) string {
	if doc := s.state.Document(); doc != nil && doc.PublicSubURL != "" {
		return doc.PublicSubURL + path + subid
	}
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

func (s *SubServer) renderPage(c *gin.Context, subid string, body []byte, header http.Header) {
	links := decodeConfigs(body)
	pageLinks := make([]subpage.Link, len(links))
	jsons := s.pageJSONConfigs(subid, len(links))
	for i, link := range links {
		pageLinks[i].URL = link
		if jsons != nil {
			pageLinks[i].JSON = jsons[i]
		}
	}
	tunnels, _, _ := s.pageTunnels(subid)
	// NoRoute leaves 404 on the writer; the page is a success.
	c.Status(http.StatusOK)
	subpage.Render(c.Writer, c.Request, subpage.Page{
		Title:   profileTitle(header),
		SubURL:  s.publicURL(c, s.publicSubPath(), subid),
		Usage:   subpage.ParseUserinfo(header.Get("Subscription-Userinfo")),
		Links:   pageLinks,
		Tunnels: tunnels,
		Apps:    s.pageApps(subid),
	})
}

// pageApps is the owner's app list for the page, from the next hop's
// subscription path (#235); the built-in list when the next hop has none to
// give — a panel older than the list answers the links again.
func (s *SubServer) pageApps(subid string) []subpage.App {
	base, subPath, _ := s.nextHop()
	body, _, status, err := s.fetchUpstream(base, subPath, subid+"?format="+appsListFormat)
	if err != nil || status != http.StatusOK {
		return subpage.DefaultApps()
	}
	apps, err := subpage.ParseApps(string(body))
	if err != nil {
		return subpage.DefaultApps()
	}
	return apps
}

// profileTitle is the subscription title the panel sends in Profile-Title,
// "base64:" and all; "" when it sends none.
func profileTitle(header http.Header) string {
	title := header.Get("Profile-Title")
	if encoded, ok := strings.CutPrefix(title, "base64:"); ok {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return ""
		}
		return string(raw)
	}
	return title
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
