package service

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Requests for a subscription (#188 points 1–3, 8, #220, docs/spec/users.md
// §12): someone with no user asks for one in the bot, after a captcha, with
// an optional comment; an admin decides (#221). The rules live here:
//   - an account has one pending request at a time;
//   - a request nobody decided on expires after 14 days (ExpireDue, run by
//     a job that also tells the person);
//   - after a rejection the account waits 7 days;
//   - an account that has a user, or that an admin blocked, does not ask;
//   - a request needs a captcha passed within the last 30 minutes, and uses
//     it up.

// The requests' rules in numbers.
const (
	subRequestTTL        = 14 * 24 * time.Hour
	subRequestCooldown   = 7 * 24 * time.Hour
	subRequestWindow     = 30 * time.Minute
	subRequestCommentMax = 200 // characters
)

// subRequestNow is the requests' clock; tests move it.
var subRequestNow = time.Now

// subRequestMu serialises the checks and writes of requests: one pending
// request per account holds under concurrent presses.
var subRequestMu sync.Mutex

// The refusals of a request.
const (
	SubRequestNeedsCaptcha   = "captcha"     // no captcha passed in the last 30 minutes
	SubRequestAlreadyPending = "pending"     // the account has a pending request
	SubRequestCoolingDown    = "cooldown"    // rejected less than 7 days ago; Until says when that ends
	SubRequestHasUser        = "has_user"    // the account has a user already
	SubRequestBlocked        = "blocked"     // an admin blocked the account
	SubRequestCommentTooLong = "comment"     // over 200 characters
	SubRequestNotPending     = "not_pending" // a decision on a request that no longer waits
)

// SubRequestRefusal is a request the rules turn down.
type SubRequestRefusal struct {
	Code  string
	Until int64 // ms, SubRequestCoolingDown: when a new request becomes possible
}

func (r *SubRequestRefusal) Error() string {
	return "request refused: " + r.Code
}

// SubRequestService keeps the requests.
type SubRequestService struct{}

// SubRequestStatus is where an account stands with requests.
type SubRequestStatus struct {
	HasUser bool
	Blocked bool
	// Pending is the account's request that waits; nil for none.
	Pending *model.SubRequest
	// Rejected is its last request when that was rejected less than 7 days
	// ago; NextAt (ms) is when the account may ask again.
	Rejected *model.SubRequest
	NextAt   int64
}

// CanApply reports whether the account may leave a request now (a captcha
// aside).
func (st *SubRequestStatus) CanApply() bool {
	return !st.HasUser && !st.Blocked && st.Pending == nil && st.Rejected == nil
}

// Status is where the account tgId stands.
func (s *SubRequestService) Status(tgId int64) (*SubRequestStatus, error) {
	users, err := telegramSubUsers(tgId)
	if err != nil {
		return nil, err
	}
	st := &SubRequestStatus{HasUser: len(users) > 0}
	db := database.GetDB()
	var account model.TgAccount
	if err := db.Where("tg_id = ?", tgId).Limit(1).Find(&account).Error; err != nil {
		return nil, err
	}
	st.Blocked = account.RequestsBlocked
	var last []model.SubRequest
	if err := db.Where("tg_id = ?", tgId).Order("created_at DESC, id DESC").Limit(1).Find(&last).Error; err != nil {
		return nil, err
	}
	if len(last) == 0 {
		return st, nil
	}
	r := &last[0]
	switch r.Status {
	case model.SubRequestPending:
		st.Pending = r
	case model.SubRequestRejected:
		if next := time.UnixMilli(r.DecidedAt).Add(subRequestCooldown); subRequestNow().Before(next) {
			st.Rejected, st.NextAt = r, next.UnixMilli()
		}
	}
	return st, nil
}

// Create leaves the account's request with comment (trimmed; "" for none).
// It needs the account's captcha window open and uses it up; the rules
// refuse with a SubRequestRefusal.
func (s *SubRequestService) Create(tgId int64, comment string) (*model.SubRequest, error) {
	comment = strings.TrimSpace(comment)
	if len([]rune(comment)) > subRequestCommentMax {
		return nil, &SubRequestRefusal{Code: SubRequestCommentTooLong}
	}
	subRequestMu.Lock()
	defer subRequestMu.Unlock()
	st, err := s.Status(tgId)
	if err != nil {
		return nil, err
	}
	switch {
	case st.HasUser:
		return nil, &SubRequestRefusal{Code: SubRequestHasUser}
	case st.Blocked:
		return nil, &SubRequestRefusal{Code: SubRequestBlocked}
	case st.Pending != nil:
		return nil, &SubRequestRefusal{Code: SubRequestAlreadyPending}
	case st.Rejected != nil:
		return nil, &SubRequestRefusal{Code: SubRequestCoolingDown, Until: st.NextAt}
	case !subRequestWindows.open(tgId):
		return nil, &SubRequestRefusal{Code: SubRequestNeedsCaptcha}
	}
	r := &model.SubRequest{TgId: tgId, Comment: comment, Status: model.SubRequestPending,
		CreatedAt: subRequestNow().UnixMilli()}
	if err := database.GetDB().Create(r).Error; err != nil {
		return nil, err
	}
	subRequestWindows.close(tgId)
	return r, nil
}

// Cancel — «Отменить заявку» — takes back the account's pending request;
// nil when none waits.
func (s *SubRequestService) Cancel(tgId int64) (*model.SubRequest, error) {
	subRequestMu.Lock()
	defer subRequestMu.Unlock()
	var r model.SubRequest
	err := database.GetDB().Where("tg_id = ? AND status = ?", tgId, model.SubRequestPending).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.decideLocked(&r, model.SubRequestCancelled, "", "")
}

// Reject turns the pending request id down for reason, as by; the account
// may ask again in 7 days.
func (s *SubRequestService) Reject(id int64, by, reason string) error {
	subRequestMu.Lock()
	defer subRequestMu.Unlock()
	var r model.SubRequest
	if err := database.GetDB().First(&r, id).Error; err != nil {
		return err
	}
	if r.Status != model.SubRequestPending {
		return &SubRequestRefusal{Code: SubRequestNotPending}
	}
	_, err := s.decideLocked(&r, model.SubRequestRejected, by, strings.TrimSpace(reason))
	return err
}

// decideLocked moves the pending request r to status.
func (s *SubRequestService) decideLocked(r *model.SubRequest, status, by, reason string) (*model.SubRequest, error) {
	r.Status, r.DecidedAt, r.DecidedBy, r.Reason = status, subRequestNow().UnixMilli(), by, reason
	res := database.GetDB().Model(&model.SubRequest{}).Where("id = ? AND status = ?", r.Id, model.SubRequestPending).
		Updates(map[string]any{"status": r.Status, "decided_at": r.DecidedAt, "decided_by": r.DecidedBy, "reason": r.Reason})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("request %d is no longer pending", r.Id)
	}
	return r, nil
}

// ExpireDue expires the requests that have waited 14 days and returns them,
// each once, for the job to tell the people.
func (s *SubRequestService) ExpireDue() ([]model.SubRequest, error) {
	subRequestMu.Lock()
	defer subRequestMu.Unlock()
	now := subRequestNow()
	var due []model.SubRequest
	if err := database.GetDB().Where("status = ? AND created_at <= ?", model.SubRequestPending,
		now.Add(-subRequestTTL).UnixMilli()).Order("id").Find(&due).Error; err != nil {
		return nil, err
	}
	var out []model.SubRequest
	for i := range due {
		r, err := s.decideLocked(&due[i], model.SubRequestExpired, "", "")
		if err != nil {
			return out, err
		}
		out = append(out, *r)
	}
	return out, nil
}

// SetBlocked blocks the account tgId from requests, or lets it ask again.
// The admin's button is #221's; the account's row is made if the bot never
// saw it.
func (s *SubRequestService) SetBlocked(tgId int64, blocked bool) error {
	return database.GetDB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tg_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"requests_blocked"}),
	}).Create(&model.TgAccount{TgId: tgId, RequestsBlocked: blocked}).Error
}

// CaptchaPassed opens the account's 30 minutes to leave a request.
func (s *SubRequestService) CaptchaPassed(tgId int64) {
	subRequestWindows.pass(tgId)
}

// requestWindows are the accounts' open captcha windows, in memory: a
// restart closes them, and the person passes the captcha again.
type requestWindows struct {
	mu    sync.Mutex
	until map[int64]time.Time
}

var subRequestWindows = &requestWindows{until: map[int64]time.Time{}}

func (w *requestWindows) pass(tgId int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := subRequestNow()
	for id, until := range w.until {
		if !now.Before(until) {
			delete(w.until, id)
		}
	}
	w.until[tgId] = now.Add(subRequestWindow)
}

func (w *requestWindows) open(tgId int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	until, ok := w.until[tgId]
	return ok && subRequestNow().Before(until)
}

func (w *requestWindows) close(tgId int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.until, tgId)
}

func (w *requestWindows) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.until = map[int64]time.Time{}
}
