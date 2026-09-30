// Package captcha is the ALTCHA check a person passes before they leave a
// request for a subscription in the bot (#188 points 2, 9–11, #220,
// docs/spec/users.md §12): a proof-of-work captcha of our own, no third-party
// service. The page and the widget (page.go) are served on the active edge
// under the subscription path and on the panel's own sub server; the
// challenge and the verification (Issuer) are the panel's, which the hops
// reach through the chain as they reach subscriptions.
//
// The server side is github.com/altcha-org/altcha-lib-go, ALTCHA's official
// Go library: SHA-256 challenges signed with an HMAC key, the protocol the
// widget of the same generation (altcha 2.x, served from altcha.js) solves.
package captcha

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go"
)

// ErrWrong is a solution that does not solve a live challenge of ours: a
// wrong number, another key's challenge, an expired one, or no payload.
var ErrWrong = errors.New("captcha: wrong solution")

// ErrReplayed is a solution that has been accepted before: each works once.
var ErrReplayed = errors.New("captcha: solution already used")

// Challenge is what the widget fetches and solves.
type Challenge = altcha.Challenge

// Issuer makes challenges and verifies their solutions, each once. Its HMAC
// key is random and lives with the process: a restart only makes the
// challenges out at that moment unsolvable, and the widget fetches another.
type Issuer struct {
	key       string
	ttl       time.Duration
	maxNumber int64
	now       func() time.Time

	mu    sync.Mutex
	spent map[string]time.Time // signature → when its challenge expires
}

// NewIssuer is an issuer whose challenges live ttl and hide a number up to
// maxNumber: the widget tries half of that on average.
func NewIssuer(ttl time.Duration, maxNumber int64) (*Issuer, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	return &Issuer{key: hex.EncodeToString(b[:]), ttl: ttl, maxNumber: maxNumber, now: time.Now,
		spent: map[string]time.Time{}}, nil
}

// Challenge is a new challenge, expiring after the issuer's ttl.
func (is *Issuer) Challenge() (Challenge, error) {
	expires := is.now().Add(is.ttl)
	return altcha.CreateChallenge(altcha.ChallengeOptions{Algorithm: altcha.SHA256, MaxNumber: is.maxNumber,
		HMACKey: is.key, Expires: &expires})
}

// Verify accepts payload — the widget's base64 JSON — once: ErrWrong when it
// does not solve a live challenge of this issuer, ErrReplayed when it has
// been accepted before.
func (is *Issuer) Verify(payload string) error {
	ok, err := altcha.VerifySolutionSafe(payload, is.key, true)
	if err != nil || !ok {
		return ErrWrong
	}
	p, err := decodePayload(payload)
	if err != nil {
		return ErrWrong
	}
	expires := is.now().Add(is.ttl)
	if at := altcha.ExtractParams(p).Get("expires"); at != "" {
		if sec, err := strconv.ParseInt(at, 10, 64); err == nil {
			expires = time.Unix(sec, 0)
		}
	}
	is.mu.Lock()
	defer is.mu.Unlock()
	now := is.now()
	for sig, until := range is.spent {
		if now.After(until) {
			delete(is.spent, sig)
		}
	}
	if _, used := is.spent[p.Signature]; used {
		return ErrReplayed
	}
	is.spent[p.Signature] = expires
	return nil
}

// spentCount is how many accepted solutions the issuer remembers.
func (is *Issuer) spentCount() int {
	is.mu.Lock()
	defer is.mu.Unlock()
	return len(is.spent)
}

func decodePayload(payload string) (altcha.Payload, error) {
	var p altcha.Payload
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p)
	return p, err
}
