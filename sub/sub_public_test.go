package sub

import (
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The public subscription address (#224) on the panel's sub server: the
// links of the subscription page and the Profile-Web-Page-Url of /sub,
// /json, /clash and /tun start with it; the paths are the panel's own.

// pageLinks are the subscription and JSON links the page was rendered with.
func pageLinks(t *testing.T, body string) (sub, json string) {
	t.Helper()
	attr := func(name string) string {
		i := strings.Index(body, name+`="`)
		if i < 0 {
			t.Fatalf("the page has no %s:\n%s", name, body)
		}
		rest := body[i+len(name)+2:]
		return html.UnescapeString(rest[:strings.Index(rest, `"`)])
	}
	return attr("data-sub-url"), attr("data-subjson-url")
}

func TestSubServerLinksGoThroughThePublicAddress(t *testing.T) {
	settings := map[string]string{"subJsonEnable": "true", "subJsonPath": "/js/", "subClashEnable": "true", "subClashPath": "/cl/"}
	for _, tc := range []struct {
		name   string
		public string
		origin string
	}{
		{"without the address", "", "http://panel.example.com:2096"},
		{"with the address", "https://sub.example.com", "https://sub.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			all := map[string]string{"subPublicURL": tc.public}
			for k, v := range settings {
				all[k] = v
			}
			engine := newTunTestServer(t, all)
			storeVlessClient(t, 1, "ivan-nl", "pub-ivan")
			addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "pub-ivan")
			page := tc.origin + "/sub/pub-ivan"

			rec := getTun(t, engine, "/sub/pub-ivan", browser)
			if rec.Code != http.StatusOK {
				t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
			}
			if sub, json := pageLinks(t, rec.Body.String()); sub != page || json != tc.origin+"/js/pub-ivan" {
				t.Errorf("page links: %q %q", sub, json)
			}

			for _, path := range []string{"/sub/pub-ivan", "/js/pub-ivan", "/cl/pub-ivan", "/tun/pub-ivan"} {
				rec := getTun(t, engine, path, nil)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
				}
				// /sub, /json and /clash name themselves, /tun the page.
				want := tc.origin + path
				if path == "/tun/pub-ivan" {
					want = page
				}
				if got := rec.Header().Get("Profile-Web-Page-Url"); got != want {
					t.Errorf("%s: Profile-Web-Page-Url = %q, want %q", path, got, want)
				}
			}
		})
	}
}

// TestSubServerProfileURLStillWins: an owner's own profile URL is what the
// apps get, with the public address or without.
func TestSubServerProfileURLStillWins(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subPublicURL": "https://sub.example.com", "subProfileUrl": "https://help.example.com/"})
	storeVlessClient(t, 1, "ivan-nl", "pub-ivan")
	if got := getTun(t, engine, "/sub/pub-ivan", nil).Header().Get("Profile-Web-Page-Url"); got != "https://help.example.com/" {
		t.Errorf("Profile-Web-Page-Url = %q", got)
	}
}
