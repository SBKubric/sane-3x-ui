package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// The bot's own path on a hop (#220, docs/spec/users.md §12): everything
// under the document's nextHop.thirdPartyPath — the captcha page, its
// widget, its challenge and verification — goes to the next hop as it came,
// and the answer comes back as it is. The subscription path has no captcha
// any more.

const testBotPath = "/third-party/s3cr3t/"

type seenRequest struct {
	method, path, query, contentType, body string
}

// botHop is a hop whose next hop is upstream, with the subscriptions under
// /s/ and /j/ and the bot's path testBotPath.
func botHop(t *testing.T, upstream *httptest.Server) *SubServer {
	t.Helper()
	host, port := hostPort(t, upstream.URL)
	state := NewState()
	state.SetDocument(&chain.Document{
		Version:  chain.DocumentVersion,
		Revision: 1,
		Self:     chain.Self{Name: "edge-a", Role: chain.RoleEdge, Host: "edge.example.com"},
		NextHop: chain.NextHop{Host: host, SubPort: port, SubScheme: "http", SubPath: "/s/", JsonPath: "/j/",
			ThirdPartyPath: testBotPath},
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

// recordingUpstream is a next hop that records what it is asked and answers
// by answer.
func recordingUpstream(t *testing.T, answer http.HandlerFunc) (*httptest.Server, func() []seenRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []seenRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"), body: string(body)})
		mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(upstream.Close)
	return upstream, func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// TestHopPassesTheBotPathOn: the page, the widget, the challenge and the
// verification under the bot's path go to the next hop under the same
// path, the query and the body as they came; the next hop's answer — its
// status, its type, its caching, a refusal too — comes back as it is.
func TestHopPassesTheBotPathOn(t *testing.T) {
	upstream, seen := recordingUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testBotPath + "captcha":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(w, "<altcha-widget>")
		case testBotPath + "captcha/altcha.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, strings.Repeat("x", 70<<10))
		case testBotPath + "captcha/challenge":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = io.WriteString(w, `{"signature":"x"}`)
		case testBotPath + "captcha/verify":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"ok":false}`)
		}
	})
	s := botHop(t, upstream)

	page := serve(s, http.MethodGet, testBotPath+"captcha?x=1", "")
	if page.Code != http.StatusOK || page.Body.String() != "<altcha-widget>" ||
		!strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") || page.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("page: %d %v %q", page.Code, page.Header(), page.Body.String())
	}
	if widget := serve(s, http.MethodGet, testBotPath+"captcha/altcha.js", ""); widget.Code != http.StatusOK || widget.Body.Len() != 70<<10 {
		t.Errorf("widget: %d, %d bytes", widget.Code, widget.Body.Len())
	}
	challenge := serve(s, http.MethodPost, testBotPath+"captcha/challenge", `{"initData":"a=1"}`)
	if challenge.Code != http.StatusOK || challenge.Body.String() != `{"signature":"x"}` {
		t.Errorf("challenge: %d %q", challenge.Code, challenge.Body.String())
	}
	verify := serve(s, http.MethodPost, testBotPath+"captcha/verify", `{"initData":"a=1","payload":"p"}`)
	if verify.Code != http.StatusForbidden || verify.Body.String() != `{"ok":false}` ||
		!strings.HasPrefix(verify.Header().Get("Content-Type"), "application/json") {
		t.Errorf("verify: %d %v %q", verify.Code, verify.Header(), verify.Body.String())
	}

	want := []seenRequest{
		{method: "GET", path: testBotPath + "captcha", query: "x=1"},
		{method: "GET", path: testBotPath + "captcha/altcha.js"},
		{method: "POST", path: testBotPath + "captcha/challenge", contentType: "application/json", body: `{"initData":"a=1"}`},
		{method: "POST", path: testBotPath + "captcha/verify", contentType: "application/json", body: `{"initData":"a=1","payload":"p"}`},
	}
	if got := seen(); !slices.Equal(got, want) {
		t.Errorf("the next hop saw\n%+v\nwant\n%+v", got, want)
	}
}

// TestHopBotPathOnlyWithTheDocument: a hop with no document, or with a
// document from a panel that names no bot path, does not pass the path on;
// another secret is not the bot's path; a method other than GET and POST is
// refused; and the old captcha under the subscription path is gone — it is
// a subscription id like any other.
func TestHopBotPathOnlyWithTheDocument(t *testing.T) {
	upstream, seen := recordingUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // the panel's answer to an unknown subscription
	})

	none := testSubServer(t, &Config{NextHop: NextHop{Host: "1.2.3.4"}}, NewState())
	if w := serve(none, http.MethodGet, testBotPath+"captcha", ""); w.Code != http.StatusNotFound {
		t.Errorf("before the document: %d", w.Code)
	}

	old := botHop(t, upstream)
	doc := *old.state.Document()
	doc.NextHop.ThirdPartyPath = ""
	old.state.SetDocument(&doc)
	if w := serve(old, http.MethodGet, testBotPath+"captcha", ""); w.Code != http.StatusNotFound {
		t.Errorf("a document with no bot path: %d", w.Code)
	}

	hop := botHop(t, upstream)
	if w := serve(hop, http.MethodGet, "/third-party/other/captcha", ""); w.Code != http.StatusNotFound {
		t.Errorf("another secret: %d", w.Code)
	}
	if w := serve(hop, http.MethodDelete, testBotPath+"captcha", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d", w.Code)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("the next hop was asked: %+v", got)
	}

	// The subscription path's captcha of before: a subscription id now,
	// fetched from the next hop as one and refused there as one.
	w := serve(hop, http.MethodGet, "/s/captcha", "")
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "altcha") {
		t.Errorf("/s/captcha: %d %q", w.Code, w.Body.String())
	}
	if got := seen(); len(got) != 1 || got[0].path != "/s/captcha" {
		t.Errorf("the next hop saw %+v", got)
	}
}
