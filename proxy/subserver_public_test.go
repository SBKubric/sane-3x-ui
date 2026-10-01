package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// The public subscription address (#224) on a hop: the subscription
// showcase passes the panel's headers through as they are, and a client
// reaches the hop through it, so the hop names the address the panel hands
// out — the document's publicSubUrl — in its links and its
// Profile-Web-Page-Url instead of itself.

// TestTheHopNamesThePublicSubAddress: without the address the hop names
// itself, as before; with it the page's link, its QR and the
// Profile-Web-Page-Url of /sub, /json and /tun start with it, on the
// document's paths.
func TestTheHopNamesThePublicSubAddress(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Profile-Web-Page-Url", "https://sub.example.com/s/abc")
		w.Header().Set("Subscription-Userinfo", "upload=1; download=1; total=0; expire=0")
		if strings.HasPrefix(r.URL.Path, "/t/") {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write([]byte("vless://a@h:443#x"))
	}))
	defer upstream.Close()
	host, port := hostPort(t, upstream.URL)

	for _, tc := range []struct {
		name, public, origin string
	}{
		{"without the address", "", "http://edge.example.com:2096"},
		{"with the address", "https://sub.example.com", "https://sub.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState()
			state.SetDocument(&chain.Document{
				Version: chain.DocumentVersion, Revision: 7,
				Self:         chain.Self{Name: "edge-a", Role: chain.RoleEdge, Host: "edge.example.com"},
				NextHop:      chain.NextHop{Host: host, SubPort: port, SubScheme: "http", SubPath: "/s/", JsonPath: "/j/", TunPath: "/t/"},
				PublicSubURL: tc.public,
			})
			s := testSubServer(t, &Config{NextHop: NextHop{Host: "192.0.2.7"}, Domain: "edge.example.com"}, state)

			for _, path := range []string{"/s/abc", "/j/abc", "/t/abc"} {
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d %q", path, w.Code, w.Body.String())
				}
				if got, want := w.Header().Get("Profile-Web-Page-Url"), tc.origin+"/s/abc"; got != want {
					t.Errorf("%s: Profile-Web-Page-Url = %q, want %q", path, got, want)
				}
			}

			req := httptest.NewRequest(http.MethodGet, "/s/abc", nil)
			req.Header.Set("Accept", "text/html")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			body := w.Body.String()
			if !strings.Contains(body, tc.origin+"/s/abc") {
				t.Errorf("the page lacks %q", tc.origin+"/s/abc")
			}
			// The JSON configs are on the page itself (#231): no JSON link.
			if strings.Contains(body, "/j/abc") {
				t.Error("the page links the JSON path")
			}
			if tc.public != "" && strings.Contains(body, "edge.example.com") {
				t.Error("the page still names the hop")
			}
		})
	}
}

// TestTruncateDocumentKeepsThePublicSubAddress: an edge behind an inner
// learns the address from the inner's truncation, as it learns the paths.
func TestTruncateDocumentKeepsThePublicSubAddress(t *testing.T) {
	cfg := &Config{Domain: "192.0.2.7", SubPort: 2096}
	doc := innerDocument()
	doc.PublicSubURL = "https://sub.example.com"
	for _, hop := range doc.Hops[1:] {
		if out := TruncateDocument(doc, hop, cfg); out.PublicSubURL != "https://sub.example.com" {
			t.Errorf("%s: publicSubUrl %q", hop.Name, out.PublicSubURL)
		}
	}
}

// TestDocumentDiffersOnThePublicSubAddress: setting the address does not
// move the revision, and the hop still takes the new document.
func TestDocumentDiffersOnThePublicSubAddress(t *testing.T) {
	a, b := testDocument(42), testDocument(42)
	b.PublicSubURL = "https://sub.example.com"
	if !documentDiffers(a, b) {
		t.Error("a document with a new public subscription address is taken for the same one")
	}
}
