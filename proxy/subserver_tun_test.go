package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// tunUpstream is a next hop with a subscription of an xray link and an
// AmneziaWG config ("abc"), one of tunnels only ("solo") and nothing else.
type tunUpstream struct {
	*httptest.Server
	asked []string
}

const tunUpstreamJSON = `[{"kind":"awg","name":"ivan-phone","uuid":"u1","filename":"ivan-phone","enable":true,"expiryTime":0,"conf":"[Interface]\nPrivateKey = k\n\n[Peer]\nEndpoint = edge.example.com:51820\n"}]`

func newTunUpstream(t *testing.T, tunPath string) *tunUpstream {
	t.Helper()
	u := &tunUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.asked = append(u.asked, r.URL.Path)
		switch r.URL.Path {
		case "/s/abc", "/s/bob":
			w.Header().Set("Subscription-Userinfo", "upload=1; download=1; total=0; expire=0")
			_, _ = w.Write([]byte("vless://a@h:443#x"))
		case tunPath + "abc", tunPath + "solo":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Profile-Web-Page-Url", "https://the-real-server:2096/s/abc")
			w.Header().Set("Subscription-Userinfo", "upload=5; download=5; total=0; expire=0")
			_, _ = w.Write([]byte(tunUpstreamJSON))
		case tunPath + "nobody", tunPath + "bob":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte("[]"))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Error!"))
		}
	}))
	t.Cleanup(u.Close)
	return u
}

// tunSubServer is a hop whose document points at upstream, with tunPath as
// the document says (possibly none, as from a panel older than /tun).
func tunSubServer(t *testing.T, upstream *tunUpstream, tunPath string) *SubServer {
	t.Helper()
	host, port := hostPort(t, upstream.URL)
	state := NewState()
	document := testDocument(42)
	document.NextHop = chain.NextHop{Host: host, SubPort: port, SubScheme: "http", SubPath: "/s/", JsonPath: "/j/", TunPath: tunPath}
	state.SetDocument(document)
	return testSubServer(t, &Config{NextHop: NextHop{Host: "10.0.0.7"}, Domain: "edge.example.com"}, state)
}

func serveTun(s *SubServer, path, accept string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestTun_ProxiedFromTheNextHop: /tun goes through a hop as /json does, the
// page address rewritten to the hop's own; an empty answer passes as it is
// (docs/spec/tunnel-subscription.md §7).
func TestTun_ProxiedFromTheNextHop(t *testing.T) {
	upstream := newTunUpstream(t, "/t/")
	s := tunSubServer(t, upstream, "/t/")

	w := serveTun(s, "/t/abc", "text/html")
	if w.Code != http.StatusOK || w.Body.String() != tunUpstreamJSON {
		t.Fatalf("/t/abc: %d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if got, want := w.Header().Get("Profile-Web-Page-Url"), "http://edge.example.com:2096/s/abc"; got != want {
		t.Errorf("Profile-Web-Page-Url = %q, want %q", got, want)
	}
	if got := w.Header().Get("Subscription-Userinfo"); got != "upload=5; download=5; total=0; expire=0" {
		t.Errorf("Subscription-Userinfo = %q", got)
	}
	if len(upstream.asked) != 1 || upstream.asked[0] != "/t/abc" {
		t.Errorf("upstream was asked for %v, want the document's tunPath", upstream.asked)
	}

	w = serveTun(s, "/t/nobody", "")
	if w.Code != http.StatusOK || w.Body.String() != "[]" {
		t.Errorf("/t/nobody: %d %q, want 200 []", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Profile-Web-Page-Url"); got != "" {
		t.Errorf("an empty answer got Profile-Web-Page-Url %q", got)
	}
}

// TestTun_DefaultPathForAnOlderDocument: a document from a panel that did not
// name tunPath yet leaves the hop on the default /tun/.
func TestTun_DefaultPathForAnOlderDocument(t *testing.T) {
	upstream := newTunUpstream(t, "/tun/")
	s := tunSubServer(t, upstream, "")
	if w := serveTun(s, "/tun/abc", ""); w.Code != http.StatusOK || w.Body.String() != tunUpstreamJSON {
		t.Errorf("/tun/abc: %d %q", w.Code, w.Body.String())
	}
}

// TestTun_NextHopFailures: a refusal passes through, a failure is a 502.
func TestTun_NextHopFailures(t *testing.T) {
	upstream := newTunUpstream(t, "/t/")
	s := tunSubServer(t, upstream, "/t/")
	upstream.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found"))
	})
	if w := serveTun(s, "/t/abc", ""); w.Code != http.StatusNotFound {
		t.Errorf("next hop 404: got %d", w.Code)
	}
	upstream.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if w := serveTun(s, "/t/abc", ""); w.Code != http.StatusBadGateway {
		t.Errorf("next hop 500: got %d, want 502", w.Code)
	}
}

// TestTun_OnThePage: the hop's subscription page shows the tunnels under the
// configs, and a subscription of tunnels alone still has a page there.
func TestTun_OnThePage(t *testing.T) {
	upstream := newTunUpstream(t, "/t/")
	s := tunSubServer(t, upstream, "/t/")

	for _, subId := range []string{"abc", "solo"} {
		w := serveTun(s, "/s/"+subId, "text/html")
		if w.Code != http.StatusOK {
			t.Fatalf("%s page: %d %q", subId, w.Code, w.Body.String())
		}
		page := w.Body.String()
		for _, want := range []string{"Tunnels", "ivan-phone", "AmneziaWG", "[Interface]", "Endpoint = edge.example.com:51820",
			`download="ivan-phone.conf"`, `alt="ivan-phone QR"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s page lacks %q", subId, want)
			}
		}
		if subId == "solo" && strings.Contains(page, "Copy subscription") {
			t.Errorf("a page of tunnels alone offers the xray subscription")
		}
	}

	// No tunnels: the section says so.
	if page := serveTun(s, "/s/bob", "text/html").Body.String(); !strings.Contains(page, "No tunnel configs in this subscription") {
		t.Errorf("page of xray links alone does not say it has no tunnels")
	}
	// A browser asking for an unknown subscription still gets the refusal.
	if w := serveTun(s, "/s/nobody", "text/html"); w.Code != http.StatusBadRequest {
		t.Errorf("page of an unknown subscription: %d, want 400", w.Code)
	}
}

// TestFrontPassesTheTunnelPath: the front's HTTP side hands /tun to the sub
// server with the other subscription paths.
func TestFrontPassesTheTunnelPath(t *testing.T) {
	doc := edgeFrontDocument()
	if got := frontSubPaths(doc); !containsString(got, "/tun/") {
		t.Errorf("front sub paths = %v, want /tun/", got)
	}
	doc.NextHop.TunPath = ""
	if got := frontSubPaths(doc); !containsString(got, "/tun/") {
		t.Errorf("front sub paths for an older document = %v, want the default /tun/", got)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
