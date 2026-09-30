package controller

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// TestUsersSearchAPI: GET /panel/api/users/search?q= is the service's fuzzy
// search behind the panel session — the users best first, an empty list for
// nobody, and 404 without a session like the rest of /panel/api.
func TestUsersSearchAPI(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "ivanov-nl", SubID: "s-ivanov", Enable: true},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "ivan", SubID: "s-ivan", Enable: true},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "iwan", SubID: "s-iwan", Enable: true})

	if w := monUIGet(r, "/panel/api/users/search?q=ivan", ""); w.Code != http.StatusNotFound {
		t.Errorf("search without a session: %d", w.Code)
	}

	names := func(q string) string {
		t.Helper()
		env := monUIDecode(t, monUIGet(r, "/panel/api/users/search?q="+q, cookie))
		if !env.Success {
			t.Fatalf("search %q refused: %s", q, env.Msg)
		}
		var users []userJSON
		if err := json.Unmarshal(env.Obj, &users); err != nil {
			t.Fatalf("search %q: %v (%s)", q, err, env.Obj)
		}
		var out []string
		for _, u := range users {
			out = append(out, u.Name)
		}
		return strings.Join(out, ",")
	}
	for q, want := range map[string]string{
		"IVAN":     "ivan,ivanov-nl,iwan", // exact, start, typo
		"s-iwan":   "iwan,ivan",           // subId; "s-ivan" is an edit away
		"ivan%20":  "ivan,ivanov-nl,iwan",
		"nobody42": "",
		"":         "",
	} {
		if got := names(q); got != want {
			t.Errorf("search %q = %q, want %q", q, got, want)
		}
	}
	// Nobody is an empty list, not null: the page iterates it.
	if env := monUIDecode(t, monUIGet(r, "/panel/api/users/search?q=nobody42", cookie)); string(env.Obj) != "[]" {
		t.Errorf("nobody: %s", env.Obj)
	}
}
