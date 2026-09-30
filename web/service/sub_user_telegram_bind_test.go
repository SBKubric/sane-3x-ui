package service

import (
	"errors"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestResolveTelegramEntry (#219): a typed tg_id is the id; an @nick — with
// or without the '@', in any case — is the account the bot saw with it; a
// nick nobody has is the refusal that asks for an invite link.
func TestResolveTelegramEntry(t *testing.T) {
	opsFixture(t)
	if _, err := writeTgAccount(model.TgAccount{TgId: 701, Username: "Ivan_TG"}); err != nil {
		t.Fatal(err)
	}
	accounts := &TgAccountService{}
	for entry, want := range map[string]int64{"701": 701, " 42 ": 42, "@ivan_tg": 701, "Ivan_TG": 701, "@IVAN_TG ": 701} {
		if got, err := accounts.Resolve(entry); err != nil || got != want {
			t.Errorf("%q: %d, %v; want %d", entry, got, err, want)
		}
	}
	_, err := accounts.Resolve("@petr_tg")
	var conflict *SubUserConflict
	if !errors.As(err, &conflict) || conflict.Code != SubUserConflictTgNickUnknown ||
		err.Error() != "@petr_tg is unknown: the person has not written to the bot yet, send them an invite link" {
		t.Errorf("unknown nick: %v", err)
	}
	for _, bad := range []string{"", "-5", "0", "@", "ivan tg", "@иван", "12ab", "@_ivan", "@abc"} {
		if id, err := accounts.Resolve(bad); err == nil || errors.As(err, &conflict) {
			t.Errorf("%q: %d, %v", bad, id, err)
		}
	}
}

// TestTelegramByNick (#219): a user is created with the Telegram of an
// @nick, and its clients carry that account's id; a nick nobody has, or a
// tgId and a nick of two accounts, are refused.
func TestTelegramByNick(t *testing.T) {
	opsFixture(t)
	if _, err := writeTgAccount(model.TgAccount{TgId: 702, Username: "anna_tg"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTgAccount(model.TgAccount{TgId: 703, Username: "ivan_tg"}); err != nil {
		t.Fatal(err)
	}
	users := &SubUserService{}
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgNick: "@anna_tg", InboundIds: []int{1, 2}})
	if anna.TgId != 702 || anna.Clients[0].TgId != 702 || anna.Clients[1].TgId != 702 {
		t.Errorf("anna: %+v", anna)
	}
	if _, err := users.Create(SubUserCreate{Name: "petr", TgNick: "@nobody_tg", InboundIds: []int{1}}); err == nil {
		t.Error("created with a nick nobody has")
	}
	if _, err := users.Create(SubUserCreate{Name: "petr", TgId: 5, TgNick: "@ivan_tg", InboundIds: []int{1}}); err == nil {
		t.Error("created with a tgId and a nick of two accounts")
	}

	if id, err := telegramOf(703, "@ivan_tg"); err != nil || id != 703 {
		t.Errorf("the same account twice: %d, %v", id, err)
	}
}

// TestMoveTelegram (#187 point 4): an id another user has is refused with
// that user named; «Move here» takes it from them — they and their clients
// keep no id — and gives it to this user and its clients.
func TestMoveTelegram(t *testing.T) {
	opsFixture(t)
	users := &SubUserService{}
	anna := mustCreateUser(t, SubUserCreate{Name: "anna", TgId: 801, InboundIds: []int{1, 2}})
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{1}})

	_, err := users.SetTelegram(ivan.SubId, 801)
	var conflict *SubUserConflict
	if !errors.As(err, &conflict) || conflict.Code != SubUserConflictTgOwned || conflict.Owner != "anna" ||
		conflict.OwnerSubId != anna.SubId || conflict.TgId != 801 || err.Error() != "Telegram id 801 belongs to user anna" {
		t.Fatalf("refusal: %#v", err)
	}

	v, err := users.MoveTelegram(ivan.SubId, 801)
	if err != nil || v.TgId != 801 || v.Clients[0].TgId != 801 || v.TgConflict {
		t.Fatalf("move: %+v, %v", v, err)
	}
	a := mustGetUser(t, anna.SubId)
	if a.TgId != 0 || a.TgConflict {
		t.Errorf("anna: tgId %d conflict %v", a.TgId, a.TgConflict)
	}
	for _, c := range a.Clients {
		if c.TgId != 0 {
			t.Errorf("anna's client %s kept %d", c.Name, c.TgId)
		}
	}
	// Nobody has 802: a move just sets it. Robot has no Telegram.
	if v, err := users.MoveTelegram(ivan.SubId, 802); err != nil || v.TgId != 802 {
		t.Errorf("move a free id: %+v, %v", v, err)
	}
	if _, err := users.MoveTelegram(model.SubUserRobotKey, 802); err == nil {
		t.Error("robot got a Telegram")
	}
}
