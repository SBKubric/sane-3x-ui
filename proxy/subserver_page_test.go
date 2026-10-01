package proxy

import (
	"encoding/base64"
	"html"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/subpage"
)

// The hop's subscription page is the panel's (#235): the same template, the
// owner's app list taken from the next hop's subscription path.

const ownersAppList = `[
  {
    "name": "Owner App",
    "platform": "Android",
    "url": "https://example.com/owner-app",
    "protocols": ["VLESS + XHTTP"]
  }
]`

// appsUpstream is a next hop that answers ?format=apps with ownersAppList
// when apps is set, and the links to anything else.
func appsUpstream(t *testing.T, apps bool) (*SubServer, *[]string) {
	t.Helper()
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		if r.URL.Path != "/s/abc" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Error!"))
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=1048576; download=1048576; total=0; expire=0")
		w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Example VPN")))
		if apps && r.URL.Query().Get("format") == "apps" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(ownersAppList))
			return
		}
		_, _ = w.Write([]byte("vless://a@edge.example.com:443?type=xhttp&path=%2Fapi#nl"))
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

// hopPageApps are the app cards of a hop page: name, URL and labels.
func hopPageApps(page string) []subpage.App {
	var apps []subpage.App
	for _, part := range strings.Split(page, `data-testid="sub-app"`)[1:] {
		part = part[:strings.Index(part, "</a>")]
		app := subpage.App{Protocols: []string{}}
		if m := regexp.MustCompile(`<strong>(.*?)</strong>`).FindStringSubmatch(part); m != nil {
			app.Name = m[1]
		}
		if m := regexp.MustCompile(`<small>(.*?)</small>`).FindStringSubmatch(part); m != nil {
			app.Platform = m[1]
		}
		if m := regexp.MustCompile(`href="(.*?)"`).FindStringSubmatch(part); m != nil {
			app.URL = m[1]
		}
		for _, m := range regexp.MustCompile(`data-testid="sub-app-label">(.*?)<`).FindAllStringSubmatch(part, -1) {
			app.Protocols = append(app.Protocols, html.UnescapeString(m[1]))
		}
		apps = append(apps, app)
	}
	return apps
}

func TestTheHopPageHasTheOwnersApps(t *testing.T) {
	s, _ := appsUpstream(t, true)
	w := hopGet(s, "/s/abc", "text/html")
	if w.Code != http.StatusOK {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	page := html.UnescapeString(w.Body.String())
	want := []subpage.App{{Name: "Owner App", Platform: "Android", URL: "https://example.com/owner-app", Protocols: []string{"VLESS + XHTTP"}}}
	if got := hopPageApps(page); !reflect.DeepEqual(got, want) {
		t.Errorf("page apps = %+v, want %+v", got, want)
	}
	for _, want := range []string{`data-testid="sub-warning"`, `data-testid="sub-label">VLESS + XHTTP<`, "Example VPN", "2.00MB", `data-role="sub">http://edge.example.com:2096/s/abc</code>`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// TestTheHopPageOfAPanelWithoutTheAppList: a next hop older than the list
// answers ?format=apps with the links; the page shows the built-in list.
func TestTheHopPageOfAPanelWithoutTheAppList(t *testing.T) {
	s, _ := appsUpstream(t, false)
	page := hopGet(s, "/s/abc", "text/html").Body.String()
	if got := hopPageApps(page); !reflect.DeepEqual(got, subpage.DefaultApps()) {
		t.Errorf("page apps = %+v, want the built-in list", got)
	}
}

// TestTheHopPassesTheAppListOn: the hop before the edge answers the edge's
// ?format=apps from its own next hop, so the list crosses the chain.
func TestTheHopPassesTheAppListOn(t *testing.T) {
	s, seen := appsUpstream(t, true)
	w := hopGet(s, "/s/abc?format=apps", "")
	if w.Code != http.StatusOK || w.Body.String() != ownersAppList {
		t.Fatalf("format=apps: %d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := *seen; len(got) != 1 || got[0] != "/s/abc?format=apps" {
		t.Errorf("next hop saw %v", got)
	}
	if w := hopGet(s, "/s/nobody?format=apps", ""); w.Code != http.StatusBadRequest {
		t.Errorf("unknown subscription: %d, want the next hop's 400", w.Code)
	}
}
