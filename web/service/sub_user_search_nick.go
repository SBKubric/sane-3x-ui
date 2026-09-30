package service

import (
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// UserNicks is the @nick (without its '@') of every Telegram account a user
// points at through sub_users.tg_id, by tg_id — what Search matches a nick
// against (docs/spec/users.md §5.1, §11). An account without a user, or
// without a nick, is left out; a nick that moved to another account went
// with it (writeTgAccount clears the old one). One query, whatever the
// number of users.
func (s *TgAccountService) UserNicks() (map[int64]string, error) {
	db := database.GetDB()
	var rows []model.TgAccount
	err := db.Model(&model.TgAccount{}).Select("tg_id", "username").
		Where("username <> '' AND tg_id IN (?)", db.Model(&model.SubUser{}).Select("tg_id").Where("tg_id <> 0")).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	nicks := make(map[int64]string, len(rows))
	for _, a := range rows {
		nicks[a.TgId] = a.Username
	}
	return nicks, nil
}
