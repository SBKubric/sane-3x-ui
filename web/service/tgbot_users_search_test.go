package service

import (
	"strings"
	"testing"
)

// TestUsersSearchListsRankedHits: the bot's «Users» search is the service's
// fuzzy one — several hits come as a list in the service's order (exact,
// start, inside, typo), not by name, and nobody is said so with the typo
// hint.
func TestUsersSearchListsRankedHits(t *testing.T) {
	bot := usersBotFixture(t)
	mustCreateUser(t, SubUserCreate{Name: "big-ivan"})
	mustCreateUser(t, SubUserCreate{Name: "ivanov"})
	mustCreateUser(t, SubUserCreate{Name: "ivan"})
	mustCreateUser(t, SubUserCreate{Name: "iwan"})

	press(t, bot, "usr_menu")
	reply := typeText(t, bot, "ivan")
	if got := strings.Join(buttonTexts(t, reply.keyboard), "|"); got != "⏸ ivan · — · 0/∞ · until —|⏸ ivanov · — · 0/∞ · until —|"+
		"⏸ big-ivan · — · 0/∞ · until —|⏸ iwan · — · 0/∞ · until —" {
		t.Errorf("ranked list: %q", got)
	}
	if !strings.Contains(reply.text, "«ivan»: 4") || stateOf(usersTestChat) != usersStateSearch {
		t.Errorf("found: %+v", reply)
	}

	press(t, bot, "usr_menu")
	reply = typeText(t, bot, "qwerty")
	if !strings.Contains(reply.text, "forgives typos") || reply.route != "" {
		t.Errorf("nobody: %+v", reply)
	}
}
