package service

import (
	"context"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
)

// fakeClock is a clock the test moves by hand.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func tgAccountOf(t *testing.T, tgId int64) (username, first, last string, lastSeen int64) {
	t.Helper()
	a, err := (&TgAccountService{}).Get(tgId)
	if err != nil {
		t.Fatalf("Get(%d): %v", tgId, err)
	}
	if a == nil {
		t.Fatalf("account %d not stored", tgId)
	}
	return a.Username, a.FirstName, a.LastName, a.LastSeen
}

// TestTgAccountNoteStoresAnySender: the first message of anyone — no user,
// no admin — makes their account, with the nick, the name and when it was
// seen.
func TestTgAccountNoteStoresAnySender(t *testing.T) {
	initUsersTestDB(t)
	clock := &fakeClock{t: time.UnixMilli(1_700_000_000_000)}
	r := newTgAccountRecorder(clock.now)

	wrote, err := r.Note(telego.User{ID: 501, Username: "ivan_k", FirstName: "Ivan", LastName: "K"})
	if err != nil || !wrote {
		t.Fatalf("Note = %v, %v", wrote, err)
	}
	if u, f, l, seen := tgAccountOf(t, 501); u != "ivan_k" || f != "Ivan" || l != "K" || seen != 1_700_000_000_000 {
		t.Errorf("stored %q %q %q %d", u, f, l, seen)
	}
}

// TestTgAccountNoteThrottlesWrites: the same account seen again within a
// minute writes nothing, unless its nick or name changed; a minute later
// last_seen moves on.
func TestTgAccountNoteThrottlesWrites(t *testing.T) {
	initUsersTestDB(t)
	start := time.UnixMilli(1_700_000_000_000)
	clock := &fakeClock{t: start}
	r := newTgAccountRecorder(clock.now)
	ivan := telego.User{ID: 501, Username: "ivan", FirstName: "Ivan"}

	if wrote, _ := r.Note(ivan); !wrote {
		t.Fatal("the first sight wrote nothing")
	}
	for i := 1; i <= 5; i++ {
		clock.t = start.Add(time.Duration(i) * 10 * time.Second)
		if wrote, err := r.Note(ivan); wrote || err != nil {
			t.Fatalf("seen again after %v: wrote=%v err=%v", clock.t.Sub(start), wrote, err)
		}
	}
	if _, _, _, seen := tgAccountOf(t, 501); seen != start.UnixMilli() {
		t.Errorf("last_seen moved within the minute: %d", seen)
	}

	// A new nick is written at once.
	clock.t = start.Add(55 * time.Second)
	renamed := ivan
	renamed.Username = "ivan_new"
	if wrote, _ := r.Note(renamed); !wrote {
		t.Error("a new nick was not written")
	}
	if u, _, _, seen := tgAccountOf(t, 501); u != "ivan_new" || seen != clock.t.UnixMilli() {
		t.Errorf("after the rename: %q %d", u, seen)
	}

	// A minute after the last write, the next sight writes.
	clock.t = start.Add(55*time.Second + time.Minute)
	if wrote, _ := r.Note(renamed); !wrote {
		t.Error("a minute later nothing was written")
	}
	if _, _, _, seen := tgAccountOf(t, 501); seen != clock.t.UnixMilli() {
		t.Errorf("last_seen = %d, want %d", seen, clock.t.UnixMilli())
	}
}

// TestTgAccountNickIsUnique: a nick seen on another account moves there —
// the last seen wins, ignoring case — and the old account keeps no nick.
// When the old account shows up again with a nick, it is written at once.
func TestTgAccountNickIsUnique(t *testing.T) {
	initUsersTestDB(t)
	clock := &fakeClock{t: time.UnixMilli(1_700_000_000_000)}
	r := newTgAccountRecorder(clock.now)

	if _, err := r.Note(telego.User{ID: 501, Username: "Maria", FirstName: "Old"}); err != nil {
		t.Fatal(err)
	}
	clock.t = clock.t.Add(time.Second)
	if _, err := r.Note(telego.User{ID: 502, Username: "maria", FirstName: "New"}); err != nil {
		t.Fatal(err)
	}
	if u, _, _, _ := tgAccountOf(t, 501); u != "" {
		t.Errorf("the old account kept the nick %q", u)
	}
	if u, _, _, _ := tgAccountOf(t, 502); u != "maria" {
		t.Errorf("the new account has %q", u)
	}
	got, err := (&TgAccountService{}).ByUsername("@MARIA")
	if err != nil || got == nil || got.TgId != 502 {
		t.Errorf("ByUsername = %+v, %v", got, err)
	}

	// The old account takes it back within the minute: written at once.
	clock.t = clock.t.Add(time.Second)
	if wrote, _ := r.Note(telego.User{ID: 501, Username: "Maria", FirstName: "Old"}); !wrote {
		t.Error("the old account's nick was not written back")
	}
	if u, _, _, _ := tgAccountOf(t, 501); u != "Maria" {
		t.Errorf("the old account has %q", u)
	}
	if u, _, _, _ := tgAccountOf(t, 502); u != "" {
		t.Errorf("the new account kept %q", u)
	}
}

// TestTgAccountNoteSkipsBots: a bot (a channel's signature, another bot) is
// no account of a person.
func TestTgAccountNoteSkipsBots(t *testing.T) {
	initUsersTestDB(t)
	r := newTgAccountRecorder(time.Now)
	if wrote, err := r.Note(telego.User{ID: 900, IsBot: true, Username: "somebot"}); wrote || err != nil {
		t.Fatalf("Note(bot) = %v, %v", wrote, err)
	}
	if a, err := (&TgAccountService{}).Get(900); err != nil || a != nil {
		t.Errorf("bot stored: %+v, %v", a, err)
	}
}

// TestBotNotesEverySender: the bot's middleware records the sender of a
// message and of a button press — admin or not — and hands the update on.
func TestBotNotesEverySender(t *testing.T) {
	initUsersTestDB(t)
	prev := tgAccounts
	tgAccounts = newTgAccountRecorder(time.Now)
	t.Cleanup(func() { tgAccounts = prev })

	group := &th.HandlerGroup{}
	group.Use((&Tgbot{}).noteSender)
	handled := 0
	group.Handle(func(ctx *th.Context, update telego.Update) error { handled++; return nil })

	updates := []telego.Update{
		{Message: &telego.Message{From: &telego.User{ID: 601, Username: "stranger"}, Text: "hi"}},
		{CallbackQuery: &telego.CallbackQuery{From: telego.User{ID: 602, FirstName: "Presser"}, Data: "my_sub x"}},
		{Message: &telego.Message{Text: "a channel post has no sender"}},
	}
	for _, u := range updates {
		if err := group.HandleUpdate(context.Background(), nil, u); err != nil {
			t.Fatal(err)
		}
	}
	if handled != len(updates) {
		t.Errorf("%d of %d updates reached their handler", handled, len(updates))
	}
	if u, _, _, _ := tgAccountOf(t, 601); u != "stranger" {
		t.Errorf("message sender: %q", u)
	}
	if _, f, _, _ := tgAccountOf(t, 602); f != "Presser" {
		t.Errorf("presser: %q", f)
	}
}
