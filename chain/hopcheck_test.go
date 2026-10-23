package chain

import (
	"encoding/json"
	"testing"
)

func rttMs(value int64) *int64 { return &value }

// TestHopCheckHeaderRoundTrip (#254): what a box writes into
// X-Chain-Next-Hop-Check is what the next hop and the panel read back — with
// an average, and without one when every echo was lost.
func TestHopCheckHeaderRoundTrip(t *testing.T) {
	for name, check := range map[string]HopCheck{
		"all back":  {At: 1757721530000, Sent: 10, LossPct: 0, RttAvgMs: rttMs(2)},
		"some lost": {At: 1757721530000, Sent: 10, LossPct: 30, RttAvgMs: rttMs(41)},
		"all lost":  {At: 1757721530000, Sent: 10, LossPct: 100},
	} {
		header := check.Header()
		parsed, ok := ParseHopCheck(header)
		if !ok {
			t.Errorf("%s: %q did not parse", name, header)
			continue
		}
		if parsed.At != check.At || parsed.Sent != check.Sent || parsed.LossPct != check.LossPct ||
			(parsed.RttAvgMs == nil) != (check.RttAvgMs == nil) ||
			(parsed.RttAvgMs != nil && *parsed.RttAvgMs != *check.RttAvgMs) {
			t.Errorf("%s: %q parsed as %+v", name, header, parsed)
		}
	}
	if got := (HopCheck{At: 5, Sent: 10, LossPct: 100}).Header(); got != "at=5; sent=10; lossPct=100" {
		t.Errorf("all-lost header = %q, want no rttAvgMs", got)
	}
}

// TestParseHopCheckRefusesWhatTheRegistryCannotHold: the header comes from
// another box and ends up in the registry, so a malformed or impossible
// report is no report.
func TestParseHopCheckRefusesWhatTheRegistryCannotHold(t *testing.T) {
	for name, header := range map[string]string{
		"empty":                    "",
		"garbage":                  "not a check",
		"no time":                  "sent=10; lossPct=0; rttAvgMs=2",
		"no series":                "at=5; sent=0; lossPct=100",
		"too long a series":        "at=5; sent=101; lossPct=0; rttAvgMs=2",
		"loss over 100":            "at=5; sent=10; lossPct=101",
		"negative loss":            "at=5; sent=10; lossPct=-1; rttAvgMs=2",
		"an average of nothing":    "at=5; sent=10; lossPct=100; rttAvgMs=2",
		"replies without average":  "at=5; sent=10; lossPct=0",
		"negative average":         "at=5; sent=10; lossPct=0; rttAvgMs=-3",
		"an average over a minute": "at=5; sent=10; lossPct=0; rttAvgMs=60001",
		"not a number":             "at=5; sent=ten; lossPct=0; rttAvgMs=2",
	} {
		if check, ok := ParseHopCheck(header); ok {
			t.Errorf("%s: %q parsed as %+v", name, header, check)
		}
	}
}

// TestOuterAckCarriesTheNextHopCheck: an acknowledgement from a box older
// than the check has no nextHopCheck and decodes as before; a newer one
// carries it, with a null average when nothing came back.
func TestOuterAckCarriesTheNextHopCheck(t *testing.T) {
	var old []OuterAck
	if err := json.Unmarshal([]byte(`[{"name":"edge-a","lastRevision":42,"lastSeen":1}]`), &old); err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || old[0].NextHopCheck != nil {
		t.Fatalf("an old ack decoded as %+v", old)
	}

	encoded, err := json.Marshal(OuterAck{Name: "edge-a", LastRevision: 42, LastSeen: 1,
		NextHopCheck: &HopCheck{At: 5, Sent: 10, LossPct: 100}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"edge-a","lastRevision":42,"lastSeen":1,"nextHopCheck":{"at":5,"sent":10,"lossPct":100,"rttAvgMs":null}}`
	if string(encoded) != want {
		t.Errorf("ack = %s, want %s", encoded, want)
	}
	plain, _ := json.Marshal(OuterAck{Name: "edge-a"})
	if string(plain) != `{"name":"edge-a","lastRevision":0,"lastSeen":0}` {
		t.Errorf("an ack without a check = %s", plain)
	}
}
