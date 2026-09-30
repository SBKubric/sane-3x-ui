package service

import (
	"strings"
	"testing"
)

// TestClientCardSaysXrayEmail: the bot's client card calls a client's email
// «xray email» (#186 point 7), in English and in Russian, so it is not taken
// for the user's contact email.
func TestClientCardSaysXrayEmail(t *testing.T) {
	bot := usersBotFixture(t)
	for _, locale := range []string{"en-US", "ru-RU"} {
		initTestBotLocale(t, locale)
		reply := clientPress(t, bot, "client_get_usage other-nl")
		if !strings.Contains(reply.text, "📧 xray email: other-nl") {
			t.Errorf("%s card:\n%s", locale, reply.text)
		}
	}
}
