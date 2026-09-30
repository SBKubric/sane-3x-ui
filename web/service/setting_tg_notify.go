package service

import (
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"
)

// The notification channel setting (#195). tgNotifyChatId lives in
// defaultValueMap and entity.AllSetting like the other bot settings; no
// migration is needed, the key appears on the first settings save. The bot
// reads it on every notification, so a new channel takes effect on save,
// without a panel restart.

// GetTgNotifyChatId is the chat id or @username of the notification
// channel, "" when none is set.
func (s *SettingService) GetTgNotifyChatId() (string, error) {
	return s.getString("tgNotifyChatId")
}

// SetTgNotifyChatId stores the notification channel, "" for none (#202: the
// bot's «📣 Notification channel» screen). It refuses a value Telegram
// could not address, as the settings form does.
func (s *SettingService) SetTgNotifyChatId(value string) error {
	value = strings.TrimSpace(value)
	if !entity.ValidTgNotifyChatId(value) {
		return common.NewError("notification channel must be a chat id (-100…) or an @username:", value)
	}
	return s.setString("tgNotifyChatId", value)
}
