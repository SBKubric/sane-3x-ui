package service

import (
	"regexp"
	"testing"
	"time"
)

// inviteClock moves the invites' clock to now for the test.
func inviteClock(t *testing.T, now time.Time) {
	t.Helper()
	prev := tgInviteNow
	tgInviteNow = func() time.Time { return now }
	t.Cleanup(func() { tgInviteNow = prev })
}

// TestTgInviteLifecycle (#187 point 2): an invite is one per user until it is
// reissued, which makes the old token worthless; a redeem links the sender
// to the user and uses the token up; a used token or one past its seven
// days is refused and links nobody.
func TestTgInviteLifecycle(t *testing.T) {
	opsFixture(t)
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	inviteClock(t, start)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1, 2}})
	invites := &TgInviteService{}

	first, err := invites.Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{32,64}$`).MatchString(first.Token) {
		t.Errorf("token %q: Telegram takes up to 64 of [A-Za-z0-9_-]", first.Token)
	}
	if want := start.Add(7 * 24 * time.Hour).UnixMilli(); first.ExpiresAt != want {
		t.Errorf("expires %d, want %d", first.ExpiresAt, want)
	}
	again, err := invites.Invite(ivan.SubId)
	if err != nil || again.Token != first.Token {
		t.Errorf("the open invite is shown again: %+v, %v", again, err)
	}

	second, err := invites.Reissue(ivan.SubId)
	if err != nil || second.Token == first.Token {
		t.Fatalf("reissue: %+v, %v", second, err)
	}
	if r, err := invites.Redeem(first.Token, 501); err != nil || r.Outcome != TgInviteUsed || r.User == nil || r.User.Name != "ivan" {
		t.Errorf("the reissued token: %+v, %v", r, err)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != 0 {
		t.Fatalf("a revoked token linked %d", v.TgId)
	}

	r, err := invites.Redeem(second.Token, 501)
	if err != nil || r.Outcome != TgInviteLinked || r.User.TgId != 501 {
		t.Fatalf("redeem: %+v, %v", r, err)
	}
	v := mustGetUser(t, ivan.SubId)
	for _, c := range v.Clients {
		if c.TgId != 501 {
			t.Errorf("client %s: tgId %d", c.Name, c.TgId)
		}
	}
	// The same person again sees the subscription; anybody else is refused.
	if r, err := invites.Redeem(second.Token, 501); err != nil || r.Outcome != TgInviteAlready {
		t.Errorf("the same person again: %+v, %v", r, err)
	}
	if r, err := invites.Redeem(second.Token, 502); err != nil || r.Outcome != TgInviteUsed {
		t.Errorf("somebody else with a used token: %+v, %v", r, err)
	}
	if cur, err := invites.Current(ivan.SubId); err != nil || cur != nil {
		t.Errorf("a used invite is still open: %+v, %v", cur, err)
	}

	// Seven days pass on an invite of another user.
	petr := mustCreateUser(t, SubUserCreate{Name: "petr", InboundIds: []int{1}})
	late, err := invites.Invite(petr.SubId)
	if err != nil {
		t.Fatal(err)
	}
	inviteClock(t, start.Add(7*24*time.Hour+time.Second))
	if r, err := invites.Redeem(late.Token, 503); err != nil || r.Outcome != TgInviteExpired || r.User.Name != "petr" {
		t.Errorf("expired: %+v, %v", r, err)
	}
	if v := mustGetUser(t, petr.SubId); v.TgId != 0 {
		t.Errorf("an expired token linked %d", v.TgId)
	}
	fresh, err := invites.Invite(petr.SubId)
	if err != nil || fresh.Token == late.Token {
		t.Errorf("an expired invite is replaced: %+v, %v", fresh, err)
	}

	// A token nobody issued names nobody.
	if r, err := invites.Redeem("no-such-token", 504); err != nil || r.Outcome != TgInviteUsed || r.User != nil {
		t.Errorf("unknown token: %+v, %v", r, err)
	}
	// The technical users get no invite.
	if _, err := invites.Invite("@robot"); err == nil {
		t.Error("robot got an invite")
	}
}

// TestTgInviteTakenAccount (#187 point 4): a person whose Telegram is some
// other user's already is refused, with that user named for the admins,
// and nothing moves; the invite stays open.
func TestTgInviteTakenAccount(t *testing.T) {
	opsFixture(t)
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgId: 601, InboundIds: []int{1}})
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	invites := &TgInviteService{}
	inv, err := invites.Invite(ivan.SubId)
	if err != nil {
		t.Fatal(err)
	}
	r, err := invites.Redeem(inv.Token, 601)
	if err != nil || r.Outcome != TgInviteTaken || r.User.Name != "ivan" || r.Owner == nil || r.Owner.Name != "anna" {
		t.Fatalf("taken: %+v, %v", r, err)
	}
	if v := mustGetUser(t, anna.SubId); v.TgId != 601 {
		t.Errorf("anna lost her Telegram: %d", v.TgId)
	}
	if v := mustGetUser(t, ivan.SubId); v.TgId != 0 {
		t.Errorf("ivan took it: %d", v.TgId)
	}
	if cur, err := invites.Current(ivan.SubId); err != nil || cur == nil || cur.Token != inv.Token {
		t.Errorf("the invite is gone: %+v, %v", cur, err)
	}
}
