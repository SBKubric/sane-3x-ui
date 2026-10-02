package entity

import (
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// The captcha's host (#243, docs/spec/users.md §12): where the bot opens its
// Mini App, the captcha at /third-party/<secret>/captcha. It touches nothing
// but that link: the subscriptions, their public address, the VPN name and
// the showcase stay where they are.
//
// "" or "edge" is the active edge (the default), "panel" the panel's own
// front, and anything else a hop of the chain by its name. "edge" and
// "panel" come first, so a hop with one of those names is not chosen by it.
const (
	TgCaptchaHostEdge  = "edge"
	TgCaptchaHostPanel = "panel"
)

// ValidTgCaptchaHost reports whether value is a choice the captcha's host
// takes: "", edge, panel or a hop name ([a-z0-9-]{1,32}). Whether the hop is
// in the chain, and serves https, is the bot's business when it builds the
// link: a hop may come and go after the setting is saved.
func ValidTgCaptchaHost(value string) bool {
	return value == "" || value == TgCaptchaHostEdge || value == TgCaptchaHostPanel || chain.NameValid(value)
}

// checkTgCaptchaHost trims the choice the settings form sent and refuses one
// that is neither edge, panel nor a hop name.
func checkTgCaptchaHost(s *AllSetting) error {
	s.TgCaptchaHost = strings.TrimSpace(s.TgCaptchaHost)
	if !ValidTgCaptchaHost(s.TgCaptchaHost) {
		return common.NewError("captcha host must be edge, panel or a hop name ([a-z0-9-]{1,32}):", s.TgCaptchaHost)
	}
	return nil
}
