package service

import (
	"fmt"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

func init() {
	database.RegisterPostMigrate((&SubUserService{}).Sync)
}

// Sync is the users migration (docs/spec/users.md §3). It creates robot and
// monitoring if they are missing, and a user for every subId that has
// clients but no user yet — named after the email of its first client, with
// -2, -3 on a clash. It never renames a user or touches a client, so running
// it again is a no-op.
//
// It runs at start-up (a post-migrate hook) and at the start of every
// SubUserService call: a client that arrives with a new subId through a path
// that knows nothing of users (a whole-inbound save, an import, a copy) gets
// its user before anyone can look.
func (s *SubUserService) Sync() error {
	subUserMu.Lock()
	defer subUserMu.Unlock()
	_, err := s.syncLocked()
	return err
}

// syncLocked is Sync under subUserMu; it returns the index with the users it
// created.
func (s *SubUserService) syncLocked() (*subUserIndex, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	db := database.GetDB()
	for _, tech := range []model.SubUser{
		{SubId: model.SubUserRobotKey, Name: model.SubUserRobot},
		{SubId: model.SubUserMonitoringKey, Name: model.SubUserMonitoring},
	} {
		if idx.users[tech.SubId] != nil {
			continue
		}
		u := tech
		if err := db.Create(&u).Error; err != nil {
			return nil, fmt.Errorf("create technical user %s: %w", u.Name, err)
		}
		idx.users[u.SubId] = &u
	}

	taken := idx.takenNames()
	for _, c := range idx.clients {
		if isReservedSubId(c.owner) || idx.users[c.owner] != nil {
			continue
		}
		u := model.SubUser{SubId: c.owner, Name: uniqueUserName(c.Name, taken)}
		if err := db.Create(&u).Error; err != nil {
			return nil, fmt.Errorf("create user for subId %s: %w", c.owner, err)
		}
		taken[strings.ToLower(u.Name)] = true
		idx.users[u.SubId] = &u
	}
	return idx, nil
}

// uniqueUserName is base, or base-2, base-3… — the first that is neither
// taken (ignoring case) nor reserved.
func uniqueUserName(base string, taken map[string]bool) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "user"
	}
	candidate := base
	for n := 2; taken[strings.ToLower(candidate)] || isReservedUserName(candidate); n++ {
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return candidate
}
