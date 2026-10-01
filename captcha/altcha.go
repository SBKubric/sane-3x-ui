// Package captcha is the ALTCHA check a person passes in the bot before they
// leave a request for a subscription (#188 points 2, 9–11, #220,
// docs/spec/users.md §12): a proof-of-work captcha of our own, no third-party
// service. It belongs to the bot alone and is bound to nothing but the
// person's Telegram account: every challenge is made for one tg_id, and only
// a solution posted with that tg_id passes it. The page and the widget
// (page.go) are served under the bot's own path on the 443 front,
// /third-party/<secret>/captcha, which the hops of the chain pass on to the
// panel on real.
//
// The server side is github.com/altcha-org/altcha-lib-go, ALTCHA's official
// Go library: SHA-256 challenges signed with an HMAC key, the protocol the
// widget of the same generation (altcha 2.x, served from altcha.js) solves.
// The package keeps no state: which solutions have been used is the
// caller's to remember (the panel keeps them in its database).
package captcha

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	altcha "github.com/altcha-org/altcha-lib-go"
)

// ErrWrong is a solution that does not solve a live challenge of ours made
// for this tg_id: a wrong number, another key's challenge, another
// account's, an expired one, or no payload.
var ErrWrong = errors.New("captcha: wrong solution")

// ErrReplayed is a solution that has been accepted before: each works once.
// The caller tells it, by the Solution's signature.
var ErrReplayed = errors.New("captcha: solution already used")

// Challenge is what the widget fetches and solves.
type Challenge = altcha.Challenge

// tgParam is the parameter of a challenge's salt that names the tg_id it was
// made for. The salt is under the HMAC, so it cannot be changed.
const tgParam = "tg"

// Issuer makes challenges and checks their solutions. Its HMAC key is the
// caller's and stable across restarts, so a challenge fetched before a
// restart is still solvable after it.
type Issuer struct {
	key       string
	ttl       time.Duration
	maxNumber int64
	now       func() time.Time
}

// NewIssuer is an issuer signing with key whose challenges live ttl and hide
// a number up to maxNumber: the widget tries half of that on average.
func NewIssuer(key string, ttl time.Duration, maxNumber int64) *Issuer {
	return &Issuer{key: key, ttl: ttl, maxNumber: maxNumber, now: time.Now}
}

// Challenge is a new challenge for the account tgId, expiring after the
// issuer's ttl.
func (is *Issuer) Challenge(tgId int64) (Challenge, error) {
	expires := is.now().Add(is.ttl)
	return altcha.CreateChallenge(altcha.ChallengeOptions{Algorithm: altcha.SHA256, MaxNumber: is.maxNumber,
		HMACKey: is.key, Expires: &expires, Params: url.Values{tgParam: {strconv.FormatInt(tgId, 10)}}})
}

// Solution is a checked solution: its signature, which the caller spends
// once, and when its challenge expires — how long the caller has to
// remember it.
type Solution struct {
	Signature string
	Expires   time.Time
}

// Check accepts payload — the widget's base64 JSON — when it solves a live
// challenge of this issuer made for tgId; ErrWrong otherwise. Whether the
// solution was used before is the caller's to tell.
func (is *Issuer) Check(payload string, tgId int64) (Solution, error) {
	ok, err := altcha.VerifySolutionSafe(payload, is.key, true)
	if err != nil || !ok {
		return Solution{}, ErrWrong
	}
	p, err := decodePayload(payload)
	if err != nil {
		return Solution{}, ErrWrong
	}
	params := altcha.ExtractParams(p)
	if params.Get(tgParam) != strconv.FormatInt(tgId, 10) {
		return Solution{}, ErrWrong
	}
	sec, err := strconv.ParseInt(params.Get("expires"), 10, 64)
	if err != nil {
		return Solution{}, ErrWrong
	}
	return Solution{Signature: p.Signature, Expires: time.Unix(sec, 0)}, nil
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
