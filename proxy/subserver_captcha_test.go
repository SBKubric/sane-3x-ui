package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// The captcha on a hop (#220, docs/spec/users.md §12): the active edge
// serves the page and its widget itself, under the subscription path of the
// document, and passes the challenge and the verification on to its next
// hop, as it passes subscriptions — the panel on real answers them in the
// end.

type seenRequest struct {
	method, path, contentType, body string
}

// captchaHop is a hop whose next hop is upstream, under the paths /s/ and /j/.
func captchaHop(t *testing.T, upstream *httptest.Server) *SubServer {
	t.Helper()
	host, port := hostPort(t, upstream.URL)
	state := NewState()
	state.SetDocument(&chain.Document{
		Version:  chain.DocumentVersion,
		Revision: 1,
		Self:     chain.Self{Name: "edge-a", Role: chain.RoleEdge, Host: "edge.example.com"},
		NextHop:  chain.NextHop{Host: host, SubPort: port, SubScheme: "http", SubPath: "/s/", JsonPath: "/j/"},
	})
	return testSubServer(t, &Config{NextHop: NextHop{Host: "10.0.0.7"}}, state)
}

func serve(s *SubServer, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestHopServesTheCaptchaPageItself: the page and the widget come from the
// hop, under the document's subscription path, and name their calls there;
// the next hop is not asked.
func TestHopServesTheCaptchaPageItself(t *testing.T) {
	var mu sync.Mutex
	var seen []seenRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, seenRequest{method: r.Method, path: r.URL.Path})
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()
	s := captchaHop(t, upstream)

	page := serve(s, http.MethodGet, "/s/captcha", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `challengeurl="/s/captcha/challenge"`) {
		t.Fatalf("page: %d %.200q", page.Code, page.Body.String())
	}
	widget := serve(s, http.MethodGet, "/s/captcha/altcha.js", "")
	if widget.Code != http.StatusOK || !strings.Contains(widget.Body.String(), "altcha-widget") {
		t.Fatalf("widget: %d", widget.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 0 {
		t.Errorf("the next hop was asked: %+v", seen)
	}
}

// TestHopPassesTheCaptchaCallsOn: the challenge and the verification go to
// the next hop under the same path, the body as it came; the next hop's
// answer — a refusal too — comes back as it is.
func TestHopPassesTheCaptchaCallsOn(t *testing.T) {
	var mu sync.Mutex
	var seen []seenRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/s/captcha/challenge":
			_, _ = io.WriteString(w, `{"algorithm":"SHA-256","challenge":"c","maxNumber":10,"salt":"s","signature":"x"}`)
		case "/s/captcha/verify":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"ok":false}`)
		}
	}))
	defer upstream.Close()
	s := captchaHop(t, upstream)

	challenge := serve(s, http.MethodGet, "/s/captcha/challenge", "")
	if challenge.Code != http.StatusOK || !strings.Contains(challenge.Body.String(), `"signature":"x"`) ||
		challenge.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("challenge: %d %v %q", challenge.Code, challenge.Header(), challenge.Body.String())
	}
	verify := serve(s, http.MethodPost, "/s/captcha/verify", `{"initData":"a=1","payload":"p"}`)
	if verify.Code != http.StatusForbidden || verify.Body.String() != `{"ok":false}` ||
		!strings.HasPrefix(verify.Header().Get("Content-Type"), "application/json") {
		t.Errorf("verify: %d %v %q", verify.Code, verify.Header(), verify.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	want := []seenRequest{{method: "GET", path: "/s/captcha/challenge"},
		{method: "POST", path: "/s/captcha/verify", contentType: "application/json", body: `{"initData":"a=1","payload":"p"}`}}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("the next hop saw %+v", seen)
	}
}

// TestHopCaptchaWaitsForTheDocument: before its first document a hop knows
// neither its next hop nor its paths: the captcha is 503, as subscriptions
// are, and a wrong method on its calls is refused.
func TestHopCaptchaWaitsForTheDocument(t *testing.T) {
	s := testSubServer(t, &Config{NextHop: NextHop{Host: "1.2.3.4"}}, NewState())
	for _, path := range []string{"/sub/captcha", "/sub/captcha/challenge"} {
		if w := serve(s, http.MethodGet, path, ""); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s before the document: %d", path, w.Code)
		}
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the next hop was asked %s %s", r.Method, r.URL.Path)
	}))
	defer upstream.Close()
	hop := captchaHop(t, upstream)
	for method, path := range map[string]string{http.MethodPost: "/s/captcha/challenge", http.MethodGet: "/s/captcha/verify"} {
		if w := serve(hop, method, path, ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", method, path, w.Code)
		}
	}
}
