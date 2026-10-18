package proxy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// joinAnswer is what the panel returns to a successful join (§4.3).
func joinAnswer() JoinResult {
	return JoinResult{
		HopId:       4,
		Name:        "edge-b",
		Secret:      "the-issued-hop-secret",
		PollSeconds: 30,
		Document: chain.Document{
			Version:  chain.DocumentVersion,
			Revision: 43,
			Self:     chain.Self{Name: "edge-b", Role: chain.RoleEdge, Host: "b.example.net"},
			NextHop:  chain.NextHop{Host: "10.0.0.7", SubPort: 2096, SubScheme: "https", SubPath: "/sub/", JsonPath: "/json/"},
			Hops:     []chain.Hop{{Name: "edge-b", Role: chain.RoleEdge, SecretHash: chain.HashSecret("the-issued-hop-secret"), State: chain.StateJoined}},
			Ports:    []chain.Port{{Port: 443, Network: chain.NetworkTCPUDP, Tag: "inbound-443", Source: chain.SourceXray}},
		},
	}
}

// bootstrapBox is a box that has a config file but no hop secret yet.
func bootstrapBox(t *testing.T, nextHopURL string) (*JoinPage, *Config, *State, *DocumentStore) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "proxy.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":2,"domain":"b.example.net","subPort":2096}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = filepath.Join(dir, "chain")
	if nextHopURL != "" {
		host, port := hostPort(t, nextHopURL)
		cfg.NextHop = NextHop{Host: host, SubPort: port, SubScheme: "http"}
	}

	state := NewState()
	store := NewDocumentStore(cfg.DocumentPath())
	page, err := NewJoinPage(cfg, state, store)
	if err != nil {
		t.Fatalf("NewJoinPage: %v", err)
	}
	return page, cfg, state, store
}

func postJoinForm(page *JoinPage, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, page.Path(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	page.Handler().ServeHTTP(w, req)
	return w
}

// TestJoinPageIsOnlyAtItsSecretPath keeps ADR 0001's shape: a one-time random
// path, nothing else served.
func TestJoinPageIsOnlyAtItsSecretPath(t *testing.T) {
	page, _, _, _ := bootstrapBox(t, "")
	if len(page.Token()) < 32 {
		t.Fatalf("token too short: %q", page.Token())
	}
	for _, path := range []string{"/", "/join/not-the-token"} {
		w := httptest.NewRecorder()
		page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="token"`) {
		t.Fatalf("join page: status %d body %q", w.Code, w.Body.String())
	}
}

// TestJoinPageWarnsOverPlainHTTP: without a certificate the join token would
// travel in clear text. The page still comes up — during an install it is
// often the only channel its owner has — but it says so (§5.4).
func TestJoinPageWarnsOverPlainHTTP(t *testing.T) {
	page, cfg, _, _ := bootstrapBox(t, "")

	w := httptest.NewRecorder()
	page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	if !strings.Contains(w.Body.String(), "plain HTTP") {
		t.Error("no warning banner on a page served without TLS")
	}
	if !strings.HasPrefix(page.URL(), "http://") {
		t.Errorf("URL without TLS = %q", page.URL())
	}

	cfg.CertFile, cfg.KeyFile = "c.pem", "k.pem"
	w = httptest.NewRecorder()
	page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	if strings.Contains(w.Body.String(), "plain HTTP") {
		t.Error("the warning banner survived a certificate")
	}
	if want := "https://b.example.net:2096" + page.Path(); page.URL() != want {
		t.Errorf("URL with TLS = %q, want %q", page.URL(), want)
	}

	// The scheme's default port must not appear: it adds nothing but a
	// mismatch with what the owner will actually type (#98).
	cfg.SubPort = 443
	if want := "https://b.example.net" + page.Path(); page.URL() != want {
		t.Errorf("URL with TLS and the default port = %q, want %q", page.URL(), want)
	}
}

// TestJoinPageJoinsOnceAndGoesDark is the whole join flow of §5.4: the form
// reaches the next hop, the answer lands in proxy.json and document.json, and
// the page is spent.
func TestJoinPageJoinsOnceAndGoesDark(t *testing.T) {
	var gotRequest joinRequest
	nextHop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ChainPathPrefix+"/join" {
			t.Errorf("join posted to %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("join body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(joinAnswer())
	}))
	defer nextHop.Close()

	page, cfg, state, store := bootstrapBox(t, nextHop.URL)
	w := postJoinForm(page, url.Values{"token": {"0123456789012345678901234567890a"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "edge-b") {
		t.Fatalf("join: status %d body %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "443/tcp,udp") {
		t.Errorf("the success page does not list the relayed ports: %q", w.Body.String())
	}

	// The box told the chain who it is, so the registry can compare the host
	// it holds with where the join came from (§4.4).
	if gotRequest.Token != "0123456789012345678901234567890a" || gotRequest.Host != "b.example.net" || gotRequest.SubPort != 2096 {
		t.Errorf("join request = %+v", gotRequest)
	}

	select {
	case <-page.Accepted():
	default:
		t.Fatal("Accepted() was not signalled")
	}
	if cfg.HopSecret != "the-issued-hop-secret" {
		t.Errorf("hopSecret = %q, want the issued one", cfg.HopSecret)
	}
	saved, err := LoadConfig(cfg.Path())
	if err != nil || saved.HopSecret != "the-issued-hop-secret" || saved.Bootstrap() {
		t.Fatalf("proxy.json after the join = %+v (%v)", saved, err)
	}
	if st, err := os.Stat(cfg.Path()); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("proxy.json mode = %v (%v), want 600", st.Mode().Perm(), err)
	}
	cached, err := store.Load()
	if err != nil || cached == nil || cached.Revision != 43 {
		t.Fatalf("document.json after the join = %+v (%v)", cached, err)
	}
	if state.Revision() != 43 {
		t.Errorf("the running box did not pick the document up: revision %d", state.Revision())
	}

	// One shot: the page is gone.
	w = httptest.NewRecorder()
	page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("the page is still up after a join: %d", w.Code)
	}
	if w := postJoinForm(page, url.Values{"token": {"0123456789012345678901234567890a"}}); w.Code != http.StatusNotFound {
		t.Errorf("a second join was accepted: %d", w.Code)
	}
}

// TestJoinPageSurvivesARefusal: a mistyped token or a next hop that is not up
// yet must be fixable from the same page — the page token is not spent by a
// failure (§5.4).
func TestJoinPageSurvivesARefusal(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer refusing.Close()

	page, cfg, _, _ := bootstrapBox(t, refusing.URL)
	w := postJoinForm(page, url.Values{"token": {"0123456789012345678901234567890a"}})
	if w.Code != http.StatusBadGateway {
		t.Errorf("a refused join: status %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unknown, expired, or already used") {
		t.Errorf("the refusal does not say what a bare 404 can mean: %q", w.Body.String())
	}
	if page.Spent() {
		t.Fatal("a refused join spent the page token")
	}
	if cfg.HopSecret != "" {
		t.Errorf("a refused join left a hop secret behind: %q", cfg.HopSecret)
	}
	if w := postJoinForm(page, url.Values{}); w.Code != http.StatusBadRequest {
		t.Errorf("an empty form: status %d, want 400", w.Code)
	}
}

// TestJoinPageAsksForTheNextHopWhenTheConfigHasNone: an installer that knew
// the next hop states it; a box installed blind asks — and a v1 config's
// upstreamHost is worth exactly one thing, the prefill (§5.3).
func TestJoinPageAsksForTheNextHopWhenTheConfigHasNone(t *testing.T) {
	var seenHost string
	nextHop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		_ = json.NewEncoder(w).Encode(joinAnswer())
	}))
	defer nextHop.Close()
	host, port := hostPort(t, nextHop.URL)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "proxy.json")
	if err := os.WriteFile(cfgPath, []byte(`{"upstreamHost":"`+host+`","relayManifestPath":"/x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = filepath.Join(dir, "chain")
	page, err := NewJoinPage(cfg, NewState(), NewDocumentStore(cfg.DocumentPath()))
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	body := w.Body.String()
	if !strings.Contains(body, `name="nextHopHost"`) {
		t.Fatal("the page does not ask for a next hop although the config names none")
	}
	if !strings.Contains(body, host) {
		t.Errorf("the legacy upstreamHost was not prefilled: %q", body)
	}

	w = postJoinForm(page, url.Values{
		"token":          {"0123456789012345678901234567890a"},
		"nextHopHost":    {host},
		"nextHopSubPort": {strconv.Itoa(port)},
		"nextHopScheme":  {"http"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("join with a typed next hop: status %d body %q", w.Code, w.Body.String())
	}
	if seenHost == "" {
		t.Error("the next hop was never called")
	}
	saved, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.NextHop.Host != host || saved.NextHop.SubPort != port || saved.NextHop.SubScheme != "http" {
		t.Errorf("the typed next hop was not saved: %+v", saved.NextHop)
	}
}

// TestJoinURLPathSitsNextToTheConfig — where the installer and
// `x-ui chain join-url` look for the link (§5.5).
func TestJoinURLPathSitsNextToTheConfig(t *testing.T) {
	if got, want := JoinURLPath("/etc/x-ui/proxy.json"), "/etc/x-ui/chain-join.url"; got != want {
		t.Errorf("JoinURLPath = %q, want %q", got, want)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "proxy.json")
	if _, err := ReadJoinURL(cfgPath); err == nil {
		t.Error("a box with no pending page reported a join URL")
	}
	if err := os.WriteFile(JoinURLPath(cfgPath), []byte("https://b.example.net:2096/join/abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	url, err := ReadJoinURL(cfgPath)
	if err != nil || url != "https://b.example.net:2096/join/abc" {
		t.Errorf("ReadJoinURL = %q, %v", url, err)
	}
}

// TestJoinPageStatesTheInstallersNextHopPort (#265): the installer writes the
// next hop's sub port it found or was told into proxy.json, and the join page
// of a box installed without a token joins through exactly that port — it
// states it rather than asking again.
func TestJoinPageStatesTheInstallersNextHopPort(t *testing.T) {
	joined := false
	nextHop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		joined = true
		_ = json.NewEncoder(w).Encode(joinAnswer())
	}))
	defer nextHop.Close()
	host, port := hostPort(t, nextHop.URL)

	page, _, _, _ := bootstrapBox(t, nextHop.URL)
	w := httptest.NewRecorder()
	page.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, page.Path(), nil))
	want := `data-testid="chain-join-nexthop">http://` + host + ":" + strconv.Itoa(port) + "<"
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("the page does not state the configured next hop %s:\n%s", want, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `name="nextHopSubPort"`) {
		t.Error("the page asks for a sub port the installer has already settled")
	}
	if w := postJoinForm(page, url.Values{"token": {"0123456789012345678901234567890a"}}); w.Code != http.StatusOK || !joined {
		t.Errorf("join through the configured port: status %d, reached the next hop: %t", w.Code, joined)
	}
}

// TestJoinNamesTheSubPortWhenTheNextHopIsUnreachable (#265): a hop behind the
// only443 front answers on 443 and its firewall drops 2096, so a box set up by
// hand with the default port timed out with nothing to go on. The error now
// names the port it tried and the one that front answers on.
func TestJoinNamesTheSubPortWhenTheNextHopIsUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	for _, tc := range []struct {
		port int
		want []string
	}{
		{port: closed, want: []string{"tried sub port " + strconv.Itoa(closed), "only443", "PROXY_NEXT_HOP_SUB_PORT=443", "--sub-port 443"}},
		{port: 443, want: []string{"tried sub port 443", "only443", "2096"}},
	} {
		cfg := &Config{NextHop: NextHop{Host: "127.0.0.1", SubPort: tc.port, SubScheme: "http"}}
		if tc.port == 443 {
			// Nothing may answer on the real 443 of the test machine.
			cfg.NextHop.Host = "192.0.2.1"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := Join(ctx, cfg, "0123456789012345678901234567890a")
		cancel()
		if err == nil {
			t.Fatalf("port %d: the join went through", tc.port)
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("port %d: the error does not say %q: %v", tc.port, want, err)
			}
		}
	}
}
