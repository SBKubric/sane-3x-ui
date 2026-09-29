package service

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
