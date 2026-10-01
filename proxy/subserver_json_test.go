package proxy

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// The client JSON config of every link on the hop's subscription page
// (#231): fetched from the next hop's subscription path at render and
// embedded, never linked, since the JSON path is served only while the panel's
// JSON subscription is on and the showcase does not pass it at all.

const (
	jsonPageLinks = "vless://a@edge.example.com:443?type=tcp#nl\nvless://b@edge.example.com:8443?type=tcp#de"
	jsonPageList  = `[
  {
    "outbounds": [{"protocol": "vless", "tag": "proxy", "settings": {"vnext": [{"address": "edge.example.com", "port": 443}]}}],
    "remarks": "nl"
  },
  {
    "outbounds": [{"protocol": "vless", "tag": "proxy", "settings": {"vnext": [{"address": "edge.example.com", "port": 8443}]}}],
    "remarks": "de"
  }
]`
)

// jsonUpstream is a next hop with the JSON subscription off: /s/ answers
// the links, or with ?format=json the list of their configs when list is
// set (an older panel ignores the query and answers the links again).
func jsonUpstream(t *testing.T, list bool) (*SubServer, *[]string) {
	t.Helper()
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		if r.URL.Path != "/s/abc" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=1; download=1; total=0; expire=0")
		if list && r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(jsonPageList))
			return
		}
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(jsonPageLinks))))
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
	return testSubServer(t, &Config{NextHop: NextHop{Host: "192.0.2.7"}, Domain: "edge.example.com"}, state), &seen
}

func hopGet(s *SubServer, path, accept string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	s.Handler().ServeHTTP(w, r)
	return w
}

// pageConfigJSONs are the configs embedded in the hop's page, in page order.
func pageConfigJSONs(t *testing.T, page string) []string {
	t.Helper()
	const open = `data-role="json">`
	var out []string
	for rest := page; ; {
		i := strings.Index(rest, open)
		if i < 0 {
			return out
		}
		rest = rest[i+len(open):]
		out = append(out, html.UnescapeString(rest[:strings.Index(rest, "</code>")]))
	}
}

func TestTheHopPageCarriesTheJSONOfEveryLink(t *testing.T) {
	s, _ := jsonUpstream(t, true)
	w := hopGet(s, "/s/abc", "text/html")
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	page := w.Body.String()
	for _, path := range []string{"/j/abc", fallbackJsonPath + "abc"} {
		if strings.Contains(page, path) {
			t.Errorf("the page links the JSON path %s", path)
		}
	}

	var want []json.RawMessage
	if err := json.Unmarshal([]byte(jsonPageList), &want); err != nil {
		t.Fatal(err)
	}
	got := pageConfigJSONs(t, page)
	if len(got) != len(want) {
		t.Fatalf("page carries %d configs, want %d:\n%s", len(got), len(want), page)
	}
	for i := range want {
		var g, w any
		if err := json.Unmarshal([]byte(got[i]), &g); err != nil {
			t.Fatalf("config %d is not JSON: %v\n%s", i, err, got[i])
		}
		_ = json.Unmarshal(want[i], &w)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("config %d = %s, want the next hop's %s", i, got[i], want[i])
		}
	}
	if n := strings.Count(page, `data-testid="sub-copy-json"`); n != 2 {
		t.Errorf("page has %d Copy JSON buttons, want one per link", n)
	}
}

// TestTheHopPassesTheJSONListOn: the hop before the edge answers the
// edge's ?format=json from its own next hop, so the list crosses the chain.
func TestTheHopPassesTheJSONListOn(t *testing.T) {
	s, seen := jsonUpstream(t, true)
	w := hopGet(s, "/s/abc?format=json", "")
	if w.Code != http.StatusOK || w.Body.String() != jsonPageList {
		t.Fatalf("format=json: %d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := *seen; len(got) != 1 || got[0] != "/s/abc?format=json" {
		t.Errorf("next hop saw %v", got)
	}
}

// TestTheHopPageOfAnOlderPanel: a next hop that does not know the list
// answers the links again; the page offers the links alone.
func TestTheHopPageOfAnOlderPanel(t *testing.T) {
	s, _ := jsonUpstream(t, false)
	w := hopGet(s, "/s/abc", "text/html")
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `data-testid="sub-copy-json"`) {
		t.Error("the page offers JSON its next hop never gave")
	}
	if !strings.Contains(w.Body.String(), "vless://b@edge.example.com:8443") {
		t.Error("the page lost a link")
	}
}
