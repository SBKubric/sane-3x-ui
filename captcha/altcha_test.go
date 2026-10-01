package captcha

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// solve does the widget's work: it finds the number whose SHA-256 after the
// salt is the challenge, and returns the payload the widget posts.
func solve(t *testing.T, c Challenge) string {
	t.Helper()
	for n := int64(0); n <= c.MaxNumber; n++ {
		sum := sha256.Sum256([]byte(c.Salt + fmt.Sprint(n)))
		if hex.EncodeToString(sum[:]) == c.Challenge {
			raw, _ := json.Marshal(map[string]any{"algorithm": c.Algorithm, "challenge": c.Challenge, "number": n,
				"salt": c.Salt, "signature": c.Signature})
			return base64.StdEncoding.EncodeToString(raw)
		}
	}
	t.Fatalf("no solution for %+v", c)
	return ""
}

func testIssuer() *Issuer { return NewIssuer("test-key", time.Minute, 2000) }

// TestIssuerChecksASolutionForItsAccount: the solution of a challenge made
// for an account passes for that account, with its signature and when its
// challenge expires; the same solution posted for another account is wrong.
func TestIssuerChecksASolutionForItsAccount(t *testing.T) {
	is := testIssuer()
	before := time.Now()
	c, err := is.Challenge(5550001)
	if err != nil {
		t.Fatal(err)
	}
	if c.Algorithm != "SHA-256" || c.MaxNumber != 2000 || c.Signature == "" || !strings.Contains(c.Salt, "tg=5550001") {
		t.Fatalf("challenge %+v", c)
	}
	payload := solve(t, c)
	sol, err := is.Check(payload, 5550001)
	if err != nil {
		t.Fatalf("a correct solution: %v", err)
	}
	if sol.Signature != c.Signature || sol.Expires.Before(before.Add(time.Minute-time.Second)) ||
		sol.Expires.After(time.Now().Add(time.Minute+time.Second)) {
		t.Errorf("solution %+v", sol)
	}
	if _, err := is.Check(payload, 5550002); !errors.Is(err, ErrWrong) {
		t.Errorf("another account's solution: %v, want ErrWrong", err)
	}
}

// TestIssuerRefusesAWrongSolution: a wrong number, a challenge signed by
// another key, a salt moved to another account, a payload that is no
// payload — each is refused as wrong.
func TestIssuerRefusesAWrongSolution(t *testing.T) {
	is := testIssuer()
	c, err := is.Challenge(7)
	if err != nil {
		t.Fatal(err)
	}
	good := solve(t, c)

	edit := func(change func(p map[string]any)) string {
		var p map[string]any
		raw, _ := base64.StdEncoding.DecodeString(good)
		_ = json.Unmarshal(raw, &p)
		change(p)
		out, _ := json.Marshal(p)
		return base64.StdEncoding.EncodeToString(out)
	}
	wrongNumber := edit(func(p map[string]any) { p["number"] = p["number"].(float64) + 1 })
	movedSalt := edit(func(p map[string]any) { p["salt"] = strings.Replace(p["salt"].(string), "tg=7", "tg=8", 1) })

	oc, err := NewIssuer("another-key", time.Minute, 2000).Challenge(7)
	if err != nil {
		t.Fatal(err)
	}
	foreign := solve(t, oc)

	for name, payload := range map[string]string{"wrong number": wrongNumber, "another key": foreign,
		"salt moved": movedSalt, "garbage": "not base64 at all", "empty": ""} {
		if _, err := is.Check(payload, 7); !errors.Is(err, ErrWrong) {
			t.Errorf("%s: %v, want ErrWrong", name, err)
		}
	}
	if _, err := is.Check(good, 7); err != nil {
		t.Errorf("the real solution after the wrong ones: %v", err)
	}
}

// TestIssuerRefusesAnExpiredChallenge: past its lifetime a challenge's
// solution is wrong, even solved correctly.
func TestIssuerRefusesAnExpiredChallenge(t *testing.T) {
	is := NewIssuer("test-key", -time.Minute, 2000) // born expired
	c, err := is.Challenge(7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := is.Check(solve(t, c), 7); !errors.Is(err, ErrWrong) {
		t.Fatalf("an expired challenge: %v, want ErrWrong", err)
	}
}

// TestIssuerKeySurvivesARestart: an issuer made again with the same key — the
// panel after a restart — accepts a challenge the first one made.
func TestIssuerKeySurvivesARestart(t *testing.T) {
	c, err := testIssuer().Challenge(7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testIssuer().Check(solve(t, c), 7); err != nil {
		t.Fatalf("after a restart: %v", err)
	}
}
