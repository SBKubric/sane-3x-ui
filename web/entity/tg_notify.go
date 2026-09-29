package entity

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The notification channel (#195): the Telegram chat the bot posts its
// notifications to — the periodic report, monitoring alerts, panel logins,
// CPU alerts. Empty means none is set, and then nothing is posted.

// tgChannelUsername is a public channel's or group's @username as Telegram
// allows it: 5 to 32 letters, digits and underscores, starting with a letter.
var tgChannelUsername = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{4,31}$`)

// ValidTgNotifyChatId reports whether value is a notification channel the bot
// can address: empty (none), a numeric chat id such as -1001234567890, or an
// @username.
func ValidTgNotifyChatId(value string) bool {
	if value == "" || tgChannelUsername.MatchString(value) {
		return true
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id != 0
}

// checkTgNotifyChatId trims the channel the settings form sent and refuses
// one Telegram could not address.
func checkTgNotifyChatId(s *AllSetting) error {
	s.TgNotifyChatId = strings.TrimSpace(s.TgNotifyChatId)
	if !ValidTgNotifyChatId(s.TgNotifyChatId) {
		return common.NewError("notification channel must be a chat id (-100…) or an @username:", s.TgNotifyChatId)
	}
	return nil
}
