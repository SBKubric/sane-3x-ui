package proxy

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// timeoutError is what a socket says when its read deadline passes.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// fakeICMP is an ICMP socket whose far end is the test: every echo that goes
// out comes back after delay unless its sequence number is in drop, and
// before it the far end can slip in replies that belong to somebody else.
type fakeICMP struct {
	v6       bool
	delay    time.Duration
	drop     map[int]bool
	writeErr error
	// strays are sent back ahead of the first real reply.
	strays [][]byte

	mu       sync.Mutex
	deadline time.Time
	sentTo   []net.Addr
	replies  chan []byte
	closed   bool
}

func newFakeICMP() *fakeICMP {
	return &fakeICMP{drop: map[int]bool{}, replies: make(chan []byte, 64)}
}

func (f *fakeICMP) protocol() int {
	if f.v6 {
		return ipv6.ICMPTypeEchoRequest.Protocol()
	}
	return ipv4.ICMPTypeEcho.Protocol()
}

func (f *fakeICMP) WriteTo(b []byte, dst net.Addr) (int, error) {
	f.mu.Lock()
	f.sentTo = append(f.sentTo, dst)
	strays := f.strays
	f.strays = nil
	f.mu.Unlock()
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	for _, stray := range strays {
		f.replies <- stray
	}
	message, err := icmp.ParseMessage(f.protocol(), b)
	if err != nil {
		return 0, err
	}
	echo := message.Body.(*icmp.Echo)
	if f.drop[echo.Seq] {
		return len(b), nil
	}
	var replyType icmp.Type = ipv4.ICMPTypeEchoReply
	if f.v6 {
		replyType = ipv6.ICMPTypeEchoReply
	}
	reply, err := (&icmp.Message{Type: replyType, Body: echo}).Marshal(nil)
	if err != nil {
		return 0, err
	}
	go func() {
		time.Sleep(f.delay)
		f.replies <- reply
	}()
	return len(b), nil
}

func (f *fakeICMP) ReadFrom(b []byte) (int, net.Addr, error) {
	f.mu.Lock()
	deadline := f.deadline
	f.mu.Unlock()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case reply := <-f.replies:
		return copy(b, reply), &net.IPAddr{}, nil
	case <-timer.C:
		return 0, nil, timeoutError{}
	}
}

func (f *fakeICMP) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deadline = t
	return nil
}

func (f *fakeICMP) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// fakeChecker is the ICMP check over a fake socket, fast enough for a unit
// test: ten echoes 2 ms apart, the last one waited for 100 ms.
func fakeChecker(conn *fakeICMP, datagram bool) *ICMPChecker {
	return &ICMPChecker{
		count:    10,
		interval: 2 * time.Millisecond,
		wait:     100 * time.Millisecond,
		resolve: func(context.Context, string) (net.IP, error) {
			if conn.v6 {
				return net.ParseIP("2001:db8::7"), nil
			}
			return net.ParseIP("10.0.0.7"), nil
		},
		listen: func(v6 bool) (icmpConn, bool, error) {
			if v6 != conn.v6 {
				return nil, false, errors.New("wrong address family")
			}
			return conn, datagram, nil
		},
		now: time.Now,
	}
}

// TestICMPCheckAllBack (#254): ten echoes, ten replies — no loss, and the
// average round trip of the replies.
func TestICMPCheckAllBack(t *testing.T) {
	conn := newFakeICMP()
	conn.delay = 5 * time.Millisecond
	before := time.Now().UnixMilli()
	check, err := fakeChecker(conn, false).Check(context.Background(), "10.0.0.7")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Sent != 10 || check.LossPct != 0 || check.RttAvgMs == nil {
		t.Fatalf("check = %+v, want 10 sent, none lost, an average", check)
	}
	if *check.RttAvgMs < 5 || *check.RttAvgMs > 100 {
		t.Errorf("rttAvgMs = %d, want about the 5 ms the far end takes", *check.RttAvgMs)
	}
	if check.At < before || !check.Valid() {
		t.Errorf("check = %+v (started at %d)", check, before)
	}
	if !conn.closed {
		t.Error("the socket was left open")
	}
	if _, raw := conn.sentTo[0].(*net.IPAddr); !raw || len(conn.sentTo) != 10 {
		t.Errorf("a raw socket sent %d echoes to %T", len(conn.sentTo), conn.sentTo[0])
	}
}

// TestICMPCheckCountsTheLost: the echoes that never come back are the loss.
func TestICMPCheckCountsTheLost(t *testing.T) {
	conn := newFakeICMP()
	conn.drop = map[int]bool{0: true, 4: true, 9: true}
	check, err := fakeChecker(conn, false).Check(context.Background(), "10.0.0.7")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Sent != 10 || check.LossPct != 30 || check.RttAvgMs == nil {
		t.Errorf("check = %+v, want 30 %% lost with an average", check)
	}
}

// TestICMPCheckAllLost: nothing back is 100 % and no average — a result, not
// an error, since it is exactly what the check is for.
func TestICMPCheckAllLost(t *testing.T) {
	conn := newFakeICMP()
	for seq := 0; seq < 10; seq++ {
		conn.drop[seq] = true
	}
	check, err := fakeChecker(conn, false).Check(context.Background(), "10.0.0.7")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Sent != 10 || check.LossPct != 100 || check.RttAvgMs != nil || !check.Valid() {
		t.Errorf("check = %+v, want 100 %% lost and no average", check)
	}
}

// TestICMPCheckARefusedSendIsALostEcho: a network that refuses the echo
// (no route) loses it; the check still reports.
func TestICMPCheckARefusedSendIsALostEcho(t *testing.T) {
	conn := newFakeICMP()
	conn.writeErr = errors.New("sendto: network is unreachable")
	check, err := fakeChecker(conn, false).Check(context.Background(), "10.0.0.7")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.Sent != 10 || check.LossPct != 100 {
		t.Errorf("check = %+v, want every echo lost", check)
	}
}

// TestICMPCheckIgnoresSomebodyElsesReplies: a raw socket sees every echo
// reply on the box. Only one with this series' token and a sequence number it
// sent counts, and a duplicate counts once.
func TestICMPCheckIgnoresSomebodyElsesReplies(t *testing.T) {
	conn := newFakeICMP()
	for seq := 0; seq < 10; seq++ {
		conn.drop[seq] = true
	}
	foreign, _ := (&icmp.Message{Type: ipv4.ICMPTypeEchoReply,
		Body: &icmp.Echo{ID: 1, Seq: 0, Data: []byte("somebody else's ping")}}).Marshal(nil)
	request, _ := (&icmp.Message{Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: 1, Seq: 0, Data: []byte("an echo request")}}).Marshal(nil)
	conn.strays = [][]byte{foreign, request, []byte("not icmp")}

	check, err := fakeChecker(conn, false).Check(context.Background(), "10.0.0.7")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.LossPct != 100 {
		t.Errorf("check = %+v: a reply that is not ours counted", check)
	}
}

// TestICMPCheckOverADatagramSocket: an unprivileged socket is addressed by UDP
// address, and the kernel's rewritten identifier does not cost the replies.
func TestICMPCheckOverADatagramSocket(t *testing.T) {
	conn := newFakeICMP()
	check, err := fakeChecker(conn, true).Check(context.Background(), "10.0.0.7")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.LossPct != 0 {
		t.Errorf("check = %+v", check)
	}
	if _, udp := conn.sentTo[0].(*net.UDPAddr); !udp {
		t.Errorf("a datagram socket sent to %T", conn.sentTo[0])
	}
}

// TestICMPCheckOverIPv6: a next hop with only an IPv6 address gets ICMPv6.
func TestICMPCheckOverIPv6(t *testing.T) {
	conn := newFakeICMP()
	conn.v6 = true
	conn.drop[3] = true
	check, err := fakeChecker(conn, false).Check(context.Background(), "edge.example.net")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if check.LossPct != 10 || check.RttAvgMs == nil {
		t.Errorf("check = %+v, want 10 %% lost", check)
	}
}

// TestICMPCheckWithoutASocketIsNoCheck: no resolvable host or no ICMP socket
// is an error — no check took place — never a 100 % loss.
func TestICMPCheckWithoutASocketIsNoCheck(t *testing.T) {
	checker := fakeChecker(newFakeICMP(), false)
	checker.resolve = func(context.Context, string) (net.IP, error) { return nil, errors.New("no such host") }
	if check, err := checker.Check(context.Background(), "nowhere.example.net"); err == nil {
		t.Errorf("an unresolvable host gave %+v", check)
	}

	checker = fakeChecker(newFakeICMP(), false)
	checker.listen = func(bool) (icmpConn, bool, error) { return nil, false, errors.New("operation not permitted") }
	if check, err := checker.Check(context.Background(), "10.0.0.7"); err == nil {
		t.Errorf("a box without an ICMP socket gave %+v", check)
	}
}

// TestICMPCheckStopsWithItsContext: a series cut short reports nothing.
func TestICMPCheckStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if check, err := fakeChecker(newFakeICMP(), false).Check(ctx, "10.0.0.7"); err == nil {
		t.Errorf("a cancelled check gave %+v", check)
	}
}

// TestResolveHostPrefersIPv4: an address is taken as it is.
func TestResolveHostPrefersIPv4(t *testing.T) {
	ip, err := resolveHost(context.Background(), "10.0.0.7")
	if err != nil || !ip.Equal(net.ParseIP("10.0.0.7")) {
		t.Errorf("resolveHost(10.0.0.7) = %v, %v", ip, err)
	}
	ip, err = resolveHost(context.Background(), "2001:db8::7")
	if err != nil || !ip.Equal(net.ParseIP("2001:db8::7")) {
		t.Errorf("resolveHost(2001:db8::7) = %v, %v", ip, err)
	}
}
