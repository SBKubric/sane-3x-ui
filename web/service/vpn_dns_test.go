package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The VPN name's record on the active edge (#225) against a fake DNSExit:
// the request, the four-minute limit with the last wanted address winning,
// the backoff and the channel told of a lasting failure and of the
// recovery, the hourly reconcile, and nothing at all without a key or a name.

// fakeDNSExit is DNSExit's /dns/ endpoint: it records every request and
// answers with the codes queued in replies (0 when none is left).
type fakeDNSExit struct {
	mu       sync.Mutex
	requests []dnsExitRequest
	headers  []http.Header
	replies  []int
}

func (f *fakeDNSExit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var req dnsExitRequest
	if r.Method != http.MethodPost || json.Unmarshal(body, &req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.requests = append(f.requests, req)
	f.headers = append(f.headers, r.Header.Clone())
	code := 0
	if len(f.replies) > 0 {
		code, f.replies = f.replies[0], f.replies[1:]
	}
	msg := "Success"
	if code > 1 {
		msg = "API Key Authentication Error"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": msg})
}

func (f *fakeDNSExit) contents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		for _, u := range r.Update {
			out = append(out, u.Content)
		}
	}
	return out
}

// vpnDNSWorld is the panel with a VPN name and a key, a fake DNSExit, a fake
// resolver, a fake clock and the channel's messages.
type vpnDNSWorld struct {
	t        *testing.T
	api      *fakeDNSExit
	resolved map[string][]string // what the resolver answers for a name
	now      time.Time
	notified []string
	svc      *VPNNameService
}

func newVPNDNSWorld(t *testing.T) *vpnDNSWorld {
	t.Helper()
	newMonitoringSettingService(t)
	w := &vpnDNSWorld{t: t, api: &fakeDNSExit{}, resolved: map[string][]string{},
		now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), svc: &VPNNameService{}}
	srv := httptest.NewServer(w.api)
	t.Cleanup(srv.Close)
	prevEndpoint, prevNow, prevLookup, prevNotify := dnsExitEndpoint, vpnDNSNow, vpnDNSLookup, vpnDNSNotify
	dnsExitEndpoint = srv.URL + "/dns/"
	vpnDNSNow = func() time.Time { return w.now }
	vpnDNSLookup = func(_ context.Context, host string) ([]net.IP, error) {
		var ips []net.IP
		for _, s := range w.resolved[host] {
			ips = append(ips, net.ParseIP(s))
		}
		if len(ips) == 0 {
			return nil, errors.New("no such host")
		}
		return ips, nil
	}
	vpnDNSNotify = func(key string, params ...string) {
		w.notified = append(w.notified, key+" "+strings.Join(params, " "))
	}
	vpnDNS.reset()
	t.Cleanup(func() {
		dnsExitEndpoint, vpnDNSNow, vpnDNSLookup, vpnDNSNotify = prevEndpoint, prevNow, prevLookup, prevNotify
		vpnDNS.reset()
	})
	s := &SettingService{}
	if err := s.SetDnsExitApiKey("test-key"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVPNName("vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	return w
}

// edges registers joined edges a (192.0.2.10) and b (192.0.2.20) and
// c, known by name (c.example.net, 192.0.2.30), and makes active the one
// named.
func (w *vpnDNSWorld) edges(active string) {
	w.t.Helper()
	for _, h := range []model.ChainHop{
		{Name: "a", Host: "192.0.2.10"}, {Name: "b", Host: "192.0.2.20"}, {Name: "c", Host: "c.example.net"},
	} {
		h.Role, h.State, h.SubPort, h.SubScheme = model.ChainRoleEdge, model.ChainStateJoined, 2096, "http"
		if err := database.GetDB().Create(&h).Error; err != nil {
			w.t.Fatal(err)
		}
	}
	w.resolved["c.example.net"] = []string{"2001:db8::30", "192.0.2.30"}
	w.activate(active)
}

func (w *vpnDNSWorld) activate(name string) {
	w.t.Helper()
	var hop model.ChainHop
	if err := database.GetDB().Where("name = ?", name).First(&hop).Error; err != nil {
		w.t.Fatal(err)
	}
	if err := (&ChainService{}).SetActive(hop.Id); err != nil {
		w.t.Fatal(err)
	}
}

func (w *vpnDNSWorld) tick(after time.Duration) {
	w.now = w.now.Add(after)
	w.svc.Tick()
}

func (w *vpnDNSWorld) wantContents(want ...string) {
	w.t.Helper()
	if got := strings.Join(w.api.contents(), " "); got != strings.Join(want, " ") {
		w.t.Fatalf("DNSExit got updates %q, want %q", got, strings.Join(want, " "))
	}
}

// TestVPNDNSUpdatesOnSwitch: a switch of the active edge moves the record to
// the new edge's address, in DNSExit's JSON, with the TTL in minutes.
func TestVPNDNSUpdatesOnSwitch(t *testing.T) {
	w := newVPNDNSWorld(t)
	w.edges("a")
	w.resolved["vpn.example.com"] = []string{"192.0.2.10"}
	if err := (&SettingService{}).SetVPNNameTTL(7); err != nil {
		t.Fatal(err)
	}

	// At start the name already points at a: nothing to do.
	w.tick(0)
	w.wantContents()

	w.activate("b")
	w.tick(15 * time.Second)
	w.wantContents("192.0.2.20")
	req := w.api.requests[0]
	if req.ApiKey != "test-key" || req.Domain != "example.com" || len(req.Update) != 1 {
		t.Fatalf("request: %+v", req)
	}
	if u := req.Update[0]; u.Type != "A" || u.Name != "vpn.example.com" || u.Content != "192.0.2.20" || u.TTL != 7 {
		t.Errorf("update: %+v", u)
	}
	if ct := w.api.headers[0].Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}

	// Nothing more while nothing changes.
	for range 10 {
		w.tick(15 * time.Second)
	}
	w.wantContents("192.0.2.20")

	// An edge known by name: its IPv4.
	w.tick(5 * time.Minute)
	w.activate("c")
	w.tick(15 * time.Second)
	w.wantContents("192.0.2.20", "192.0.2.30")
	if len(w.notified) != 0 {
		t.Errorf("notified: %q", w.notified)
	}
}

// TestVPNDNSRateLimitCoalesces: switches closer than four minutes apart make
// one more call, four minutes after the last one, for the edge active then.
func TestVPNDNSRateLimitCoalesces(t *testing.T) {
	w := newVPNDNSWorld(t)
	w.edges("a")
	w.tick(0) // the name resolves to nothing: the first call sets it
	w.wantContents("192.0.2.10")

	w.activate("b")
	w.tick(time.Minute)
	w.activate("c")
	w.tick(time.Minute)
	w.activate("b")
	w.tick(time.Minute)
	w.wantContents("192.0.2.10")
	w.tick(59 * time.Second) // 3:59 since the call
	w.wantContents("192.0.2.10")
	w.tick(time.Second) // 4:00
	w.wantContents("192.0.2.10", "192.0.2.20")

	// Back and forth within the limit to where the record already is: no call.
	w.activate("a")
	w.tick(30 * time.Second)
	w.activate("b")
	w.tick(5 * time.Minute)
	w.wantContents("192.0.2.10", "192.0.2.20")
}

// TestVPNDNSRetriesAndNotifies: a refused update is retried with a growing
// pause, never sooner than four minutes; the third failure in a row tells
// the channel once, and the success after it tells it again.
func TestVPNDNSRetriesAndNotifies(t *testing.T) {
	w := newVPNDNSWorld(t)
	w.edges("a")
	w.api.replies = []int{2, 2, 2, 2}
	w.tick(0)
	w.wantContents("192.0.2.10")

	// The second try four minutes later, the third eight after it, the
	// fourth sixteen after that.
	w.tick(3*time.Minute + 59*time.Second)
	w.wantContents("192.0.2.10")
	w.tick(time.Second)
	w.wantContents("192.0.2.10", "192.0.2.10")
	if len(w.notified) != 0 {
		t.Fatalf("notified after two failures: %q", w.notified)
	}
	w.tick(7 * time.Minute)
	w.wantContents("192.0.2.10", "192.0.2.10")
	w.tick(time.Minute)
	w.wantContents("192.0.2.10", "192.0.2.10", "192.0.2.10")
	if len(w.notified) != 1 || !strings.HasPrefix(w.notified[0], "tgbot.vpnname.failed ") ||
		!strings.Contains(w.notified[0], "vpn.example.com") || !strings.Contains(w.notified[0], "192.0.2.10") ||
		!strings.Contains(w.notified[0], "API Key Authentication Error") || strings.Contains(w.notified[0], "test-key") {
		t.Fatalf("notified: %q", w.notified)
	}
	w.tick(16 * time.Minute)
	w.wantContents("192.0.2.10", "192.0.2.10", "192.0.2.10", "192.0.2.10")
	if len(w.notified) != 1 {
		t.Fatalf("told again: %q", w.notified)
	}
	// The pause stops growing at thirty minutes; this one goes through.
	w.tick(29 * time.Minute)
	w.wantContents("192.0.2.10", "192.0.2.10", "192.0.2.10", "192.0.2.10")
	w.tick(time.Minute)
	w.wantContents("192.0.2.10", "192.0.2.10", "192.0.2.10", "192.0.2.10", "192.0.2.10")
	if len(w.notified) != 2 || !strings.HasPrefix(w.notified[1], "tgbot.vpnname.recovered ") {
		t.Fatalf("notified: %q", w.notified)
	}
	w.tick(10 * time.Minute)
	if len(w.api.requests) != 5 {
		t.Errorf("calls after the recovery: %d", len(w.api.requests))
	}
}

// TestVPNDNSReconcile: once an hour the name is resolved; when it no longer
// points at the active edge (changed by hand at DNSExit) it is set again.
func TestVPNDNSReconcile(t *testing.T) {
	w := newVPNDNSWorld(t)
	w.edges("a")
	w.resolved["vpn.example.com"] = []string{"192.0.2.10"}
	w.tick(0)
	w.wantContents()

	w.resolved["vpn.example.com"] = []string{"198.51.100.1"}
	w.tick(59 * time.Minute)
	w.wantContents()
	w.tick(time.Minute)
	w.wantContents("192.0.2.10")

	// Right after an update the resolvers may still cache the old answer:
	// the next look is an hour later.
	w.tick(30 * time.Minute)
	w.wantContents("192.0.2.10")
	w.resolved["vpn.example.com"] = []string{"192.0.2.10"}
	w.tick(30 * time.Minute)
	w.wantContents("192.0.2.10")
}

// TestVPNDNSNothingWithoutKeyOrName: with no key, no name or no active edge
// DNSExit is never called.
func TestVPNDNSNothingWithoutKeyOrName(t *testing.T) {
	w := newVPNDNSWorld(t)
	s := &SettingService{}
	w.tick(0) // no active edge
	w.edges("a")
	if err := s.SetDnsExitApiKey(""); err != nil {
		t.Fatal(err)
	}
	w.tick(5 * time.Minute)
	if err := s.SetDnsExitApiKey("test-key"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVPNName(""); err != nil {
		t.Fatal(err)
	}
	w.tick(5 * time.Minute)
	if err := (&ChainService{}).ClearActive(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVPNName("vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	w.tick(5 * time.Minute)
	w.wantContents()
}
