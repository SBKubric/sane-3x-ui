package captcha

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

func testIssuer(t *testing.T) *Issuer {
	t.Helper()
	is, err := NewIssuer(time.Minute, 2000)
	if err != nil {
		t.Fatal(err)
	}
	return is
}

// TestIssuerAcceptsASolutionOnce: the solution of a challenge the issuer
// made passes, once; the same payload again is a replay.
func TestIssuerAcceptsASolutionOnce(t *testing.T) {
	is := testIssuer(t)
	c, err := is.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	if c.Algorithm != "SHA-256" || c.MaxNumber != 2000 || c.Signature == "" {
		t.Fatalf("challenge %+v", c)
	}
	payload := solve(t, c)
	if err := is.Verify(payload); err != nil {
		t.Fatalf("a correct solution: %v", err)
	}
	if err := is.Verify(payload); !errors.Is(err, ErrReplayed) {
		t.Fatalf("the same solution again: %v, want ErrReplayed", err)
	}
}

// TestIssuerRefusesAWrongSolution: a wrong number, a challenge signed by
// another key, a payload that is no payload — each is refused as wrong,
// and none burns the real solution.
func TestIssuerRefusesAWrongSolution(t *testing.T) {
	is := testIssuer(t)
	c, err := is.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	good := solve(t, c)

	var p map[string]any
	raw, _ := base64.StdEncoding.DecodeString(good)
	_ = json.Unmarshal(raw, &p)
	p["number"] = p["number"].(float64) + 1
	wrongRaw, _ := json.Marshal(p)
	wrongNumber := base64.StdEncoding.EncodeToString(wrongRaw)

	other := testIssuer(t)
	oc, err := other.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	foreign := solve(t, oc)

	for name, payload := range map[string]string{"wrong number": wrongNumber, "another key": foreign,
		"garbage": "not base64 at all", "empty": ""} {
		if err := is.Verify(payload); !errors.Is(err, ErrWrong) {
			t.Errorf("%s: %v, want ErrWrong", name, err)
		}
	}
	if err := is.Verify(good); err != nil {
		t.Errorf("the real solution after the wrong ones: %v", err)
	}
}

// TestIssuerRefusesAnExpiredChallenge: past its lifetime a challenge's
// solution is wrong, even solved correctly.
func TestIssuerRefusesAnExpiredChallenge(t *testing.T) {
	is, err := NewIssuer(-time.Minute, 2000) // born expired
	if err != nil {
		t.Fatal(err)
	}
	c, err := is.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	if err := is.Verify(solve(t, c)); !errors.Is(err, ErrWrong) {
		t.Fatalf("an expired challenge: %v, want ErrWrong", err)
	}
}

// TestIssuerForgetsSpentSolutionsPastTheirLifetime: the replay memory holds
// a solution only while its challenge lives, so it does not grow forever.
func TestIssuerForgetsSpentSolutionsPastTheirLifetime(t *testing.T) {
	is := testIssuer(t)
	now := time.Now()
	is.now = func() time.Time { return now }
	c, err := is.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	if err := is.Verify(solve(t, c)); err != nil {
		t.Fatal(err)
	}
	if n := is.spentCount(); n != 1 {
		t.Fatalf("spent: %d", n)
	}
	now = now.Add(2 * time.Minute)
	c2, _ := is.Challenge()
	if err := is.Verify(solve(t, c2)); err != nil && !errors.Is(err, ErrWrong) {
		t.Fatal(err)
	}
	if n := is.spentCount(); n > 1 {
		t.Fatalf("spent past the lifetime: %d", n)
	}
}
