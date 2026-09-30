package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The captcha before a request (#188 points 2, 9–11, #220, docs/spec/users.md
// §12), the panel's end: the page (package captcha) runs as a Telegram Mini
// App on the active edge, which passes the challenge and the verification on
// through the chain to the panel's sub server (sub/captchaController.go),
// which calls this. The person's tg_id is the one Telegram signed into the
// Mini App's initData — with an HMAC keyed by the bot's token — and the
// initData is fresh. A correct ALTCHA solution works once and opens the
// account's 30 minutes to leave a request; the bot then asks for the
// comment in the person's chat.

// ErrCaptchaInitData is an initData that does not name a person: not signed
// by our bot, or not fresh.
var ErrCaptchaInitData = errors.New("captcha: initData is not Telegram's or not fresh")

// webAppInitDataMaxAge is how old an initData may be: Telegram signs it when
// the Mini App opens, and the captcha takes seconds.
const webAppInitDataMaxAge = 15 * time.Minute

// webAppInitDataSkew is how far ahead of our clock an initData may be dated.
const webAppInitDataSkew = time.Minute

// subRequestCaptcha issues the challenges: ten minutes to solve one, a
// number up to 200 000 — about a second on a phone.
var subRequestCaptcha = mustCaptchaIssuer(10*time.Minute, 200_000)

func mustCaptchaIssuer(ttl time.Duration, maxNumber int64) *captcha.Issuer {
	is, err := captcha.NewIssuer(ttl, maxNumber)
	if err != nil {
		panic(err)
	}
	return is
}

// requestCaptchaPush brings the comment step to the chat of the account that
// passed the captcha, in the background: the page is waiting for its answer.
var requestCaptchaPush = func(tgId int64) {
	t := &Tgbot{}
	if !t.IsRunning() || checkAdmin(tgId) {
		return
	}
	go t.requestCaptchaPassed(tgId)
}

// webAppUser is the tg_id initData names, when botToken signed it and it was
// signed within webAppInitDataMaxAge of now.
func webAppUser(initData, botToken string, now time.Time) (int64, error) {
	if botToken == "" || initData == "" {
		return 0, ErrCaptchaInitData
	}
	values, err := tu.ValidateWebAppData(botToken, initData)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrCaptchaInitData, err)
	}
	sec, err := strconv.ParseInt(values.Get(tu.WebAppAuthDate), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: no auth_date", ErrCaptchaInitData)
	}
	signed := time.Unix(sec, 0)
	if now.Sub(signed) > webAppInitDataMaxAge || signed.Sub(now) > webAppInitDataSkew {
		return 0, fmt.Errorf("%w: signed at %s", ErrCaptchaInitData, signed.UTC().Format(time.RFC3339))
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get(tu.WebAppUser)), &user); err != nil || user.ID == 0 {
		return 0, fmt.Errorf("%w: no user", ErrCaptchaInitData)
	}
	return user.ID, nil
}

// SubRequestCaptchaService is the captcha's challenge and verification.
type SubRequestCaptchaService struct{}

// Challenge is a new challenge for the widget.
func (s *SubRequestCaptchaService) Challenge() (captcha.Challenge, error) {
	return subRequestCaptcha.Challenge()
}

// Verify checks the page's post: initData names the person
// (ErrCaptchaInitData otherwise), payload solves a challenge of ours
// (captcha.ErrWrong) not solved before (captcha.ErrReplayed). Then the
// person has 30 minutes to leave a request, and the bot asks for the
// comment.
func (s *SubRequestCaptchaService) Verify(initData, payload string) error {
	token, err := (&SettingService{}).GetTgBotToken()
	if err != nil {
		return err
	}
	tgId, err := webAppUser(initData, token, subRequestNow())
	if err != nil {
		return err
	}
	if err := subRequestCaptcha.Verify(payload); err != nil {
		return err
	}
	(&SubRequestService{}).CaptchaPassed(tgId)
	logger.Infof("request captcha passed by Telegram %d", tgId)
	requestCaptchaPush(tgId)
	return nil
}
