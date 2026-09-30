package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The requests' rules (#188 point 2, #220): one pending request per
// account, expiry after 14 days, a new one 7 days after a rejection, no
// request from an account that has a user or is blocked, and none without
// a captcha passed in the last 30 minutes.

// requestClock sets the requests' clock to *now for the test; the test moves
// it by changing now.
func requestClock(t *testing.T, now *time.Time) {
	t.Helper()
	prev := subRequestNow
	subRequestNow = func() time.Time { return *now }
	t.Cleanup(func() { subRequestNow = prev; subRequestWindows.reset() })
}

// requestRefusal is the code of err's refusal, "" for none.
func requestRefusal(err error) string {
	var r *SubRequestRefusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// mustRequest makes a request of tgId after its captcha.
func mustRequest(t *testing.T, tgId int64, comment string) *model.SubRequest {
	t.Helper()
	requests := &SubRequestService{}
	requests.CaptchaPassed(tgId)
	r, err := requests.Create(tgId, comment)
	if err != nil {
		t.Fatalf("Create(%d): %v", tgId, err)
	}
	return r
}

// TestRequestNeedsTheCaptchaWithinItsWindow: without a captcha there is no
// request; a passed one opens 30 minutes for one request, which uses it.
func TestRequestNeedsTheCaptchaWithinItsWindow(t *testing.T) {
	opsFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	requestClock(t, &now)
	requests := &SubRequestService{}

	if _, err := requests.Create(501, ""); requestRefusal(err) != SubRequestNeedsCaptcha {
		t.Fatalf("no captcha: %v", err)
	}
	requests.CaptchaPassed(501)
	now = now.Add(31 * time.Minute)
	if _, err := requests.Create(501, ""); requestRefusal(err) != SubRequestNeedsCaptcha {
		t.Fatalf("31 minutes after the captcha: %v", err)
	}
	requests.CaptchaPassed(501)
	now = now.Add(29 * time.Minute)
	r, err := requests.Create(501, "  please  ")
	if err != nil {
		t.Fatalf("29 minutes after the captcha: %v", err)
	}
	if r.TgId != 501 || r.Status != model.SubRequestPending || r.Comment != "please" || r.CreatedAt != now.UnixMilli() {
		t.Errorf("the request: %+v", r)
	}
	if _, err := requests.Cancel(501); err != nil {
		t.Fatal(err)
	}
	if _, err := requests.Create(501, ""); requestRefusal(err) != SubRequestNeedsCaptcha {
		t.Errorf("a second request on the same captcha: %v", err)
	}
}

// TestRequestOnePendingPerAccount: while one request waits, another is
// refused; a cancelled one frees the account, and another account is not
// held up by the first.
func TestRequestOnePendingPerAccount(t *testing.T) {
	opsFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	requestClock(t, &now)
	requests := &SubRequestService{}

	first := mustRequest(t, 501, "")
	requests.CaptchaPassed(501)
	if _, err := requests.Create(501, "again"); requestRefusal(err) != SubRequestAlreadyPending {
		t.Fatalf("a second pending request: %v", err)
	}
	mustRequest(t, 502, "")

	st, err := requests.Status(501)
	if err != nil || st.Pending == nil || st.Pending.Id != first.Id || st.CanApply() {
		t.Fatalf("status: %+v, %v", st, err)
	}
	cancelled, err := requests.Cancel(501)
	if err != nil || cancelled == nil || cancelled.Status != model.SubRequestCancelled || cancelled.DecidedAt != now.UnixMilli() {
		t.Fatalf("cancel: %+v, %v", cancelled, err)
	}
	if again, err := requests.Cancel(501); err != nil || again != nil {
		t.Errorf("cancel with nothing pending: %+v, %v", again, err)
	}
	if st, _ := requests.Status(501); st.Pending != nil || !st.CanApply() {
		t.Errorf("after the cancel: %+v", st)
	}
	mustRequest(t, 501, "")
}

// TestRequestExpiresAfterFourteenDays: a request nobody decided on in 14
// days expires — once — and the account may ask again.
func TestRequestExpiresAfterFourteenDays(t *testing.T) {
	opsFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	requestClock(t, &now)
	requests := &SubRequestService{}
	old := mustRequest(t, 501, "")
	now = now.Add(time.Hour)
	fresh := mustRequest(t, 502, "")

	now = now.Add(14*24*time.Hour - 2*time.Hour)
	if due, err := requests.ExpireDue(); err != nil || len(due) != 0 {
		t.Fatalf("before 14 days: %+v, %v", due, err)
	}
	now = now.Add(time.Hour)
	due, err := requests.ExpireDue()
	if err != nil || len(due) != 1 || due[0].Id != old.Id || due[0].Status != model.SubRequestExpired {
		t.Fatalf("at 14 days: %+v, %v", due, err)
	}
	if again, _ := requests.ExpireDue(); len(again) != 0 {
		t.Errorf("expired twice: %+v", again)
	}
	if st, _ := requests.Status(502); st.Pending == nil || st.Pending.Id != fresh.Id {
		t.Errorf("the fresh request: %+v", st)
	}
	if st, _ := requests.Status(501); st.Pending != nil || !st.CanApply() {
		t.Errorf("after the expiry: %+v", st)
	}
	mustRequest(t, 501, "")
}

// TestRequestSevenDaysAfterARejection: after a rejection the account waits
// seven days, the reason and the date shown; then it may ask again.
func TestRequestSevenDaysAfterARejection(t *testing.T) {
	opsFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	requestClock(t, &now)
	requests := &SubRequestService{}
	r := mustRequest(t, 501, "")
	now = now.Add(time.Hour)
	if err := requests.Reject(r.Id, "admin", "no invite"); err != nil {
		t.Fatal(err)
	}
	if err := requests.Reject(r.Id, "admin", "twice"); requestRefusal(err) != SubRequestNotPending {
		t.Errorf("rejecting a decided request: %v", err)
	}

	st, err := requests.Status(501)
	next := now.Add(7 * 24 * time.Hour).UnixMilli()
	if err != nil || st.Rejected == nil || st.Rejected.Reason != "no invite" || st.Rejected.DecidedBy != "admin" ||
		st.NextAt != next || st.CanApply() {
		t.Fatalf("after the rejection: %+v, %v", st, err)
	}
	requests.CaptchaPassed(501)
	_, err = requests.Create(501, "")
	var refusal *SubRequestRefusal
	if !errors.As(err, &refusal) || refusal.Code != SubRequestCoolingDown || refusal.Until != next {
		t.Fatalf("within 7 days: %v", err)
	}
	now = now.Add(7*24*time.Hour + time.Minute)
	if st, _ := requests.Status(501); st.Rejected != nil || !st.CanApply() {
		t.Errorf("after 7 days: %+v", st)
	}
	mustRequest(t, 501, "")
}

// TestRequestRefusedToAUserOrABlockedAccount (#188 point 8): an account that
// has a user never asks; a blocked account neither, until unblocked. The
// block is on the Telegram account and outlives its nick and name updates.
func TestRequestRefusedToAUserOrABlockedAccount(t *testing.T) {
	opsFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	requestClock(t, &now)
	requests := &SubRequestService{}
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 501, InboundIds: []int{1}})

	requests.CaptchaPassed(501)
	if _, err := requests.Create(501, ""); requestRefusal(err) != SubRequestHasUser {
		t.Errorf("an account with a user: %v", err)
	}
	if st, _ := requests.Status(501); !st.HasUser || st.CanApply() {
		t.Errorf("status of an account with a user: %+v", st)
	}

	if err := requests.SetBlocked(502, true); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTgAccount(model.TgAccount{TgId: 502, Username: "spammer", LastSeen: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	requests.CaptchaPassed(502)
	if _, err := requests.Create(502, ""); requestRefusal(err) != SubRequestBlocked {
		t.Errorf("a blocked account: %v", err)
	}
	if st, _ := requests.Status(502); !st.Blocked || st.CanApply() {
		t.Errorf("status of a blocked account: %+v", st)
	}
	if err := requests.SetBlocked(502, false); err != nil {
		t.Fatal(err)
	}
	mustRequest(t, 502, "")
}

// TestRequestCommentUpTo200Characters: the comment is at most 200
// characters, counted as a person counts them, not in bytes.
func TestRequestCommentUpTo200Characters(t *testing.T) {
	opsFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	requestClock(t, &now)
	requests := &SubRequestService{}
	requests.CaptchaPassed(501)
	if _, err := requests.Create(501, strings.Repeat("я", 201)); requestRefusal(err) != SubRequestCommentTooLong {
		t.Fatalf("201 characters: %v", err)
	}
	r, err := requests.Create(501, strings.Repeat("я", 200))
	if err != nil || len([]rune(r.Comment)) != 200 {
		t.Fatalf("200 characters: %+v, %v", r, err)
	}
}
