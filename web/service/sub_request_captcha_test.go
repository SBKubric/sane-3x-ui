package service

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

	"github.com/coinman-dev/3ax-ui/v2/captcha"
)

// The captcha's panel end (#188 points 9–11, #220): the person's tg_id comes
// from Telegram's initData, whose HMAC is keyed by the bot's token and whose
// auth_date must be fresh; a correct ALTCHA solution, once, opens the
// account's 30 minutes to leave a request.

// testWebAppToken signed testInitData.
const testWebAppToken = "123456:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// testInitData is what Telegram hands a Mini App opened by user 5550001 at
// testInitDataAt, signed with testWebAppToken as core.telegram.org/bots/
// webapps describes — computed apart from this code (Python's hmac).
const (
	testInitData = "auth_date=1790000000&query_id=AAHdF6IQAAAAAN0XohDhrOrc&user=%7B%22id%22%3A5550001%2C%22first_name%22" +
		"%3A%22Petr+%26+Co%22%2C%22username%22%3A%22petrov%22%2C%22language_code%22%3A%22ru%22%7D&signature=" +
		"SignatureOfThirdPartyValidation&hash=af0734198db3411a6674023f5c5bd0b639dbafd80efb4e5e24a5253b7bba9195"
	testInitDataUser = int64(5550001)
)

var testInitDataAt = time.Unix(1790000000, 0)

// TestWebAppUserFromSignedInitData: a valid initData names its user; a
// forged one — another user under the same hash, another token's hash, no
// hash — names nobody, nor does one past its freshness.
func TestWebAppUserFromSignedInitData(t *testing.T) {
	id, err := webAppUser(testInitData, testWebAppToken, testInitDataAt.Add(time.Minute))
	if err != nil || id != testInitDataUser {
		t.Fatalf("valid initData: %d, %v", id, err)
	}

	forged := map[string]string{
		"another user":  strings.Replace(testInitData, "5550001", "5550002", 1),
		"another token": testInitData,
		"no hash":       testInitData[:strings.Index(testInitData, "&hash=")],
		"empty":         "",
		"moved date":    strings.Replace(testInitData, "auth_date=1790000000", "auth_date=1790000500", 1),
	}
	for name, data := range forged {
		token := testWebAppToken
		if name == "another token" {
			token = "654321:" + strings.Repeat("b", 35)
		}
		if id, err := webAppUser(data, token, testInitDataAt.Add(time.Minute)); !errors.Is(err, ErrCaptchaInitData) {
			t.Errorf("%s: %d, %v", name, id, err)
		}
	}
	for name, at := range map[string]time.Time{
		"an hour later":       testInitDataAt.Add(time.Hour),
		"from the future":     testInitDataAt.Add(-10 * time.Minute),
		"just past freshness": testInitDataAt.Add(webAppInitDataMaxAge + time.Second),
	} {
		if _, err := webAppUser(testInitData, testWebAppToken, at); !errors.Is(err, ErrCaptchaInitData) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := webAppUser(testInitData, "", testInitDataAt); !errors.Is(err, ErrCaptchaInitData) {
		t.Errorf("no bot token: %v", err)
	}
}

// solveChallenge is the widget's work: the number whose SHA-256 after the
// salt is the challenge, as the payload the page posts.
func solveChallenge(t *testing.T, c captcha.Challenge) string {
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

// captchaFixture is the users fixture with the bot's token set, the
// requests' clock just after testInitData was signed, an easy issuer, and
// the pushes to the bot recorded instead of sent.
func captchaFixture(t *testing.T) *[]int64 {
	t.Helper()
	opsFixture(t)
	setSetting(t, "tgBotToken", testWebAppToken)
	now := testInitDataAt.Add(time.Minute)
	requestClock(t, &now)
	easy, err := captcha.NewIssuer(time.Minute, 500)
	if err != nil {
		t.Fatal(err)
	}
	prevIssuer, prevPush := subRequestCaptcha, requestCaptchaPush
	var pushed []int64
	subRequestCaptcha = easy
	requestCaptchaPush = func(tgId int64) { pushed = append(pushed, tgId) }
	t.Cleanup(func() { subRequestCaptcha, requestCaptchaPush = prevIssuer, prevPush })
	return &pushed
}

// TestCaptchaSolutionOpensTheRequestWindowOnce: a correct solution with a
// valid initData opens the window of initData's user and brings the bot's
// comment step to their chat; the same solution again is a replay, a wrong
// one is wrong, and a forged initData opens nothing and spends nothing.
func TestCaptchaSolutionOpensTheRequestWindowOnce(t *testing.T) {
	pushed := captchaFixture(t)
	svc := &SubRequestCaptchaService{}
	c, err := svc.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	payload := solveChallenge(t, c)

	forged := strings.Replace(testInitData, "5550001", "5550002", 1)
	if err := svc.Verify(forged, payload); !errors.Is(err, ErrCaptchaInitData) {
		t.Fatalf("forged initData: %v", err)
	}
	if subRequestWindows.open(5550002) || subRequestWindows.open(testInitDataUser) || len(*pushed) != 0 {
		t.Fatalf("a forged initData opened a window or pushed %v", *pushed)
	}

	if err := svc.Verify(testInitData, payload); err != nil {
		t.Fatalf("a correct solution: %v", err)
	}
	if !subRequestWindows.open(testInitDataUser) || len(*pushed) != 1 || (*pushed)[0] != testInitDataUser {
		t.Fatalf("after the captcha: window %v, pushed %v", subRequestWindows.open(testInitDataUser), *pushed)
	}
	if err := svc.Verify(testInitData, payload); !errors.Is(err, captcha.ErrReplayed) {
		t.Errorf("the same solution again: %v", err)
	}

	c2, _ := svc.Challenge()
	var p map[string]any
	raw, _ := base64.StdEncoding.DecodeString(solveChallenge(t, c2))
	_ = json.Unmarshal(raw, &p)
	p["number"] = p["number"].(float64) + 1
	wrongRaw, _ := json.Marshal(p)
	if err := svc.Verify(testInitData, base64.StdEncoding.EncodeToString(wrongRaw)); !errors.Is(err, captcha.ErrWrong) {
		t.Errorf("a wrong solution: %v", err)
	}
	if len(*pushed) != 1 {
		t.Errorf("refusals pushed: %v", *pushed)
	}
}
