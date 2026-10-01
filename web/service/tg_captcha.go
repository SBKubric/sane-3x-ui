package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	tu "github.com/mymmrac/telego/telegoutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The bot's captcha (#188 points 2, 9–11, #220, docs/spec/users.md §12), the
// panel's end. It is the bot's alone and bound to nothing but the Telegram
// account: the tg_id Telegram signed into the Mini App's initData — with an
// HMAC keyed by the bot's token — keys the challenge, the solution, the
// pass and the replay protection. Nothing here looks at a subscription, a
// subId or a user: whether the account may ask for a subscription is the
// bot's question (SubRequestService), not the captcha's.
//
// The page (package captcha) runs as a Mini App at /third-party/<secret>/
// captcha on the active edge's front, which passes the challenge and the
// verification on through the chain to the panel's bot handler
// (web/controller/third_party.go), which calls this. The result is kept in
// tg_captcha: an account that has passed is remembered and does not pass
// again, until an admin resets it or blocks the account. The solutions
// accepted are kept in tg_captcha_solutions while their challenges live, so
// none works twice, a restart in between or not.

// ErrCaptchaInitData is an initData that does not name a person: not signed
// by our bot, or not fresh.
var ErrCaptchaInitData = errors.New("captcha: initData is not Telegram's or not fresh")

// webAppInitDataMaxAge is how old an initData may be: Telegram signs it when
// the Mini App opens, and the captcha takes seconds.
const webAppInitDataMaxAge = 15 * time.Minute

// webAppInitDataSkew is how far ahead of our clock an initData may be dated.
const webAppInitDataSkew = time.Minute

// The challenges: ten minutes to solve one, a number up to 200 000 — about a
// second on a phone.
const (
	tgCaptchaTTL       = 10 * time.Minute
	tgCaptchaMaxNumber = 200_000
)

// tgCaptchaNow is the captcha's clock; tests move it.
var tgCaptchaNow = time.Now

// tgCaptchaMu serialises the writes of tg_captcha: an attempt counts once
// under concurrent posts.
var tgCaptchaMu sync.Mutex

// tgCaptchaIssuer is the issuer of the challenges. Its HMAC key comes from
// the panel's own secret, so it is the same after a restart: a challenge
// fetched before one is solvable after it, and a solution spent before one
// is still found spent.
var tgCaptchaIssuer = func() (*captcha.Issuer, error) {
	secret, err := (&SettingService{}).GetSecret()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("3ax-ui bot captcha"))
	return captcha.NewIssuer(hex.EncodeToString(mac.Sum(nil)), tgCaptchaTTL, tgCaptchaMaxNumber), nil
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

// TgCaptchaService is the captcha: its challenge and verification, and the
// accounts' passes.
type TgCaptchaService struct{}

// account is the tg_id initData names (ErrCaptchaInitData otherwise).
func (s *TgCaptchaService) account(initData string) (int64, error) {
	token, err := (&SettingService{}).GetTgBotToken()
	if err != nil {
		return 0, err
	}
	return webAppUser(initData, token, tgCaptchaNow())
}

// Challenge is a new challenge for the account initData names; a forged or
// stale initData gets none (ErrCaptchaInitData).
func (s *TgCaptchaService) Challenge(initData string) (captcha.Challenge, error) {
	tgId, err := s.account(initData)
	if err != nil {
		return captcha.Challenge{}, err
	}
	is, err := tgCaptchaIssuer()
	if err != nil {
		return captcha.Challenge{}, err
	}
	return is.Challenge(tgId)
}

// Verify checks the page's post: initData names the account
// (ErrCaptchaInitData otherwise), payload solves a challenge made for it
// (captcha.ErrWrong) that was not solved before (captcha.ErrReplayed). The
// attempt is counted either way; a pass is remembered, and the bot asks for
// the comment.
func (s *TgCaptchaService) Verify(initData, payload string) error {
	tgId, err := s.account(initData)
	if err != nil {
		return err
	}
	is, err := tgCaptchaIssuer()
	if err != nil {
		return err
	}
	now := tgCaptchaNow()
	sol, verdict := is.Check(payload, tgId)
	if verdict == nil {
		fresh, err := spendTgCaptchaSolution(sol, tgId, now)
		if err != nil {
			return err
		}
		if !fresh {
			verdict = captcha.ErrReplayed
		}
	}
	if err := s.recordAttempt(tgId, now, verdict == nil); err != nil {
		return err
	}
	if verdict != nil {
		return verdict
	}
	logger.Infof("captcha passed by Telegram %d", tgId)
	requestCaptchaPush(tgId)
	return nil
}

// spendTgCaptchaSolution records sol as used by tgId; false when it was used
// before. The solutions whose challenges have expired go first: they could
// not pass again anyway.
func spendTgCaptchaSolution(sol captcha.Solution, tgId int64, now time.Time) (bool, error) {
	db := database.GetDB()
	if err := db.Where("expires_at < ?", now.UnixMilli()).Delete(&model.TgCaptchaSolution{}).Error; err != nil {
		return false, err
	}
	res := db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.TgCaptchaSolution{Signature: sol.Signature, TgId: tgId, ExpiresAt: sol.Expires.UnixMilli()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// recordAttempt counts an attempt of tgId at now, a pass or a fail.
func (s *TgCaptchaService) recordAttempt(tgId int64, now time.Time, passed bool) error {
	tgCaptchaMu.Lock()
	defer tgCaptchaMu.Unlock()
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		var row model.TgCaptcha
		if err := tx.Where("tg_id = ?", tgId).Limit(1).Find(&row).Error; err != nil {
			return err
		}
		row.TgId = tgId
		row.Attempts++
		row.LastAttemptAt = now.UnixMilli()
		if passed {
			row.PassedAt = now.UnixMilli()
		} else {
			row.LastFailAt = now.UnixMilli()
		}
		return tx.Save(&row).Error
	})
}

// State is where the account tgId stands with the captcha; a row of zeros
// when it never tried.
func (s *TgCaptchaService) State(tgId int64) (*model.TgCaptcha, error) {
	row := &model.TgCaptcha{}
	if err := database.GetDB().Where("tg_id = ?", tgId).Limit(1).Find(row).Error; err != nil {
		return nil, err
	}
	row.TgId = tgId
	return row, nil
}

// Passed reports whether the account tgId has passed the captcha and the
// pass has not been reset since.
func (s *TgCaptchaService) Passed(tgId int64) (bool, error) {
	st, err := s.State(tgId)
	if err != nil {
		return false, err
	}
	return st.PassedAt > 0, nil
}

// Pass records a pass of tgId now, as a correct solution does.
func (s *TgCaptchaService) Pass(tgId int64) error {
	return s.recordAttempt(tgId, tgCaptchaNow(), true)
}

// Reset — «Сбросить капчу», and every block of the account — forgets the
// pass of tgId: its next request asks for the captcha again. The attempts
// stay counted.
func (s *TgCaptchaService) Reset(tgId int64) error {
	tgCaptchaMu.Lock()
	defer tgCaptchaMu.Unlock()
	return database.GetDB().Model(&model.TgCaptcha{}).Where("tg_id = ?", tgId).Update("passed_at", 0).Error
}
