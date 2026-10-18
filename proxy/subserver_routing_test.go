package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// The client routing through the chain (#263): the panel answers the
// routing header by the app's User-Agent, so a hop fetches the subscription
// with the User-Agent of the app that asked, and passes the routing header
// and Vary back.

// routingUpstream is a panel that answers each User-Agent with its own
// routing header, recording the User-Agents it saw per path.
func routingUpstream(t *testing.T) (*SubServer, func(path string) []string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string][]string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = append(seen[r.URL.Path], r.Header.Get("User-Agent"))
		mu.Unlock()
		w.Header().Set("Subscription-Userinfo", "upload=1; download=1; total=0; expire=0")
		w.Header().Add("Vary", "User-Agent")
		w.Header().Set("Routing", "for "+r.Header.Get("User-Agent"))
		if r.URL.Path == "/j/abc" {
			_, _ = w.Write([]byte(`{"outbounds":[]}`))
			return
		}
		_, _ = w.Write([]byte("vless://a@h:443#x"))
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
	s := testSubServer(t, &Config{NextHop: NextHop{Host: "10.0.0.7"}, Domain: "edge.example.com"}, state)
	return s, func(path string) []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen[path]...)
	}
}

func TestEdgeFetchesTheSubscriptionAsTheApp(t *testing.T) {
	s, seen := routingUpstream(t)
	for _, tc := range []struct{ path, ua string }{
		{"/s/abc", "Happ/3.13.0"},
		{"/s/abc", "v2raytun/android"},
		{"/j/abc", "Happ/3.13.0"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("User-Agent", tc.ua)
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s as %q: %d %s", tc.path, tc.ua, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Routing"); got != "for "+tc.ua {
			t.Errorf("%s as %q: Routing %q", tc.path, tc.ua, got)
		}
		if got := w.Header().Get("Vary"); got != "User-Agent" {
			t.Errorf("%s as %q: Vary %q", tc.path, tc.ua, got)
		}
	}
	if got := seen("/s/abc"); len(got) != 2 || got[0] != "Happ/3.13.0" || got[1] != "v2raytun/android" {
		t.Errorf("the panel saw %q on /s/abc", got)
	}
	if got := seen("/j/abc"); len(got) != 1 || got[0] != "Happ/3.13.0" {
		t.Errorf("the panel saw %q on /j/abc", got)
	}
}

// TestEdgePageFetchesAsItself: the page is the hop's own and carries no
// routing, so the browser's User-Agent does not go upstream.
func TestEdgePageFetchesAsItself(t *testing.T) {
	s, seen := routingUpstream(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/s/abc", nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("User-Agent", "Mozilla/5.0 Happ-lookalike")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d", w.Code)
	}
	for _, ua := range seen("/s/abc") {
		if ua == "Mozilla/5.0 Happ-lookalike" {
			t.Errorf("the page fetch went upstream as the browser")
		}
	}
}
