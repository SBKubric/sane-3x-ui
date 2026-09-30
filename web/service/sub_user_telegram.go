package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"gorm.io/gorm"
)

// The user's Telegram (#186 points 2, 3, 8 and 9; docs/spec/users.md §11).
// sub_users.tg_id is the source: unique when not 0, written onto every
// client of the user whenever the user is saved, so upstream's notifications
// and /usage, which read the clients, keep working. Our own code reads it off
// the user only.
//
// The migration moves the clients' tgId onto a user that has none only when
// its clients agree and nobody else has that id; anything else leaves the
// user without one and its clients as they are, and the user shows a
// conflict until an admin assigns one of the ids or unlinks Telegram.

func init() {
	database.RegisterPostMigrate(uniqueSubUserTgIds)
}

// subUserTgIdIndex makes a user's tgId unique among the users that have one.
const subUserTgIdIndex = `CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_users_tg_id ON sub_users(tg_id) WHERE tg_id <> 0`

// uniqueSubUserTgIds is the post-migrate step that puts the unique index on
// sub_users.tg_id. Users that share an id from before the rule lose it
// first: none of them may keep it, they become conflicts (their clients keep
// the id) for an admin to resolve.
func uniqueSubUserTgIds() error {
	db := database.GetDB()
	return db.Transaction(func(tx *gorm.DB) error {
		shared := tx.Model(&model.SubUser{}).Select("tg_id").Where("tg_id <> 0").Group("tg_id").Having("count(*) > 1")
		res := tx.Model(&model.SubUser{}).Where("tg_id IN (?)", shared).Update("tg_id", 0)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			logger.Warningf("users: %d users shared a Telegram id; they have none now and show a conflict", res.RowsAffected)
		}
		return tx.Exec(subUserTgIdIndex).Error
	})
}

// clientTgIds are the distinct tgIds other than 0 on the clients, in their
// order.
func clientTgIds(clients []SubUserClient) []int64 {
	var out []int64
	for _, c := range clients {
		if c.TgId != 0 && !slices.Contains(out, c.TgId) {
			out = append(out, c.TgId)
		}
	}
	return out
}

// tgIdOwner is the regular user other than key whose own tgId is tgId.
func (idx *subUserIndex) tgIdOwner(tgId int64, key string) *model.SubUser {
	for _, u := range idx.users {
		if u.SubId != key && !u.IsTechnical() && u.TgId == tgId {
			return u
		}
	}
	return nil
}

// tgIdCarriers are the regular users other than key one of whose clients
// carries tgId, by name.
func (idx *subUserIndex) tgIdCarriers(tgId int64, key string) []string {
	var out []string
	for _, u := range idx.sortedUsers() {
		if u.SubId == key || u.IsTechnical() {
			continue
		}
		if slices.ContainsFunc(idx.clientsOf(u.SubId), func(c SubUserClient) bool { return c.TgId == tgId }) {
			out = append(out, u.Name)
		}
	}
	return out
}

// adoptTelegramIds is the migration's step for tgIds (#186 point 3): a
// regular user without one takes its clients' id when they agree on exactly
// one and no other user has it, on its row or on a client. It writes the
// user rows only, and is a no-op the second time.
func (idx *subUserIndex) adoptTelegramIds(db *gorm.DB) error {
	for _, u := range idx.sortedUsers() {
		if u.IsTechnical() || u.TgId != 0 {
			continue
		}
		ids := clientTgIds(idx.clientsOf(u.SubId))
		if len(ids) != 1 {
			continue
		}
		id := ids[0]
		if idx.tgIdOwner(id, u.SubId) != nil || len(idx.tgIdCarriers(id, u.SubId)) > 0 {
			continue
		}
		if err := db.Model(&model.SubUser{}).Where("sub_id = ? AND tg_id = 0", u.SubId).Update("tg_id", id).Error; err != nil {
			return fmt.Errorf("move Telegram id %d to user %s: %w", id, u.Name, err)
		}
		u.TgId = id
	}
	return nil
}

// tgConflict reports whether a client of the regular user carries a tgId
// other than the user's.
func (idx *subUserIndex) tgConflict(u *model.SubUser) bool {
	if u.IsTechnical() {
		return false
	}
	return slices.ContainsFunc(idx.clientsOf(u.SubId), func(c SubUserClient) bool { return c.TgId != 0 && c.TgId != u.TgId })
}

// SubUserTelegram is a user's Telegram as the card that resolves a conflict
// shows it.
type SubUserTelegram struct {
	SubId string `json:"subId"`
	Name  string `json:"name"`
	TgId  int64  `json:"tgId"` // the user's, 0 for none
	// Account is the user's Telegram account as the bot last saw it; nil
	// when it has none or the bot never saw it.
	Account  *model.TgAccount `json:"account"`
	Conflict bool             `json:"conflict"`
	// Candidates are the tgIds on the user's clients, in the clients' order.
	Candidates []SubUserTgCandidate `json:"candidates"`
}

// SubUserTgCandidate is one tgId found on a user's clients.
type SubUserTgCandidate struct {
	TgId    int64            `json:"tgId"`
	Account *model.TgAccount `json:"account"`
	Clients []string         `json:"clients"` // the user's clients that carry it
	// Owner is the other user whose tgId it is ("" for none): it cannot be
	// assigned while it is theirs.
	Owner      string `json:"owner"`
	OwnerSubId string `json:"ownerSubId"`
	// AlsoOn are the other users whose clients carry it.
	AlsoOn []string `json:"alsoOn"`
	// Assignable: not the user's already and nobody else's.
	Assignable bool `json:"assignable"`
}

// Telegram returns the user's Telegram: its tgId and account, and the tgIds
// its clients carry with who else has each.
func (s *SubUserService) Telegram(key string) (*SubUserTelegram, error) {
	idx, err := s.synced()
	if err != nil {
		return nil, err
	}
	u := idx.users[key]
	if u == nil {
		return nil, common.NewErrorf("user with subId %q not found", key)
	}
	accounts := &TgAccountService{}
	account := func(id int64) *model.TgAccount {
		if id == 0 {
			return nil
		}
		a, err := accounts.Get(id)
		if err != nil {
			logger.Warning("telegram accounts:", err)
		}
		return a
	}
	out := &SubUserTelegram{SubId: u.SubId, Name: u.Name, TgId: u.TgId, Account: account(u.TgId),
		Conflict: idx.tgConflict(u), Candidates: []SubUserTgCandidate{}}
	if u.IsTechnical() {
		return out, nil
	}
	clients := idx.clientsOf(u.SubId)
	for _, id := range clientTgIds(clients) {
		c := SubUserTgCandidate{TgId: id, Account: account(id), AlsoOn: idx.tgIdCarriers(id, u.SubId)}
		for _, cl := range clients {
			if cl.TgId == id {
				c.Clients = append(c.Clients, cl.Name)
			}
		}
		if owner := idx.tgIdOwner(id, u.SubId); owner != nil {
			c.Owner, c.OwnerSubId = owner.Name, owner.SubId
		}
		if c.AlsoOn == nil {
			c.AlsoOn = []string{}
		}
		c.Assignable = c.Owner == "" && id != u.TgId
		out.Candidates = append(out.Candidates, c)
	}
	return out, nil
}

// SetTelegram gives the user the Telegram id tgId — «Назначить <tg_id>» of
// a conflict, or a plain change — and writes it onto all of its clients. An
// id another user has is refused: an account and a user are one to one.
// 0 is UnlinkTelegram.
func (s *SubUserService) SetTelegram(key string, tgId int64) (*SubUserView, error) {
	if tgId < 0 {
		return nil, common.NewErrorf("%d is not a Telegram user id", tgId)
	}
	subUserMu.Lock()
	defer subUserMu.Unlock()
	idx, u, err := s.regularUser(key)
	if err != nil {
		return nil, err
	}
	if owner := idx.tgIdOwner(tgId, u.SubId); tgId != 0 && owner != nil {
		return nil, common.NewErrorf("Telegram id %d belongs to user %s", tgId, owner.Name)
	}
	if err := database.GetDB().Model(&model.SubUser{}).Where("sub_id = ?", u.SubId).Update("tg_id", tgId).Error; err != nil {
		return nil, err
	}
	u.TgId = tgId
	if err := s.writeClientsTgId(idx, u.SubId, tgId); err != nil {
		return nil, err
	}
	return s.viewOf(u.SubId)
}

// UnlinkTelegram — «Отвязать Telegram» — leaves the user and every one of its
// clients without a Telegram id. The account itself (tg_accounts) stays.
func (s *SubUserService) UnlinkTelegram(key string) (*SubUserView, error) {
	return s.SetTelegram(key, 0)
}

// writeClientsTgId writes tgId onto every client of the user under key that
// carries another one: into the xray client through the panel's update
// path, onto the tunnel client's row (the tunnel itself does not use it).
func (s *SubUserService) writeClientsTgId(idx *subUserIndex, key string, tgId int64) error {
	db := database.GetDB()
	for _, c := range idx.clientsOf(key) {
		if c.TgId == tgId {
			continue
		}
		var err error
		switch c.Kind {
		case SubUserClientXray:
			err = s.updateXrayClient(idx, c, func(m map[string]any) { m["tgId"] = tgId })
		default:
			err = db.Model(&model.TunnelClient{}).Where("uuid = ?", c.Key).Update("tg_id", tgId).Error
		}
		if err != nil {
			return fmt.Errorf("write the Telegram id of client %s: %w", c.Name, err)
		}
	}
	return nil
}

// savedView is viewOf after a save of the user: its tgId, when it has one,
// is written onto all of its clients first (#186 point 2) — new ones, one
// taken over or assigned, one edited in its inbound since. A user without
// one leaves its clients alone: their ids are a conflict to resolve by hand.
// A failed write is logged; the save itself stands and the next one retries.
func (s *SubUserService) savedView(key string) (*SubUserView, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	if u := idx.users[key]; u != nil && !u.IsTechnical() && u.TgId != 0 {
		if err := s.writeClientsTgId(idx, key, u.TgId); err != nil {
			logger.Warning("users:", err)
		}
	}
	return s.viewOf(key)
}

// xrayClientTgIds reads the tgId of each client in an inbound's settings, in
// their order, whether the form stored it as a number or as a string ("777",
// "" for none, #201) — model.Client takes the number only. nil when the
// settings do not parse.
func xrayClientTgIds(settings string) []int64 {
	var parsed struct {
		Clients []struct {
			TgId json.RawMessage `json:"tgId"`
		} `json:"clients"`
	}
	if json.Unmarshal([]byte(settings), &parsed) != nil {
		return nil
	}
	out := make([]int64, len(parsed.Clients))
	for i, c := range parsed.Clients {
		raw := strings.Trim(strings.TrimSpace(string(c.TgId)), `"`)
		out[i], _ = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	}
	return out
}
