package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// searchFixture is a fresh database with the users given, stored as rows the
// way Create leaves them; clients come separately through usersInbound.
func searchFixture(t *testing.T, users ...model.SubUser) {
	t.Helper()
	initUsersTestDB(t)
	for i := range users {
		if err := database.GetDB().Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// searchNames is Search's answer as the names of the users, in its order.
func searchNames(t *testing.T, q string) string {
	t.Helper()
	users, err := (&SubUserService{}).Search(q)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Name)
	}
	return strings.Join(names, ",")
}

// TestSearchRanksExactPrefixSubstringTypo: exact hits come first, then the
// fields that start with the query, then those that contain it, then those
// a typo away; users of one rank come by name.
func TestSearchRanksExactPrefixSubstringTypo(t *testing.T) {
	searchFixture(t,
		model.SubUser{SubId: "s1", Name: "big-ivan"},
		model.SubUser{SubId: "s2", Name: "iven"},
		model.SubUser{SubId: "s3", Name: "ivanov"},
		model.SubUser{SubId: "s4", Name: "Ivan"},
		model.SubUser{SubId: "s5", Name: "oleg"},
		model.SubUser{SubId: "s6", Name: "ivana"},
		model.SubUser{SubId: "s7", Name: "ivanow"},
	)
	if got, want := searchNames(t, "ivan"), "Ivan,ivana,ivanov,ivanow,big-ivan,iven"; got != want {
		t.Errorf("Search(ivan) = %q, want %q", got, want)
	}
	// A six-letter word forgives two edits; fewer edits rank first.
	if got, want := searchNames(t, "IVANOV"), "ivanov,ivanow,big-ivan,Ivan,ivana"; got != want {
		t.Errorf("Search(IVANOV) = %q, want %q", got, want)
	}
}

// TestSearchFields: every field of the decision finds its user — the name,
// the subId, the contact email, the xray email and a tunnel client's name,
// the @nick — and a technical user is found by its name and its clients but
// never by its key.
func TestSearchFields(t *testing.T) {
	searchFixture(t,
		model.SubUser{SubId: "sub-anna-0001", Name: "anna", ContactEmail: "anna.k@example.org", TgId: 5001},
		model.SubUser{SubId: "sub-boris-0002", Name: "boris", TgId: 5002},
	)
	usersInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: uuidN(1), Email: "boris-nl", SubID: "sub-boris-0002", Enable: true},
		model.Client{ID: uuidN(2), Email: "legacy-orphan", Enable: true})
	awgPeer(t, 3, "zoya-awg", "sub-anna-0001")
	tgAccount(t, 5002, "borya_tg")

	for q, want := range map[string]string{
		"sub-anna-0001":      "anna",  // subId
		"anna.k@example.org": "anna",  // contact email
		"example.org":        "anna",  // contact email, a substring
		"boris-nl":           "boris", // xray email
		"zoya-awg":           "anna",  // tunnel client name
		"borya_tg":           "boris", // nick, typed without '@'
		"@borya_tg":          "boris", // nick, typed with '@'
		"@boris":             "",      // '@' searches the nicks alone
		"legacy-orphan":      "robot", // robot's client
		"@robot":             "",      // a technical key is no subId
		"robot":              "robot", // a technical user's name
		"monitoring":         "monitoring",
	} {
		if got := searchNames(t, q); got != want {
			t.Errorf("Search(%q) = %q, want %q", q, got, want)
		}
	}
}

// tgAccount stores a Telegram account the way the bot's middleware does: a
// nick seen on it is taken from any other account.
func tgAccount(t *testing.T, tgId int64, nick string) {
	t.Helper()
	if _, err := writeTgAccount(model.TgAccount{TgId: tgId, Username: nick}); err != nil {
		t.Fatal(err)
	}
}

// TestSearchByNick: the @nick of the account a user's tg_id points at is a
// field like the others — whole, start, typo — with or without the '@'; the
// nick of an account without a user finds nobody, and a nick that moved to
// another account finds that account's user.
func TestSearchByNick(t *testing.T) {
	searchFixture(t,
		model.SubUser{SubId: "s1", Name: "anna", TgId: 5001},
		model.SubUser{SubId: "s2", Name: "boris", TgId: 5002},
	)
	tgAccount(t, 5001, "Anna_Karenina")
	tgAccount(t, 5002, "borya")
	tgAccount(t, 9999, "ghost_writer") // wrote to the bot, no user

	for q, want := range map[string]string{
		"@anna_karenina": "anna",  // whole, ignoring case
		"anna_karenina":  "anna",  // without the '@'
		"@anna_kar":      "anna",  // start
		"@karenina":      "anna",  // inside
		"@anna_karenia":  "anna",  // a typo
		"@borja":         "boris", // a typo in a short nick
		"@ghost_writer":  "",      // an account without a user
		"@anna":          "anna",  // '@' searches nicks only: the start of anna's
		"@boris":         "",      // boris is a name, not a nick
	} {
		if got := searchNames(t, q); got != want {
			t.Errorf("Search(%q) = %q, want %q", q, got, want)
		}
	}

	// The nick shows up on boris's account: anna's loses it.
	tgAccount(t, 5002, "anna_karenina")
	if got := searchNames(t, "@anna_karenina"); got != "boris" {
		t.Errorf("after the move: %q", got)
	}
	if got := searchNames(t, "@borya"); got != "" {
		t.Errorf("the old nick of boris: %q", got)
	}
}

// TestSearchTypos: one edit in a word of up to five characters, two in a
// longer one, per field and per word; more finds nobody.
func TestSearchTypos(t *testing.T) {
	searchFixture(t,
		model.SubUser{SubId: "s1", Name: "Ivan Petrov"},
		model.SubUser{SubId: "s2", Name: "maria", ContactEmail: "masha.smirnova@mail.ru"},
	)
	for q, want := range map[string]string{
		"ivn":            "Ivan Petrov", // a short word, one edit
		"petrof":         "Ivan Petrov", // a long word, one edit
		"pitrof":         "Ivan Petrov", // a long word, two edits
		"ptrf":           "",            // a short word, two edits
		"iavn petrov":    "Ivan Petrov", // each query word finds its word
		"petrov ivan":    "Ivan Petrov", // in any order
		"ivan pteroff":   "",            // one word three edits off
		"ivanpetrov":     "Ivan Petrov", // the whole field, one edit
		"mraia":          "",            // a transposition is two edits: too many for five letters
		"smirnowa":       "maria",       // a word of the contact email
		"masha.smirnova": "maria",       // a prefix of it
		"mashka":         "maria",       // two edits from "masha"
	} {
		if got := searchNames(t, q); got != want {
			t.Errorf("Search(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestSearchShortQueries: one character matches only a whole field or its
// start, two also inside a field, and typos start at three.
func TestSearchShortQueries(t *testing.T) {
	searchFixture(t,
		model.SubUser{SubId: "s1", Name: "ab"},
		model.SubUser{SubId: "s2", Name: "xab"},
		model.SubUser{SubId: "s3", Name: "ac"},
		model.SubUser{SubId: "s4", Name: "a"},
	)
	for q, want := range map[string]string{
		"a":  "a,ab,ac", // exact, then the starts; not inside "xab"
		"b":  "",
		"ab": "ab,xab", // exact, then inside; "ac" is no typo of it
		"":   "",
		"  ": "",
	} {
		if got := searchNames(t, q); got != want {
			t.Errorf("Search(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestSearchDigits: a Telegram id — the user's or a client's — matches only
// whole; digits still find names and subIds that hold them, never by typos.
func TestSearchDigits(t *testing.T) {
	searchFixture(t,
		model.SubUser{SubId: "s1", Name: "ivan", TgId: 424242},
		model.SubUser{SubId: "s2", Name: "room-4242"},
		model.SubUser{SubId: "s3", Name: "oleg"},
		model.SubUser{SubId: "s4", Name: "nomer-424243"},
	)
	usersInbound(t, 1, model.VLESS, "nl", model.Client{ID: uuidN(1), Email: "oleg-nl", SubID: "s3", TgID: 777, Enable: true})
	for q, want := range map[string]string{
		"424242": "ivan",                   // the user's tg_id; "424243" is a digit off, no typo
		"4242":   "nomer-424243,room-4242", // no tg_id prefix; inside names
		"777":    "oleg",                   // a client's tg_id
		"77":     "",                       // no part of a tg_id
		"42424":  "nomer-424243",
	} {
		if got := searchNames(t, q); got != want {
			t.Errorf("Search(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestSearchLimit: at most twenty users, the best of them.
func TestSearchLimit(t *testing.T) {
	var users []model.SubUser
	for i := range 30 {
		users = append(users, model.SubUser{SubId: fmt.Sprintf("s%02d", i), Name: fmt.Sprintf("client-%02d", i)})
	}
	users = append(users, model.SubUser{SubId: "sx", Name: "zz-client"})
	searchFixture(t, users...)

	got, err := (&SubUserService{}).Search("client")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != SubUserSearchLimit || got[0].Name != "client-00" || got[19].Name != "client-19" {
		t.Errorf("Search(client): %d users, %s … %s", len(got), got[0].Name, got[len(got)-1].Name)
	}
}

// BenchmarkSearch: a typo query over 3000 users, each with a client — the
// index read and the matching in memory.
func BenchmarkSearch(b *testing.B) {
	initUsersTestDB(b)
	db := database.GetDB()
	var clients []model.Client
	for i := range 3000 {
		u := model.SubUser{SubId: fmt.Sprintf("sub-%05d", i), Name: fmt.Sprintf("user-%05d", i), ContactEmail: fmt.Sprintf("person%d@example.org", i)}
		if err := db.Create(&u).Error; err != nil {
			b.Fatal(err)
		}
		clients = append(clients, model.Client{ID: fmt.Sprintf("%08d-0000-0000-0000-000000000000", i), Email: u.Name + "-nl", SubID: u.SubId, Enable: true})
	}
	usersInbound(b, 1, model.VLESS, "nl", clients...)
	b.Run("search", func(b *testing.B) {
		for b.Loop() {
			if _, err := (&SubUserService{}).Search("persn123"); err != nil {
				b.Fatal(err)
			}
		}
	})
	// List reads the same index: the search's own share is the difference.
	b.Run("list", func(b *testing.B) {
		for b.Loop() {
			if _, err := (&SubUserService{}).List(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
