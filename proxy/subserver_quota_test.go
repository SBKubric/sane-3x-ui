package proxy

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/subpage"
)

// The total limit on a hop's page (#247): the panel sends the clients behind
// the sum in subpage.TrafficPartsHeader beside Subscription-Userinfo; a hop
// passes it on and its page lists them. Without it — an older panel, or an
// older hop on the way — the page is as before.

// quotaParts are a subscription of VLESS 50 GB and AmneziaWG 20 GB.
var quotaParts = []subpage.TrafficPart{
	{Protocol: "vless", Remark: "nl", Used: 2 << 30, Limit: 50 << 30},
	{Protocol: "amneziawg", Remark: "awg", Used: 1 << 30, Limit: 20 << 30},
}

// quotaUpstream is a next hop whose answer to /s/abc carries the breakdown
// value, none for "".
func quotaUpstream(t *testing.T, breakdown string) *SubServer {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/s/abc" || r.URL.Query().Get("format") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=1073741824; download=1073741824; total=53687091200; expire=0")
		if breakdown != "" {
			w.Header().Set(subpage.TrafficPartsHeader, breakdown)
		}
		_, _ = w.Write([]byte("vless://a@edge.example.com:443?type=tcp#nl"))
	}))
	t.Cleanup(upstream.Close)
	host, port := hostPort(t, upstream.URL)
	state := NewState()
	state.SetDocument(&chain.Document{
		Version:  chain.DocumentVersion,
		Revision: 1,
		Self:     chain.Self{Name: "edge-a", Role: chain.RoleEdge, Host: "edge.example.com"},
		NextHop:  chain.NextHop{Host: host, SubPort: port, SubScheme: "http", SubPath: "/s/", JsonPath: "/j/"},
	})
	return testSubServer(t, &Config{NextHop: NextHop{Host: "192.0.2.7"}, Domain: "edge.example.com"}, state)
}

// TestTheHopPassesTheTrafficBreakdownOn: an app's raw subscription carries
// the breakdown as the next hop sent it, with Subscription-Userinfo
// untouched, so a hop nearer the clients has it for its page.
func TestTheHopPassesTheTrafficBreakdownOn(t *testing.T) {
	value := subpage.EncodeTrafficParts(quotaParts)
	w := hopGet(quotaUpstream(t, value), "/s/abc", "")
	if w.Code != http.StatusOK {
		t.Fatalf("raw: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(subpage.TrafficPartsHeader); got != value {
		t.Errorf("breakdown = %q, want %q", got, value)
	}
	if got := w.Header().Get("Subscription-Userinfo"); got != "upload=1073741824; download=1073741824; total=53687091200; expire=0" {
		t.Errorf("Subscription-Userinfo = %q", got)
	}
	if got := hopGet(quotaUpstream(t, ""), "/s/abc", "").Header().Values(subpage.TrafficPartsHeader); len(got) != 0 {
		t.Errorf("a breakdown out of nowhere: %q", got)
	}
}

// TestTheHopPageShowsTheTotalLimit: with the breakdown the page words the
// total limit and lists each protocol; without it, or with one that does not
// parse, the page shows the sum alone, as a v1.9.0 hop does.
func TestTheHopPageShowsTheTotalLimit(t *testing.T) {
	page := html.UnescapeString(hopGet(quotaUpstream(t, subpage.EncodeTrafficParts(quotaParts)), "/s/abc", "text/html").Body.String())
	for _, want := range []string{`data-testid="sub-quota"`, "Total limit: 70.00GB (VLESS 50.00GB + AmneziaWG 20.00GB)",
		"<span>VLESS</span><span>2.00GB / 50.00GB</span>", "<span>AmneziaWG</span><span>1.00GB / 20.00GB</span>",
		"<dd>3.00GB</dd>", "<dd>70.00GB</dd>"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	for _, breakdown := range []string{"", "garbage"} {
		page := html.UnescapeString(hopGet(quotaUpstream(t, breakdown), "/s/abc", "text/html").Body.String())
		if strings.Contains(page, `data-testid="sub-quota"`) {
			t.Errorf("breakdown %q: the page shows a total limit", breakdown)
		}
		for _, want := range []string{"<dd>2.00GB</dd>", "<dd>50.00GB</dd>"} {
			if !strings.Contains(page, want) {
				t.Errorf("breakdown %q: the page lacks the sum %q", breakdown, want)
			}
		}
	}
}

// TestTheHopPageOfTunnelsAloneShowsTheTotalLimit: a subscription of tunnels
// alone has its page from /tun, whose breakdown the page lists too.
func TestTheHopPageOfTunnelsAloneShowsTheTotalLimit(t *testing.T) {
	parts := []subpage.TrafficPart{{Protocol: "amneziawg", Used: 10, Limit: 20 << 30}, {Protocol: "nativewg", Limit: 20 << 30}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/t/solo" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=5; download=5; total=42949672960; expire=0")
		w.Header().Set(subpage.TrafficPartsHeader, subpage.EncodeTrafficParts(parts))
		_, _ = w.Write([]byte(tunUpstreamJSON))
	}))
	t.Cleanup(upstream.Close)
	s := tunSubServer(t, &tunUpstream{Server: upstream}, "/t/")

	w := serveTun(s, "/s/solo", "text/html")
	page := html.UnescapeString(w.Body.String())
	if w.Code != http.StatusOK || !strings.Contains(page, "Total limit: 40.00GB (20.00GB × 2 protocols)") {
		t.Errorf("page of tunnels alone: %d, lacks the total limit", w.Code)
	}
	if got := serveTun(s, "/t/solo", "").Header().Get(subpage.TrafficPartsHeader); got != subpage.EncodeTrafficParts(parts) {
		t.Errorf("/tun breakdown = %q", got)
	}
}
