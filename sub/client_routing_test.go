package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// The client routing (#263): Happ, Incy and v2RayTun get the routing of setting
// subClientRouting in the routing header, each in its own format, picked by
// User-Agent; other apps get none, and the subscription itself is the same
// for all of them.

const (
	clientRoutingSetting = `{"name":"RU","domainStrategy":"IPIfNonMatch",
		"directSites":["geosite:category-ru","domain:ozon.ru"],"directIp":["geoip:ru","geoip:private","17.0.0.0/8"],
		"directProtocols":["bittorrent"],"proxySites":["domain:acomics.ru"]}`
	happUA     = "Happ/3.13.0"
	v2rayTunUA = "v2raytun/android"
	v2rayNGUA  = "v2rayNG/1.9.30"
)

// clientRoutingServer is a sub server with one VLESS client under subId
// "cr", the client routing on, and the old Happ routing rules set too.
func clientRoutingServer(t *testing.T, routing string) func(userAgent, path string) (int, http.Header, string) {
	t.Helper()
	engine := newTunTestServer(t, map[string]string{
		"subClientRouting":        routing,
		"subClientRoutingUpdated": "1760745600",
		"subEnableRouting":        "false",
		"subRoutingRules":         "happ://routing/add/b2xk",
		"subEncrypt":              "true",
		"subJsonEnable":           "true",
		"subJsonPath":             "/json/",
	})
	storeVlessClient(t, 1, "ivan-nl", "cr")
	return func(userAgent, path string) (int, http.Header, string) {
		rec := getTun(t, engine, path, map[string]string{"User-Agent": userAgent})
		return rec.Code, rec.Header(), rec.Body.String()
	}
}

func happProfileOf(t *testing.T, header string) map[string]any {
	t.Helper()
	encoded, ok := strings.CutPrefix(header, "happ://routing/onadd/")
	if !ok {
		t.Fatalf("Routing %q is not a Happ onadd link", header)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSubClientRouting_Happ: Happ gets its profile with the setting's rules,
// routing switched on, and the same LastUpdated on every fetch — the time
// the setting changed, not the time of the fetch.
func TestSubClientRouting_Happ(t *testing.T) {
	fetch := clientRoutingServer(t, clientRoutingSetting)
	code, header, _ := fetch(happUA, "/sub/cr")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	p := happProfileOf(t, header.Get("Routing"))
	if p["Name"] != "RU" || p["GlobalProxy"] != "true" || p["RouteOrder"] != "proxy-block-direct" ||
		p["DomainStrategy"] != "IPIfNonMatch" || p["LastUpdated"] != "1760745600" {
		t.Errorf("profile %v", p)
	}
	if sites, _ := p["DirectSites"].([]any); len(sites) != 2 || sites[0] != "geosite:category-ru" {
		t.Errorf("DirectSites %v", p["DirectSites"])
	}
	if sites, _ := p["ProxySites"].([]any); len(sites) != 1 || sites[0] != "domain:acomics.ru" {
		t.Errorf("ProxySites %v", p["ProxySites"])
	}
	if header.Get("Routing-Enable") != "true" {
		t.Errorf("Routing-Enable %q: the client routing switches Happ's routing on", header.Get("Routing-Enable"))
	}
	if !strings.Contains(strings.Join(header.Values("Vary"), ","), "User-Agent") {
		t.Errorf("Vary %v", header.Values("Vary"))
	}
	_, again, _ := fetch(happUA, "/sub/cr")
	if again.Get("Routing") != header.Get("Routing") {
		t.Error("a second fetch got another profile")
	}
}

// TestSubClientRouting_V2RayTun: v2RayTun gets its routing object in base64,
// rules proxy first and the bittorrent rule in it.
func TestSubClientRouting_V2RayTun(t *testing.T) {
	fetch := clientRoutingServer(t, clientRoutingSetting)
	_, header, _ := fetch(v2rayTunUA, "/sub/cr")
	raw, err := base64.StdEncoding.DecodeString(header.Get("Routing"))
	if err != nil {
		t.Fatalf("Routing %q is not base64", header.Get("Routing"))
	}
	var o struct {
		Name           string `json:"name"`
		DomainStrategy string `json:"domainStrategy"`
		Rules          []struct {
			OutboundTag string   `json:"outboundTag"`
			Domain      []string `json:"domain"`
			Protocol    []string `json:"protocol"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if o.Name != "RU" || o.DomainStrategy != "IPIfNonMatch" || len(o.Rules) != 4 {
		t.Fatalf("object %s", raw)
	}
	if o.Rules[0].OutboundTag != "proxy" || o.Rules[0].Domain[0] != "domain:acomics.ru" {
		t.Errorf("first rule %+v", o.Rules[0])
	}
	if last := o.Rules[3]; last.OutboundTag != "direct" || len(last.Protocol) != 1 || last.Protocol[0] != "bittorrent" {
		t.Errorf("last rule %+v", last)
	}
	if header.Get("Routing-Enable") != "false" {
		t.Errorf("Routing-Enable %q: other apps keep subEnableRouting", header.Get("Routing-Enable"))
	}
}

// TestSubClientRouting_OtherApps: v2rayNG and the rest get no routing — not
// even the old Happ rules, which the client routing replaces.
func TestSubClientRouting_OtherApps(t *testing.T) {
	fetch := clientRoutingServer(t, clientRoutingSetting)
	for _, ua := range []string{v2rayNGUA, "", "clash-verge/v2.0"} {
		if _, header, _ := fetch(ua, "/sub/cr"); header.Get("Routing") != "" {
			t.Errorf("%q got Routing %q", ua, header.Get("Routing"))
		}
	}
}

// TestSubClientRouting_SameBodyForEveryApp: the header is all that changes.
func TestSubClientRouting_SameBodyForEveryApp(t *testing.T) {
	fetch := clientRoutingServer(t, clientRoutingSetting)
	_, _, want := fetch(v2rayNGUA, "/sub/cr")
	if want == "" {
		t.Fatal("empty subscription")
	}
	links, err := base64.StdEncoding.DecodeString(want)
	if err != nil || !strings.HasPrefix(string(links), "vless://") {
		t.Fatalf("body %q", want)
	}
	for _, ua := range []string{happUA, v2rayTunUA, "Mozilla/5.0"} {
		if _, _, body := fetch(ua, "/sub/cr"); body != want {
			t.Errorf("%q got another body:\n%s\n%s", ua, body, want)
		}
	}
}

// TestSubClientRouting_JSONSubscription: the JSON subscription carries the
// same header for the same app.
func TestSubClientRouting_JSONSubscription(t *testing.T) {
	fetch := clientRoutingServer(t, clientRoutingSetting)
	code, header, _ := fetch(happUA, "/json/cr")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if p := happProfileOf(t, header.Get("Routing")); p["Name"] != "RU" {
		t.Errorf("profile %v", p)
	}
}

// TestSubClientRouting_OffKeepsTheOldRules: without the client routing the
// headers are what they were: subRoutingRules to everybody, no Vary.
func TestSubClientRouting_OffKeepsTheOldRules(t *testing.T) {
	fetch := clientRoutingServer(t, "")
	for _, ua := range []string{happUA, v2rayTunUA, v2rayNGUA} {
		_, header, _ := fetch(ua, "/sub/cr")
		if header.Get("Routing") != "happ://routing/add/b2xk" || header.Get("Routing-Enable") != "false" {
			t.Errorf("%q: Routing %q, Routing-Enable %q", ua, header.Get("Routing"), header.Get("Routing-Enable"))
		}
		if v := header.Values("Vary"); len(v) != 0 {
			t.Errorf("%q: Vary %v", ua, v)
		}
	}
}

// TestSubClientRouting_BrokenValueIsOff: a value that does not parse,
// written past the form, turns the client routing off instead of failing
// the subscription server.
func TestSubClientRouting_BrokenValueIsOff(t *testing.T) {
	fetch := clientRoutingServer(t, `{"name":`)
	code, header, _ := fetch(happUA, "/sub/cr")
	if code != http.StatusOK || header.Get("Routing") != "happ://routing/add/b2xk" {
		t.Errorf("status %d, Routing %q", code, header.Get("Routing"))
	}
}

// TestSubClientRouting_TakesASaveWithoutARestart: the routing is read on
// every answer, so a routing saved on a running panel reaches the apps on
// their next update.
func TestSubClientRouting_TakesASaveWithoutARestart(t *testing.T) {
	fetch := clientRoutingServer(t, "")
	if err := (&service.SettingService{}).SetSubClientRouting(clientRoutingSetting); err != nil {
		t.Fatal(err)
	}
	if _, header, _ := fetch(happUA, "/sub/cr"); happProfileOf(t, header.Get("Routing"))["Name"] != "RU" {
		t.Errorf("Routing %q", header.Get("Routing"))
	}
}

// TestSubClientRouting_Incy: Incy gets Happ's very link; Routing-Enable is
// Happ's header, which Incy does not read, and stays subEnableRouting.
func TestSubClientRouting_Incy(t *testing.T) {
	fetch := clientRoutingServer(t, clientRoutingSetting)
	_, happ, _ := fetch(happUA, "/sub/cr")
	_, incy, _ := fetch("INCY/3.8.8/ios", "/sub/cr")
	if incy.Get("Routing") == "" || incy.Get("Routing") != happ.Get("Routing") {
		t.Errorf("Incy Routing %q, Happ %q", incy.Get("Routing"), happ.Get("Routing"))
	}
	if incy.Get("Routing-Enable") != "false" {
		t.Errorf("Incy Routing-Enable %q", incy.Get("Routing-Enable"))
	}
}
