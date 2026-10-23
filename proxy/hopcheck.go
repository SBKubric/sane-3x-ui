package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/chain"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// The host reachability check of a hop's next hop (SBKubric/sane-3x-ui#254):
// a series of ICMP echoes, run beside every poll of the chain.
const (
	// hopCheckCount and hopCheckInterval are the series: ten echoes, one
	// every 200 ms, so a lost one is a percentage point of ten, not noise.
	hopCheckCount    = 10
	hopCheckInterval = 200 * time.Millisecond
	// hopCheckWait is how long the last echo is waited for after it went out.
	hopCheckWait = time.Second
	// hopCheckTimeout bounds a whole series, name resolution included, well
	// inside the shortest poll interval (the 5 s fast retry after a join).
	hopCheckTimeout = 4 * time.Second
)

// HostChecker runs a host reachability check of one host. An error means no
// check took place — the host did not resolve, or no ICMP socket could be
// opened — and is never a result: a lost echo is a result, a missing socket
// is not.
type HostChecker interface {
	Check(ctx context.Context, host string) (chain.HopCheck, error)
}

// icmpConn is the part of *icmp.PacketConn the check uses, so a test can hand
// it a socket that answers, drops or garbles echoes on demand.
type icmpConn interface {
	WriteTo(b []byte, dst net.Addr) (int, error)
	ReadFrom(b []byte) (int, net.Addr, error)
	SetReadDeadline(t time.Time) error
	Close() error
}

// ICMPChecker sends the echoes. `x-ui proxy` runs as root, so a raw ICMP socket
// is the first choice; an unprivileged datagram socket (net.ipv4.ping_group_range)
// is the fallback for a box that runs it otherwise.
type ICMPChecker struct {
	count    int
	interval time.Duration
	wait     time.Duration

	resolve func(ctx context.Context, host string) (net.IP, error)
	// listen opens a socket for the address family; datagram reports a
	// datagram socket, which the kernel addresses by UDP address and whose
	// echo identifier it rewrites.
	listen func(ipv6 bool) (conn icmpConn, datagram bool, err error)
	now    func() time.Time
}

// NewICMPChecker is the check `x-ui proxy` runs: ten echoes 200 ms apart.
func NewICMPChecker() *ICMPChecker {
	return &ICMPChecker{
		count:    hopCheckCount,
		interval: hopCheckInterval,
		wait:     hopCheckWait,
		resolve:  resolveHost,
		listen:   listenICMP,
		now:      time.Now,
	}
}

// resolveHost turns the next hop's host into one address, IPv4 first: the
// relay dials the same name, and a box with both families reaches its next
// hop over IPv4 unless it has nothing else.
func resolveHost(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		if addr.IP.To4() != nil {
			return addr.IP, nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0].IP, nil
	}
	return nil, fmt.Errorf("%s has no address", host)
}

// listenICMP opens a raw ICMP socket, or a datagram one where raw is refused.
func listenICMP(v6 bool) (icmpConn, bool, error) {
	raw, datagram, wildcard := "ip4:icmp", "udp4", "0.0.0.0"
	if v6 {
		raw, datagram, wildcard = "ip6:ipv6-icmp", "udp6", "::"
	}
	conn, rawErr := icmp.ListenPacket(raw, wildcard)
	if rawErr == nil {
		return conn, false, nil
	}
	conn, err := icmp.ListenPacket(datagram, wildcard)
	if err != nil {
		return nil, false, fmt.Errorf("no ICMP socket (raw: %v; datagram: %v)", rawErr, err)
	}
	return conn, true, nil
}

// Check sends the series to host and counts what came back. Every echo that
// went out — or could not, because the network refused it — is in Sent; an
// echo is back when a reply with its sequence number and this series' token
// arrived before the series ended.
func (c *ICMPChecker) Check(ctx context.Context, host string) (chain.HopCheck, error) {
	ip, err := c.resolve(ctx, host)
	if err != nil {
		return chain.HopCheck{}, fmt.Errorf("resolve %s: %w", host, err)
	}
	v6 := ip.To4() == nil
	conn, datagram, err := c.listen(v6)
	if err != nil {
		return chain.HopCheck{}, err
	}
	defer conn.Close()

	var dst net.Addr = &net.IPAddr{IP: ip}
	if datagram {
		dst = &net.UDPAddr{IP: ip}
	}
	var requestType, replyType icmp.Type = ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	protocol := ipv4.ICMPTypeEcho.Protocol()
	if v6 {
		requestType, replyType = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
		protocol = ipv6.ICMPTypeEchoRequest.Protocol()
	}

	// The token tells this series' replies from everything else a raw socket
	// sees — other pings on the box, a previous series' late replies — and
	// survives the identifier rewrite of a datagram socket.
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return chain.HopCheck{}, err
	}
	id := int(binary.BigEndian.Uint16(token))

	series := &echoSeries{token: token, protocol: protocol, replyType: replyType,
		sentAt: make([]time.Time, c.count), rtt: make(map[int]time.Duration, c.count), now: c.now}
	start := c.now()
	for seq := 0; seq < c.count; seq++ {
		if err := ctx.Err(); err != nil {
			return chain.HopCheck{}, err
		}
		message := icmp.Message{Type: requestType, Body: &icmp.Echo{ID: id, Seq: seq, Data: token}}
		packet, err := message.Marshal(nil)
		if err != nil {
			return chain.HopCheck{}, err
		}
		series.sentAt[seq] = c.now()
		// A refused send (no route, network down) is a lost echo, not a
		// missing check: it is exactly what the check is for.
		_, _ = conn.WriteTo(packet, dst)

		deadline := start.Add(time.Duration(seq+1) * c.interval)
		if seq == c.count-1 {
			deadline = series.sentAt[seq].Add(c.wait)
		}
		series.collect(ctx, conn, deadline, c.count)
	}
	if err := ctx.Err(); err != nil {
		return chain.HopCheck{}, err
	}
	return series.result(c.count, c.now()), nil
}

// echoSeries is one check in flight: when each echo went out and how long the
// ones that came back took.
type echoSeries struct {
	token     []byte
	protocol  int
	replyType icmp.Type
	sentAt    []time.Time
	rtt       map[int]time.Duration
	now       func() time.Time
}

// collect reads replies until deadline, or until every echo is back.
func (s *echoSeries) collect(ctx context.Context, conn icmpConn, deadline time.Time, count int) {
	buf := make([]byte, 1500)
	for len(s.rtt) < count && ctx.Err() == nil {
		if !s.now().Before(deadline) {
			return
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			return
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				// Not a timeout: the socket has nothing sensible to say
				// until the deadline, and asking again at once would spin.
				sleepUntil(ctx, deadline)
			}
			return
		}
		s.note(buf[:n])
	}
}

// note records a reply that belongs to this series.
func (s *echoSeries) note(packet []byte) {
	message, err := icmp.ParseMessage(s.protocol, packet)
	if err != nil || message.Type != s.replyType {
		return
	}
	echo, ok := message.Body.(*icmp.Echo)
	if !ok || echo.Seq < 0 || echo.Seq >= len(s.sentAt) || s.sentAt[echo.Seq].IsZero() || !bytes.Equal(echo.Data, s.token) {
		return
	}
	if _, seen := s.rtt[echo.Seq]; seen {
		return
	}
	s.rtt[echo.Seq] = s.now().Sub(s.sentAt[echo.Seq])
}

// result turns the series into the report: loss rounded down, so that one lost
// echo is never 0 % and one that came back is never 100 %; the average round
// trip to the nearest millisecond, none when nothing came back.
func (s *echoSeries) result(sent int, at time.Time) chain.HopCheck {
	check := chain.HopCheck{At: at.UnixMilli(), Sent: sent, LossPct: (sent - len(s.rtt)) * 100 / sent}
	if len(s.rtt) == 0 {
		return check
	}
	var total time.Duration
	for _, rtt := range s.rtt {
		total += rtt
	}
	average := (total/time.Duration(len(s.rtt)) + time.Millisecond/2) / time.Millisecond
	ms := int64(average)
	check.RttAvgMs = &ms
	return check
}

func sleepUntil(ctx context.Context, deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
