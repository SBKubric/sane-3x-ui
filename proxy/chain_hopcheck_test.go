package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// stubChecker is a host reachability check that answers at once with check,
// and says which host it was asked about.
type stubChecker struct {
	check chain.HopCheck
	err   error
	hosts chan string
}

func (s *stubChecker) Check(_ context.Context, host string) (chain.HopCheck, error) {
	defer func() { s.hosts <- host }()
	return s.check, s.err
}

func int64Ptr(value int64) *int64 { return &value }

// waitForCheck waits until the background check of one poll has finished
// and returns the host it checked.
func waitForCheck(t *testing.T, poller *Poller, hosts chan string) string {
	t.Helper()
	select {
	case host := <-hosts:
		deadline := time.Now().Add(time.Second)
		for poller.checking.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		return host
	case <-time.After(2 * time.Second):
		t.Fatal("the poll ran no host reachability check")
		return ""
	}
}

// TestPollChecksTheNextHopAndReportsIt (#254): every poll runs the check of
// the host the hop dials — its document's nextHop.host once there is one —
// and the next poll carries the result inward in X-Chain-Next-Hop-Check.
func TestPollChecksTheNextHopAndReportsIt(t *testing.T) {
	doc := testDocument(42)
	var seen http.Header
	poller, state, _, _ := wavePoller(t, serveDocument(&doc, &seen))
	checker := &stubChecker{check: chain.HopCheck{At: 1757721530000, Sent: 10, LossPct: 20, RttAvgMs: int64Ptr(3)},
		hosts: make(chan string, 4)}
	poller.SetHostChecker(checker)

	// Before the box has a document it checks the next hop of proxy.json.
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if host := waitForCheck(t, poller, checker.hosts); host != poller.cfg.NextHop.Host {
		t.Errorf("first check went to %q, want proxy.json's next hop %q", host, poller.cfg.NextHop.Host)
	}
	if got := state.NextHopCheck(); got == nil || got.LossPct != 20 {
		t.Fatalf("the check was not kept: %+v", got)
	}

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if host := waitForCheck(t, poller, checker.hosts); host != "10.0.0.7" {
		t.Errorf("second check went to %q, want the document's next hop 10.0.0.7", host)
	}
	got, ok := chain.ParseHopCheck(seen.Get(chain.NextHopCheckHeader))
	if !ok || got.LossPct != 20 || got.RttAvgMs == nil || *got.RttAvgMs != 3 {
		t.Errorf("%s = %q, want the first check", chain.NextHopCheckHeader, seen.Get(chain.NextHopCheckHeader))
	}

	status := state.Status(nil, poller.cfg)
	if status.NextHop.Check == nil || status.NextHop.Check.LossPct != 20 {
		t.Errorf("status next hop = %+v, want the check", status.NextHop)
	}
}

// TestPollWithoutACheckSendsNone: a box that has not finished a check yet,
// or could not run one, sends no header — and a failed check keeps the last
// good one rather than reporting a loss it never measured.
func TestPollWithoutACheckSendsNone(t *testing.T) {
	doc := testDocument(42)
	var seen http.Header
	poller, state, _, _ := wavePoller(t, serveDocument(&doc, &seen))

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, present := seen[http.CanonicalHeaderKey(chain.NextHopCheckHeader)]; present {
		t.Errorf("a box with no check sent %q", seen.Get(chain.NextHopCheckHeader))
	}
	if status := state.Status(nil, poller.cfg); status.NextHop.Check != nil {
		t.Errorf("status shows a check that never ran: %+v", status.NextHop.Check)
	}

	state.SetNextHopCheck(chain.HopCheck{At: 1, Sent: 10, LossPct: 0, RttAvgMs: int64Ptr(1)})
	checker := &stubChecker{err: context.DeadlineExceeded, hosts: make(chan string, 1)}
	poller.SetHostChecker(checker)
	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	waitForCheck(t, poller, checker.hosts)
	if got := state.NextHopCheck(); got == nil || got.At != 1 {
		t.Errorf("a failed check replaced the last one: %+v", got)
	}
}

// TestChainHandlerPassesTheNeighboursCheckInward: an outer neighbour's
// check arrives in its poll's header and leaves in this hop's own
// X-Chain-Outer, inside that neighbour's acknowledgement — as does a check
// that reached the neighbour from further out. A neighbour older than the
// check sends none, and its acknowledgement goes on as before.
func TestChainHandlerPassesTheNeighboursCheckInward(t *testing.T) {
	state := NewState()
	state.SetDocument(innerDocument())
	cfg := &Config{HopSecret: "inner-1-secret", Domain: "10.0.0.7"}
	handler := testChainHandler(t, cfg, state)

	further := EncodeOuterAcks([]OuterAck{{Name: "edge-a", LastRevision: 42, LastSeen: 1,
		NextHopCheck: &chain.HopCheck{At: 7, Sent: 10, LossPct: 100}}})
	own := chain.HopCheck{At: 9, Sent: 10, LossPct: 10, RttAvgMs: int64Ptr(12)}
	if w := getWithSecret(handler, ChainPathPrefix+"/document", "inner-2-secret", map[string]string{
		chain.SeenHeader: "42", chain.NextHopCheckHeader: own.Header(), chain.OuterHeader: further,
	}); w.Code != http.StatusOK {
		t.Fatalf("inner-2's poll: %d", w.Code)
	}
	if w := getWithSecret(handler, ChainPathPrefix+"/document", "edge-b-secret", map[string]string{
		chain.SeenHeader: "42", chain.NextHopCheckHeader: "garbled",
	}); w.Code != http.StatusOK {
		t.Fatalf("edge-b's poll: %d", w.Code)
	}

	decoded := DecodeOuterAcks(EncodeOuterAcks(state.OuterAcks()))
	byName := map[string]OuterAck{}
	for _, ack := range decoded {
		byName[ack.Name] = ack
	}
	if check := byName["inner-2"].NextHopCheck; check == nil || check.LossPct != 10 || check.RttAvgMs == nil || *check.RttAvgMs != 12 {
		t.Errorf("inner-2's own check = %+v", check)
	}
	if check := byName["edge-a"].NextHopCheck; check == nil || check.LossPct != 100 || check.RttAvgMs != nil {
		t.Errorf("edge-a's check from further out = %+v", check)
	}
	if ack, found := byName["edge-b"]; !found || ack.NextHopCheck != nil {
		t.Errorf("edge-b, which sent no valid check, = %+v (found %t)", ack, found)
	}
	raw, _ := json.Marshal(byName["edge-b"])
	if string(raw) != `{"name":"edge-b","lastRevision":42,"lastSeen":`+jsonInt(byName["edge-b"].LastSeen)+`}` {
		t.Errorf("an ack without a check grew a field: %s", raw)
	}
}

func jsonInt(value int64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
