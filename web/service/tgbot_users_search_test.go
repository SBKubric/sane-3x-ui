package service

import (
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
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

// TestUsersSearchByNick: in the bot, «@nick» of the user's Telegram account
// opens its card, a typo too; the card still offers the Telegram screen of
// #210 next to what the search opened.
func TestUsersSearchByNick(t *testing.T) {
	bot := usersBotFixture(t)
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", TgId: 424242, InboundIds: []int{2}})
	if _, err := writeTgAccount(model.TgAccount{TgId: 424242, Username: "ivan_the_great"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"@ivan_the_great", "@IVAN_THE_GRAET"} {
		press(t, bot, "usr_menu")
		reply := typeText(t, bot, q)
		if reply.route != "usr_c "+ivan.SubId {
			t.Fatalf("search %q: %+v", q, reply)
		}
		if tg := button(t, reply.keyboard, "Telegram"); !strings.HasPrefix(tg, "usr_tg ") {
			t.Errorf("search %q: the Telegram button is %q", q, tg)
		}
	}
}
