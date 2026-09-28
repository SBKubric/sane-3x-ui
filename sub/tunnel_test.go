package sub

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// tunTestSettings are the sub server settings every /tun test starts from.
var tunTestSettings = map[string]string{"subPath": "/sub/", "subTitle": "Test VPN", "subUpdates": "12"}

// newTunTestServer opens a fresh database with the given settings on top of
// tunTestSettings and builds the sub server's router the way Start does.
func newTunTestServer(t *testing.T, settings map[string]string) *gin.Engine {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
	service.InvalidateTunnelSubCache()
	t.Cleanup(service.InvalidateTunnelSubCache)
	db := database.GetDB()
	all := map[string]string{}
	for k, v := range tunTestSettings {
		all[k] = v
	}
	for k, v := range settings {
		all[k] = v
	}
	for key, value := range all {
		db.Where("key = ?", key).Delete(&model.Setting{})
		if err := db.Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine, err := NewServer().initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	return engine
}

// addTunnelPeer creates a client through the tunnel service, links it to
// subId and returns it.
func addTunnelPeer(t *testing.T, kind, email, subId string) model.TunnelClient {
	t.Helper()
	c := &model.TunnelClient{Name: email, Email: email, Enable: true}
	var err error
	if kind == model.TunnelKindAwg {
		err = (&service.AwgService{}).AddClient(c)
	} else {
		err = (&service.WgService{}).AddClient(c)
	}
	if err != nil {
		t.Fatalf("add %s peer %s: %v", kind, email, err)
	}
	if subId != "" {
		if err := (&service.TunnelSubscriptionService{}).Set(c.UUID, kind, subId); err != nil {
			t.Fatal(err)
		}
	}
	return *c
}

// setTunnelEndpoint gives the kind's server a public endpoint.
func setTunnelEndpoint(t *testing.T, kind, endpoint string) {
	t.Helper()
	if err := database.GetDB().Model(&model.TunnelServer{}).Where("kind = ?", kind).Update("endpoint", endpoint).Error; err != nil {
		t.Fatal(err)
	}
}

func getTun(t *testing.T, engine http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "panel.example.com:2096"
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// profileHeaders are the headers a subscription answer carries only when it
// has something to say.
var profileHeaders = []string{"Subscription-Userinfo", "Profile-Update-Interval", "Profile-Title", "Profile-Web-Page-Url", "Routing-Enable"}

type tunItem struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UUID       string `json:"uuid"`
	Filename   string `json:"filename"`
	Enable     bool   `json:"enable"`
	ExpiryTime int64  `json:"expiryTime"`
	Conf       string `json:"conf"`
}

// TestTunUnknownSubIdIsAnEmptyList: an unknown subId and one without tunnels
// look the same, 200 and [], and carry no profile headers, so /tun cannot be
// used to find out which subIds exist (docs/spec/tunnel-subscription.md §6).
func TestTunUnknownSubIdIsAnEmptyList(t *testing.T) {
	engine := newTunTestServer(t, nil)
	addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "s-ivan")
	addTunnelPeer(t, model.TunnelKindAwg, "robot-peer", "")

	for _, subId := range []string{"unknown", "robot-peer"} {
		rec := getTun(t, engine, "/tun/"+subId, nil)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Errorf("%s: %d %q, want 200 []", subId, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", subId, ct)
		}
		for _, h := range profileHeaders {
			if v := rec.Header().Get(h); v != "" {
				t.Errorf("%s: %s = %q on an empty answer", subId, h, v)
			}
		}
	}
}

// TestTunServesTheSubscriptionsConfigs: the schema of an element, the
// headers of a non-empty answer, JSON whatever the Accept header says, and
// the host override in Endpoint.
func TestTunServesTheSubscriptionsConfigs(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"proxyOverrideEnable": "true", "proxyOverrideHost": "edge.example.net"})
	setTunnelEndpoint(t, model.TunnelKindAwg, "203.0.113.7:51820")
	phone := addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "s-ivan")
	laptop := addTunnelPeer(t, model.TunnelKindWg, "ivan-laptop", "s-ivan")
	addTunnelPeer(t, model.TunnelKindAwg, "petr-phone", "s-petr")

	for _, accept := range []string{"", "text/html,application/xhtml+xml"} {
		rec := getTun(t, engine, "/tun/s-ivan", map[string]string{"Accept": accept})
		if rec.Code != http.StatusOK {
			t.Fatalf("Accept %q: status %d: %s", accept, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("Accept %q: Content-Type %q", accept, ct)
		}
		var items []tunItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("Accept %q: body is not a JSON array: %v\n%s", accept, err, rec.Body.String())
		}
		if len(items) != 2 {
			t.Fatalf("Accept %q: %d items, want 2: %+v", accept, len(items), items)
		}
		awg, wg := items[0], items[1]
		if awg.Kind != "awg" || awg.Name != "ivan-phone" || awg.UUID != phone.UUID || awg.Filename != "ivan-phone" || !awg.Enable || awg.ExpiryTime != 0 {
			t.Errorf("awg item: %+v", awg)
		}
		if wg.Kind != "wg" || wg.Name != "ivan-laptop" || wg.UUID != laptop.UUID || wg.Filename != "ivan-laptop" {
			t.Errorf("wg item: %+v", wg)
		}
		if !strings.HasPrefix(awg.Conf, "[Interface]\n") || !strings.Contains(awg.Conf, "Endpoint = edge.example.net:") {
			t.Errorf("awg conf does not name the active edge:\n%s", awg.Conf)
		}
		if strings.Contains(awg.Conf, "203.0.113.7") {
			t.Errorf("awg conf leaks the real server:\n%s", awg.Conf)
		}

		h := rec.Header()
		if got := h.Get("Subscription-Userinfo"); got != "upload=0; download=0; total=0; expire=0" {
			t.Errorf("Subscription-Userinfo = %q", got)
		}
		if got := h.Get("Profile-Web-Page-Url"); got != "http://panel.example.com:2096/sub/s-ivan" {
			t.Errorf("Profile-Web-Page-Url = %q, want the subscription page", got)
		}
		if h.Get("Profile-Update-Interval") == "" || h.Get("Profile-Title") == "" || h.Get("Routing-Enable") == "" {
			t.Errorf("profile headers missing: %v", h)
		}
	}
}

// TestTunIsOffWithItsSwitch: subTunEnable off means no route at all.
func TestTunIsOffWithItsSwitch(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subTunEnable": "false"})
	addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "s-ivan")
	if rec := getTun(t, engine, "/tun/s-ivan", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status %d with the route off, want 404", rec.Code)
	}
}

// TestTunFollowsItsPath: the route lives under subTunPath.
func TestTunFollowsItsPath(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subTunPath": "/tunnels/"})
	addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "s-ivan")
	rec := getTun(t, engine, "/tunnels/s-ivan", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ivan-phone"`) {
		t.Errorf("/tunnels/s-ivan: %d %s", rec.Code, rec.Body.String())
	}
}

// TestTunnelFilenames: a filename is the client name cut down to what an
// interface name may be, unique within the answer.
func TestTunnelFilenames(t *testing.T) {
	entry := func(kind, email string) service.TunnelSubEntry {
		return service.TunnelSubEntry{Kind: kind, Client: model.TunnelClient{Email: email}}
	}
	got := tunnelFilenames([]service.TunnelSubEntry{
		entry("awg", "ivan-phone"),
		entry("awg", "Иван телефон"),
		entry("wg", "ivan phone/../x"),
		entry("awg", "a-very-long-client-name"),
		entry("awg", "ivan-phone"),
		entry("wg", "a-very-long-client-name"),
		entry("awg", "ok_=+.-"),
		entry("wg", "Иван"),
	})
	want := []string{"ivan-phone", "awg2", "ivanphone..x", "a-very-long-cli", "ivan-phone-2", "a-very-long-c-2", "ok_=+.-", "wg8"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("tunnelFilenames =\n%q\nwant\n%q", got, want)
	}
}

// storeVlessClient writes an enabled VLESS inbound with one client of subId.
func storeVlessClient(t *testing.T, id int, email, subId string) {
	t.Helper()
	ib := &model.Inbound{Id: id, Port: 20000 + id, Protocol: model.VLESS, Tag: "in-tun-" + email, Remark: "nl", Enable: true,
		Settings:       `{"clients":[{"id":"aaaaaaaa-0000-0000-0000-00000000000` + strconv.Itoa(id) + `","email":"` + email + `","enable":true,"subId":"` + subId + `"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: "{}"}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatal(err)
	}
}

// pageTunnels pulls the tunnels the subscription page was rendered with out
// of its bootstrap element; ok is false when the page has no Tunnels section.
func pageTunnels(t *testing.T, body string) (items []tunItem, ok bool) {
	t.Helper()
	const attr = `data-tunnels="`
	i := strings.Index(body, attr)
	if i < 0 {
		return nil, false
	}
	rest := body[i+len(attr):]
	raw := html.UnescapeString(rest[:strings.Index(rest, `"`)])
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("data-tunnels is not the /tun JSON: %v\n%s", err, raw)
	}
	return items, true
}

var browser = map[string]string{"Accept": "text/html,application/xhtml+xml"}

// TestSubPageShowsTheTunnels: the subscription page of a user with an xray
// client and an AWG peer carries the peer's config for its Tunnels section,
// and a page of xray clients only says it has none.
func TestSubPageShowsTheTunnels(t *testing.T) {
	engine := newTunTestServer(t, nil)
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")
	storeVlessClient(t, 2, "petr-nl", "page-petr")
	addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "page-ivan")

	rec := getTun(t, engine, "/sub/page-ivan", browser)
	if rec.Code != http.StatusOK {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	items, ok := pageTunnels(t, rec.Body.String())
	if !ok || len(items) != 1 || items[0].Name != "ivan-phone" || !strings.HasPrefix(items[0].Conf, "[Interface]\n") {
		t.Errorf("page tunnels: %v %+v", ok, items)
	}

	rec = getTun(t, engine, "/sub/page-petr", browser)
	if items, ok := pageTunnels(t, rec.Body.String()); rec.Code != http.StatusOK || !ok || len(items) != 0 {
		t.Errorf("page without tunnels: %d %v %+v", rec.Code, ok, items)
	}

	// Apps still get the xray links alone.
	rec = getTun(t, engine, "/sub/page-ivan", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Interface") {
		t.Errorf("raw /sub: %d %s", rec.Code, rec.Body.String())
	}
}

// TestSubPageOfTunnelsOnly: a subscription of tunnels alone still has its
// page, since the bot answers every user with /sub/<subId> (#167 Q7); apps
// asking /sub for it get the refusal they always got.
func TestSubPageOfTunnelsOnly(t *testing.T) {
	engine := newTunTestServer(t, nil)
	addTunnelPeer(t, model.TunnelKindAwg, "solo-phone", "page-solo")

	rec := getTun(t, engine, "/sub/page-solo", browser)
	if rec.Code != http.StatusOK {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}
	if items, ok := pageTunnels(t, rec.Body.String()); !ok || len(items) != 1 || items[0].Name != "solo-phone" {
		t.Errorf("page tunnels: %v %+v", ok, items)
	}
	if rec := getTun(t, engine, "/sub/page-solo", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("raw /sub of tunnels only: %d, want 400", rec.Code)
	}
	if rec := getTun(t, engine, "/sub/page-nobody", browser); rec.Code != http.StatusBadRequest {
		t.Errorf("page of an unknown subId: %d, want 400", rec.Code)
	}
}

// TestSubPageWithTheTunnelsOff: with the route off the page has no Tunnels
// section and a subscription of tunnels alone has no page.
func TestSubPageWithTheTunnelsOff(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subTunEnable": "false"})
	storeVlessClient(t, 1, "ivan-nl", "off-ivan")
	addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "off-ivan")
	addTunnelPeer(t, model.TunnelKindAwg, "solo-phone", "off-solo")

	rec := getTun(t, engine, "/sub/off-ivan", browser)
	if _, ok := pageTunnels(t, rec.Body.String()); rec.Code != http.StatusOK || ok {
		t.Errorf("page with the route off: %d, tunnels section %v", rec.Code, ok)
	}
	if rec := getTun(t, engine, "/sub/off-solo", browser); rec.Code != http.StatusBadRequest {
		t.Errorf("tunnels-only page with the route off: %d, want 400", rec.Code)
	}
}
