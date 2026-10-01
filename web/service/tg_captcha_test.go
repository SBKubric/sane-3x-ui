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
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The bot's captcha, the panel's end (#188 points 9–11, #220): the person's
// tg_id comes from Telegram's initData, whose HMAC is keyed by the bot's
// token and whose auth_date must be fresh, and it alone keys the challenge,
// the solution and the pass. A correct ALTCHA solution works once — across
// a restart too — and the pass is remembered in tg_captcha.

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

// captchaFixture is the users fixture with the bot's token set, the clocks
// just after testInitData was signed, an easy issuer, and the pushes to the
// bot recorded instead of sent.
func captchaFixture(t *testing.T) (*[]int64, *time.Time) {
	t.Helper()
	opsFixture(t)
	setSetting(t, "tgBotToken", testWebAppToken)
	now := testInitDataAt.Add(time.Minute)
	requestClock(t, &now)
	prevIssuer, prevPush := tgCaptchaIssuer, requestCaptchaPush
	var pushed []int64
	tgCaptchaIssuer = func() (*captcha.Issuer, error) { return captcha.NewIssuer("test-key", time.Hour, 500), nil }
	requestCaptchaPush = func(tgId int64) { pushed = append(pushed, tgId) }
	t.Cleanup(func() { tgCaptchaIssuer, requestCaptchaPush = prevIssuer, prevPush })
	return &pushed, &now
}

// TestCaptchaChallengeIsForTheAccount: a challenge is made for the account
// a valid initData names, and for nobody on a forged one.
func TestCaptchaChallengeIsForTheAccount(t *testing.T) {
	captchaFixture(t)
	svc := &TgCaptchaService{}
	c, err := svc.Challenge(testInitData)
	if err != nil || !strings.Contains(c.Salt, "tg=5550001") {
		t.Fatalf("challenge: %+v, %v", c, err)
	}
	if _, err := svc.Challenge(strings.Replace(testInitData, "5550001", "5550002", 1)); !errors.Is(err, ErrCaptchaInitData) {
		t.Errorf("a forged initData: %v", err)
	}
	if _, err := svc.Challenge(""); !errors.Is(err, ErrCaptchaInitData) {
		t.Errorf("no initData: %v", err)
	}
}

// TestCaptchaPassIsRememberedAndSolutionsWorkOnce: a correct solution with a
// valid initData records the pass of initData's user, counts the attempt
// and brings the bot's comment step to their chat; the same solution again
// is a replay — after a restart too, for the spent solutions live in the
// database — and a wrong one is wrong and counted as a fail. A forged
// initData records nothing and spends nothing.
func TestCaptchaPassIsRememberedAndSolutionsWorkOnce(t *testing.T) {
	pushed, now := captchaFixture(t)
	svc := &TgCaptchaService{}
	c, err := svc.Challenge(testInitData)
	if err != nil {
		t.Fatal(err)
	}
	payload := solveChallenge(t, c)

	forged := strings.Replace(testInitData, "5550001", "5550002", 1)
	if err := svc.Verify(forged, payload); !errors.Is(err, ErrCaptchaInitData) {
		t.Fatalf("forged initData: %v", err)
	}
	for _, id := range []int64{5550002, testInitDataUser} {
		if st, _ := svc.State(id); st.Attempts != 0 || st.PassedAt != 0 {
			t.Fatalf("a forged initData left %+v", st)
		}
	}
	if len(*pushed) != 0 {
		t.Fatalf("a forged initData pushed %v", *pushed)
	}

	if err := svc.Verify(testInitData, payload); err != nil {
		t.Fatalf("a correct solution: %v", err)
	}
	st, err := svc.State(testInitDataUser)
	if err != nil || st.PassedAt != now.UnixMilli() || st.Attempts != 1 || st.LastAttemptAt != now.UnixMilli() || st.LastFailAt != 0 {
		t.Fatalf("after the captcha: %+v, %v", st, err)
	}
	if passed, _ := svc.Passed(testInitDataUser); !passed || len(*pushed) != 1 || (*pushed)[0] != testInitDataUser {
		t.Fatalf("after the captcha: passed %v, pushed %v", passed, *pushed)
	}

	// A restart: a new issuer with the same key, the database as it was.
	tgCaptchaIssuer = func() (*captcha.Issuer, error) { return captcha.NewIssuer("test-key", time.Hour, 500), nil }
	if err := svc.Verify(testInitData, payload); !errors.Is(err, captcha.ErrReplayed) {
		t.Errorf("the same solution again: %v", err)
	}

	*now = now.Add(time.Minute)
	c2, _ := svc.Challenge(testInitData)
	var p map[string]any
	raw, _ := base64.StdEncoding.DecodeString(solveChallenge(t, c2))
	_ = json.Unmarshal(raw, &p)
	p["number"] = p["number"].(float64) + 1
	wrongRaw, _ := json.Marshal(p)
	if err := svc.Verify(testInitData, base64.StdEncoding.EncodeToString(wrongRaw)); !errors.Is(err, captcha.ErrWrong) {
		t.Errorf("a wrong solution: %v", err)
	}
	st, _ = svc.State(testInitDataUser)
	if st.Attempts != 3 || st.LastFailAt != now.UnixMilli() || st.PassedAt == 0 {
		t.Errorf("after a replay and a wrong one: %+v", st)
	}
	if len(*pushed) != 1 {
		t.Errorf("refusals pushed: %v", *pushed)
	}
}

// TestCaptchaSolutionIsTheAccounts: a solution of a challenge made for one
// account does not pass for another, both signed by Telegram.
func TestCaptchaSolutionIsTheAccounts(t *testing.T) {
	captchaFixture(t)
	is, _ := tgCaptchaIssuer()
	c, err := is.Challenge(5550002)
	if err != nil {
		t.Fatal(err)
	}
	if err := (&TgCaptchaService{}).Verify(testInitData, solveChallenge(t, c)); !errors.Is(err, captcha.ErrWrong) {
		t.Errorf("another account's solution: %v", err)
	}
}

// TestCaptchaSpentSolutionsAreCleanedUp: a spent solution is kept while its
// challenge lives — spent again, it is a replay — then the next spend drops
// it.
func TestCaptchaSpentSolutionsAreCleanedUp(t *testing.T) {
	_, now := captchaFixture(t)
	count := func() int64 {
		var n int64
		if err := database.GetDB().Model(&model.TgCaptchaSolution{}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	first := captcha.Solution{Signature: "first", Expires: now.Add(10 * time.Minute)}
	if fresh, err := spendTgCaptchaSolution(first, 7, *now); err != nil || !fresh {
		t.Fatalf("first spend: %v, %v", fresh, err)
	}
	if fresh, err := spendTgCaptchaSolution(first, 7, now.Add(5*time.Minute)); err != nil || fresh {
		t.Fatalf("the same within its lifetime: %v, %v", fresh, err)
	}
	if n := count(); n != 1 {
		t.Fatalf("spent solutions: %d", n)
	}
	*now = now.Add(20 * time.Minute)
	second := captcha.Solution{Signature: "second", Expires: now.Add(10 * time.Minute)}
	if fresh, err := spendTgCaptchaSolution(second, 7, *now); err != nil || !fresh {
		t.Fatalf("second spend: %v, %v", fresh, err)
	}
	if n := count(); n != 1 {
		t.Errorf("spent solutions after the first one's lifetime: %d", n)
	}
}

// TestCaptchaIssuerKeyIsThePanels: the issuer's key comes from the panel's
// secret, the same after a restart: a challenge of one issuer passes with
// the next.
func TestCaptchaIssuerKeyIsThePanels(t *testing.T) {
	opsFixture(t)
	first, err := tgCaptchaIssuer()
	if err != nil {
		t.Fatal(err)
	}
	c, err := first.Challenge(7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tgCaptchaIssuer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Check(solveChallenge(t, c), 7); err != nil {
		t.Errorf("a challenge across two issuers: %v", err)
	}
}
