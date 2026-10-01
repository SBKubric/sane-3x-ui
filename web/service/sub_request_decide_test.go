package service

import (
	"slices"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// An admin's decisions on requests (#188 points 4–7, #221): approve with the
// request defaults or with changes, reject, block.

// decideFixture is the users fixture — vless 1, trojan 2 and awg 5 enabled,
// vmess 3 disabled — with the requests' clock at 30.09.2026 12:00 and the
// account 555, @petrov «Petr Petrov», which has left a request.
func decideFixture(t *testing.T) (*time.Time, *model.SubRequest) {
	t.Helper()
	usersBotFixture(t)
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)
	requestClock(t, &now)
	if _, err := writeTgAccount(model.TgAccount{TgId: 555, Username: "petrov", FirstName: "Petr", LastName: "Petrov"}); err != nil {
		t.Fatal(err)
	}
	return &now, mustRequest(t, 555, "from work")
}

// userInbounds are the inbounds a user has clients in, in order.
func userInbounds(v *SubUserView) []int {
	var ids []int
	for _, c := range v.Clients {
		ids = append(ids, c.InboundId)
	}
	slices.Sort(ids)
	return ids
}

// TestApproveWithTheRequestDefaults: «✅ Approve» makes a user named after
// the @nick with a client in every enabled inbound, 50 GB each and 30 days
// from the first use, bound to the applicant's Telegram, and marks the
// request approved by the admin; a second approval is refused.
func TestApproveWithTheRequestDefaults(t *testing.T) {
	now, r := decideFixture(t)
	*now = now.Add(time.Hour)
	requests := &SubRequestService{}

	v, err := requests.ApproveWithDefaults(r.Id, "@admin")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "petrov" || v.TgId != 555 || !slices.Equal(userInbounds(v), []int{1, 2, 5}) {
		t.Fatalf("the user: %s tg %d inbounds %v", v.Name, v.TgId, userInbounds(v))
	}
	for _, c := range v.Clients {
		if c.TotalGB != 50<<30 || c.ExpiryTime != -30*86400000 || c.TgId != 555 {
			t.Errorf("client %s: %d bytes, expiry %d, tg %d", c.Name, c.TotalGB, c.ExpiryTime, c.TgId)
		}
	}
	got, err := requests.Get(r.Id)
	if err != nil || got.Status != model.SubRequestApproved || got.DecidedBy != "@admin" || got.DecidedAt != now.UnixMilli() {
		t.Fatalf("the request: %+v, %v", got, err)
	}
	if st, _ := requests.Status(555); !st.HasUser {
		t.Errorf("the account has no user: %+v", st)
	}
	if _, err := requests.ApproveWithDefaults(r.Id, "@admin"); requestRefusal(err) != SubRequestNotPending {
		t.Errorf("approved twice: %v", err)
	}
}

// TestApproveFollowsTheSettings: the defaults the panel's settings hold —
// chosen inbounds, other limits, none — are what the user gets.
func TestApproveFollowsTheSettings(t *testing.T) {
	_, r := decideFixture(t)
	setSetting(t, "subRequestInbounds", "2,3")
	setSetting(t, "subRequestTrafficGB", "0")
	setSetting(t, "subRequestExpiryDays", "7")

	v, err := (&SubRequestService{}).ApproveWithDefaults(r.Id, "@admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(userInbounds(v), []int{2, 3}) {
		t.Fatalf("inbounds %v, want the chosen 2 and 3 (a disabled one too)", userInbounds(v))
	}
	for _, c := range v.Clients {
		if c.TotalGB != 0 || c.ExpiryTime != -7*86400000 {
			t.Errorf("client %s: %d bytes, expiry %d", c.Name, c.TotalGB, c.ExpiryTime)
		}
	}

	// Inbounds that are all gone leave nothing to approve with: refused,
	// the request still waits.
	mustRequest(t, 556, "")
	setSetting(t, "subRequestInbounds", "40,41")
	pending, _ := (&SubRequestService{}).Pending()
	if _, err := (&SubRequestService{}).ApproveWithDefaults(pending[0].Id, "@admin"); err == nil {
		t.Fatal("approved with no inbound")
	}
	if got, _ := (&SubRequestService{}).Get(pending[0].Id); got.Status != model.SubRequestPending {
		t.Errorf("the request after a failed approval: %+v", got)
	}
}

// TestApproveWithChanges: the new user's name, inbounds and limits are the
// admin's; the Telegram is always the applicant's.
func TestApproveWithChanges(t *testing.T) {
	_, r := decideFixture(t)
	requests := &SubRequestService{}

	draft, err := requests.Draft(r.Id)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Name != "petrov" || draft.TgId != 555 || !slices.Equal(draft.InboundIds, []int{1, 2, 5}) ||
		draft.TotalGB != 50<<30 || draft.ExpiryTime != -30*86400000 {
		t.Fatalf("the draft: %+v", draft)
	}
	draft.Name, draft.InboundIds, draft.TotalGB, draft.TgId = "petya", []int{1}, 10<<30, 999
	v, err := requests.Approve(r.Id, "@admin", draft)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "petya" || v.TgId != 555 || !slices.Equal(userInbounds(v), []int{1}) || v.Clients[0].TotalGB != 10<<30 {
		t.Fatalf("the user: %s tg %d %+v", v.Name, v.TgId, v.Clients)
	}
	if got, _ := requests.Get(r.Id); got.Status != model.SubRequestApproved {
		t.Errorf("the request: %+v", got)
	}
}

// TestApproveRefusedLeavesTheRequestWaiting: a create the users' rules
// refuse — the Telegram meanwhile another user's — approves nothing.
func TestApproveRefusedLeavesTheRequestWaiting(t *testing.T) {
	_, r := decideFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 555, InboundIds: []int{1}})

	if _, err := (&SubRequestService{}).ApproveWithDefaults(r.Id, "@admin"); err == nil {
		t.Fatal("approved with a Telegram another user has")
	}
	if got, _ := (&SubRequestService{}).Get(r.Id); got.Status != model.SubRequestPending {
		t.Errorf("the request: %+v", got)
	}
	if _, err := (&SubUserService{}).Find("petrov"); err == nil {
		t.Error("a user petrov was made")
	}
}

// TestRequestUserNames (#188 point 6): the user is named after the @nick,
// else after the Telegram name, else the id; a name taken gets -2, -3.
func TestRequestUserNames(t *testing.T) {
	_, r := decideFixture(t)
	requests := &SubRequestService{}
	mustCreateUser(t, SubUserCreate{Name: "Petrov"})
	mustCreateUser(t, SubUserCreate{Name: "petrov-2"})

	d, err := requests.Draft(r.Id)
	if err != nil || d.Name != "petrov-3" {
		t.Fatalf("a taken nick: %q, %v", d.Name, err)
	}

	for _, account := range []model.TgAccount{{TgId: 601, FirstName: "Anna", LastName: "Ivanova"}, {TgId: 602}} {
		if _, err := writeTgAccount(account); err != nil {
			t.Fatal(err)
		}
		mustRequest(t, account.TgId, "")
	}
	pending, err := requests.Pending()
	if err != nil || len(pending) != 3 {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
	want := map[int64]string{555: "petrov-3", 601: "Anna Ivanova", 602: "tg602"}
	for _, p := range pending {
		d, err := requests.Draft(p.Id)
		if err != nil || d.Name != want[p.TgId] {
			t.Errorf("account %d: %q, %v; want %q", p.TgId, d.Name, err, want[p.TgId])
		}
	}
	mustCreateUser(t, SubUserCreate{Name: "anna ivanova"})
	if d, _ := requests.Draft(pending[1].Id); d.Name != "Anna Ivanova-2" {
		t.Errorf("a taken name: %q", d.Name)
	}
}

// TestRequestLists: the pending requests oldest first, an account's past
// rejections newest first, and the blocked accounts.
func TestRequestLists(t *testing.T) {
	now, first := decideFixture(t)
	requests := &SubRequestService{}
	*now = now.Add(time.Hour)
	second := mustRequest(t, 556, "")

	pending, err := requests.Pending()
	if err != nil || len(pending) != 2 || pending[0].Id != first.Id || pending[1].Id != second.Id {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
	if n, err := requests.CountPending(); err != nil || n != 2 {
		t.Errorf("count: %d, %v", n, err)
	}

	if err := requests.Reject(first.Id, "@admin", "no places"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(8 * 24 * time.Hour)
	again := mustRequest(t, 555, "")
	*now = now.Add(time.Hour)
	if err := requests.Reject(again.Id, "@admin", "still no places"); err != nil {
		t.Fatal(err)
	}
	rejections, err := requests.Rejections(555)
	if err != nil || len(rejections) != 2 || rejections[0].Reason != "still no places" || rejections[1].Reason != "no places" {
		t.Fatalf("rejections: %+v, %v", rejections, err)
	}
	if pending, _ := requests.Pending(); len(pending) != 1 || pending[0].Id != second.Id {
		t.Errorf("pending after the rejections: %+v", pending)
	}
}

// TestBlockAndUnblock: «🚫 Block» turns the account's pending request down
// and takes no more from it; «Unblock» lets it ask again once the week after
// that rejection is over. Blocking an account with nothing pending only
// blocks it. A block forgets the account's captcha pass.
func TestBlockAndUnblock(t *testing.T) {
	now, r := decideFixture(t)
	requests := &SubRequestService{}

	if passed, err := (&TgCaptchaService{}).Passed(555); err != nil || !passed {
		t.Fatalf("the applicant's captcha before the block: %v, %v", passed, err)
	}
	if err := requests.Block(555, "@admin"); err != nil {
		t.Fatal(err)
	}
	if passed, err := (&TgCaptchaService{}).Passed(555); err != nil || passed {
		t.Errorf("the applicant's captcha after the block: %v, %v", passed, err)
	}
	got, _ := requests.Get(r.Id)
	if got.Status != model.SubRequestRejected || got.DecidedBy != "@admin" {
		t.Errorf("the pending request after the block: %+v", got)
	}
	st, _ := requests.Status(555)
	if !st.Blocked || st.CanApply() {
		t.Errorf("status: %+v", st)
	}
	(&TgCaptchaService{}).Pass(555)
	if _, err := requests.Create(555, ""); requestRefusal(err) != SubRequestBlocked {
		t.Errorf("a request from a blocked account: %v", err)
	}
	if err := requests.Block(700, "@admin"); err != nil {
		t.Fatal(err)
	}
	blocked, err := requests.Blocked()
	if err != nil || len(blocked) != 2 || blocked[0].TgId != 555 || blocked[1].TgId != 700 {
		t.Fatalf("blocked: %+v, %v", blocked, err)
	}

	if err := requests.SetBlocked(555, false); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(7*24*time.Hour + time.Minute)
	if st, _ := requests.Status(555); st.Blocked || !st.CanApply() {
		t.Errorf("after the unblock: %+v", st)
	}
	if blocked, _ := requests.Blocked(); len(blocked) != 1 || blocked[0].TgId != 700 {
		t.Errorf("blocked after the unblock: %+v", blocked)
	}
}
