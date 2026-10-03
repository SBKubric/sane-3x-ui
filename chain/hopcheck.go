package chain

import (
	"strconv"
	"strings"
)

// NextHopCheckHeader carries the polling hop's own host reachability check of
// its next hop on every poll (SBKubric/sane-3x-ui#254), beside X-Chain-Front:
// "at=1757721530000; sent=10; lossPct=0; rttAvgMs=2". A series that lost every
// echo has no average and leaves rttAvgMs out.
const NextHopCheckHeader = "X-Chain-Next-Hop-Check"

// HopCheckMaxSent bounds the series a report may claim. A hop sends ten
// echoes; anything far beyond is not a report from a box of this chain.
const HopCheckMaxSent = 100

// hopCheckMaxRttMs bounds the average round trip a report may claim: an echo
// slower than a minute was not waited for.
const hopCheckMaxRttMs = 60_000

// HopCheck is a hop's host reachability check of its next hop: a series of
// ICMP echoes to the host the hop already dials (nextHop.host of its
// document), run by `x-ui proxy` on every poll of the chain. It says nothing
// a hop did not know: the address is its own next hop's, and the figures are
// about the leg the poll itself travels.
//
// The result travels inward like a front report — the hop's own in
// NextHopCheckHeader, its outer neighbours' in their OuterAck — and the panel
// shows the latest one per hop to mon-server as chain.hops[].nextHopCheck of
// GET /mon/v1/state (monitoring contract §4.1).
//
// At is when the series finished, in milliseconds UTC. RttAvgMs is the mean
// round trip of the echoes that came back, nil when none did (LossPct 100).
type HopCheck struct {
	At       int64  `json:"at"`
	Sent     int    `json:"sent"`
	LossPct  int    `json:"lossPct"`
	RttAvgMs *int64 `json:"rttAvgMs"`
}

// Valid reports whether the check says something the registry can hold: a
// time, a series of a plausible length, a loss percentage, and an average
// exactly when some echo came back.
func (c HopCheck) Valid() bool {
	if c.At <= 0 || c.Sent < 1 || c.Sent > HopCheckMaxSent || c.LossPct < 0 || c.LossPct > 100 {
		return false
	}
	if c.LossPct == 100 {
		return c.RttAvgMs == nil
	}
	return c.RttAvgMs != nil && *c.RttAvgMs >= 0 && *c.RttAvgMs <= hopCheckMaxRttMs
}

// Header renders the check as the NextHopCheckHeader value.
func (c HopCheck) Header() string {
	header := "at=" + strconv.FormatInt(c.At, 10) +
		"; sent=" + strconv.Itoa(c.Sent) +
		"; lossPct=" + strconv.Itoa(c.LossPct)
	if c.RttAvgMs != nil {
		header += "; rttAvgMs=" + strconv.FormatInt(*c.RttAvgMs, 10)
	}
	return header
}

// ParseHopCheck reads a NextHopCheckHeader value. The header comes from
// another box and ends up in the registry, so anything short of a valid check
// is no report at all.
func ParseHopCheck(header string) (HopCheck, bool) {
	var check HopCheck
	seen := false
	for _, part := range strings.Split(header, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		var err error
		switch strings.TrimSpace(key) {
		case "at":
			check.At, err = strconv.ParseInt(value, 10, 64)
		case "sent":
			check.Sent, err = strconv.Atoi(value)
		case "lossPct":
			check.LossPct, err = strconv.Atoi(value)
		case "rttAvgMs":
			var rtt int64
			rtt, err = strconv.ParseInt(value, 10, 64)
			check.RttAvgMs = &rtt
		default:
			continue
		}
		if err != nil {
			return HopCheck{}, false
		}
		seen = true
	}
	if !seen || !check.Valid() {
		return HopCheck{}, false
	}
	return check, true
}
