package service

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Telegram accounts (#186 point 1, docs/spec/users.md §11): every sender the
// bot sees, admin or not, is a row of tg_accounts with the nick and name it
// had then. A user points at its account through sub_users.tg_id.

// TgAccountService reads the Telegram accounts.
type TgAccountService struct{}

// Get returns the account tgId, nil when the bot never saw it.
func (s *TgAccountService) Get(tgId int64) (*model.TgAccount, error) {
	var a model.TgAccount
	err := database.GetDB().First(&a, "tg_id = ?", tgId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ByUsername returns the account that has the nick now (with or without the
// '@', ignoring case); nil when none has it.
func (s *TgAccountService) ByUsername(nick string) (*model.TgAccount, error) {
	nick = strings.TrimPrefix(strings.TrimSpace(nick), "@")
	if nick == "" {
		return nil, nil
	}
	var a model.TgAccount
	err := database.GetDB().Where("lower(username) = lower(?)", nick).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// tgAccountWriteEvery is how often one account's last_seen is written while
// its nick and name stay the same: the bot sees every message and press,
// the database needs a sign of life, not each of them.
const tgAccountWriteEvery = time.Minute

// tgAccountSeen is what the recorder last wrote for an account.
type tgAccountSeen struct {
	username, firstName, lastName string
	at                            time.Time
}

// tgAccountRecorder writes the accounts the bot sees, at most once a minute
// per account unless its nick or name changed. It remembers what it wrote
// since the panel started; the first sight after a start always writes.
type tgAccountRecorder struct {
	mu   sync.Mutex
	seen map[int64]tgAccountSeen
	now  func() time.Time
}

func newTgAccountRecorder(now func() time.Time) *tgAccountRecorder {
	return &tgAccountRecorder{seen: map[int64]tgAccountSeen{}, now: now}
}

// tgAccounts is the bot's recorder.
var tgAccounts = newTgAccountRecorder(time.Now)

// tgAccountSeenMax bounds the recorder's memory; past it, entries older than
// the throttle window are forgotten (they would write anyway).
const tgAccountSeenMax = 10000

// Note records a sight of the sender; wrote reports whether the database was
// written. Bots are not accounts of a person and are skipped.
func (r *tgAccountRecorder) Note(u telego.User) (wrote bool, err error) {
	if u.IsBot || u.ID == 0 {
		return false, nil
	}
	now := r.now()
	cur := tgAccountSeen{username: u.Username, firstName: u.FirstName, lastName: u.LastName, at: now}

	r.mu.Lock()
	defer r.mu.Unlock()
	prev, known := r.seen[u.ID]
	if known && prev.username == cur.username && prev.firstName == cur.firstName && prev.lastName == cur.lastName &&
		now.Sub(prev.at) < tgAccountWriteEvery {
		return false, nil
	}
	cleared, err := writeTgAccount(model.TgAccount{TgId: u.ID, Username: u.Username, FirstName: u.FirstName,
		LastName: u.LastName, LastSeen: now.UnixMilli()})
	if err != nil {
		return false, err
	}
	// The accounts that lost the nick hold none now: their next sight with
	// one is news.
	for _, id := range cleared {
		delete(r.seen, id)
	}
	if len(r.seen) >= tgAccountSeenMax {
		for id, s := range r.seen {
			if now.Sub(s.at) >= tgAccountWriteEvery {
				delete(r.seen, id)
			}
		}
	}
	r.seen[u.ID] = cur
	return true, nil
}

// writeTgAccount stores the account and takes its nick away from any other
// account that had it; cleared lists those.
func writeTgAccount(a model.TgAccount) (cleared []int64, err error) {
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if a.Username != "" {
			if err := tx.Model(&model.TgAccount{}).Where("lower(username) = lower(?) AND tg_id <> ?", a.Username, a.TgId).
				Pluck("tg_id", &cleared).Error; err != nil {
				return err
			}
			if len(cleared) > 0 {
				if err := tx.Model(&model.TgAccount{}).Where("tg_id IN ?", cleared).Update("username", "").Error; err != nil {
					return err
				}
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tg_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"username", "first_name", "last_name", "last_seen"}),
		}).Create(&a).Error
	})
	return cleared, err
}

// noteSender is the bot's first middleware: it records the sender of every
// message and button press, then lets the update on to its handler.
func (t *Tgbot) noteSender(ctx *th.Context, update telego.Update) error {
	var from *telego.User
	switch {
	case update.Message != nil:
		from = update.Message.From
	case update.CallbackQuery != nil:
		from = &update.CallbackQuery.From
	}
	if from != nil {
		if _, err := tgAccounts.Note(*from); err != nil {
			logger.Warning("telegram accounts:", err)
		}
	}
	return ctx.Next(update)
}
