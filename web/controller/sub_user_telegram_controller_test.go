package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// TestUsersTelegramAPI (#186 point 8): a user's Telegram through the users
// API — the conflict flag in the view, the tgIds of its clients with who
// else has them, «Назначить» refused for another user's id, and «Отвязать».
func TestUsersTelegramAPI(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "ivan-nl", SubID: "s-ivan", TgID: 11, Enable: true},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "ivan-de", SubID: "s-ivan", TgID: 12, Enable: true},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "petr-nl", SubID: "s-petr", TgID: 12, Enable: true},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000004", Email: "anna-nl", SubID: "s-anna", TgID: 13, Enable: true})
	if err := (&service.SubUserService{}).Sync(); err != nil {
		t.Fatal(err)
	}

	// Without a session the new routes do not exist either.
	if w := monUIGet(r, "/panel/api/users/telegram/s-ivan", ""); w.Code != http.StatusNotFound {
		t.Errorf("telegram without a session: %d", w.Code)
	}
	if w := chainPost(r, "/panel/api/users/unlinkTelegram/s-ivan", "", ""); w.Code != http.StatusNotFound {
		t.Errorf("unlink without a session: %d", w.Code)
	}

	var ivan struct {
		TgId       int64 `json:"tgId"`
		TgConflict bool  `json:"tgConflict"`
	}
	env := monUIDecode(t, monUIGet(r, "/panel/api/users/get/s-ivan", cookie))
	if err := json.Unmarshal(env.Obj, &ivan); err != nil || ivan.TgId != 0 || !ivan.TgConflict {
		t.Errorf("ivan: %s, %v", env.Obj, err)
	}

	env = monUIDecode(t, monUIGet(r, "/panel/api/users/telegram/s-ivan", cookie))
	var tg service.SubUserTelegram
	if err := json.Unmarshal(env.Obj, &tg); err != nil || !env.Success {
		t.Fatalf("telegram: %+v, %v", env, err)
	}
	if !tg.Conflict || len(tg.Candidates) != 2 || tg.Candidates[0].TgId != 11 || tg.Candidates[1].TgId != 12 ||
		len(tg.Candidates[1].AlsoOn) != 1 || tg.Candidates[1].AlsoOn[0] != "petr-nl" {
		t.Errorf("telegram: %s", env.Obj)
	}

	// anna owns 13: ivan cannot have it.
	env = monUIDecode(t, chainPost(r, "/panel/api/users/setTelegram/s-ivan", cookie, `{"tgId":13}`))
	if env.Success || env.Msg != "Telegram id 13 belongs to user anna-nl" {
		t.Errorf("assign anna's id: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/setTelegram/s-ivan", cookie, `{"tgId":12,"x":1}`))
	if env.Success {
		t.Error("an unknown field was accepted")
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/setTelegram/s-ivan", cookie, `{"tgId":12}`))
	if err := json.Unmarshal(env.Obj, &ivan); err != nil || !env.Success || ivan.TgId != 12 || ivan.TgConflict {
		t.Errorf("assign 12: %+v", env)
	}

	env = monUIDecode(t, chainPost(r, "/panel/api/users/unlinkTelegram/s-ivan", cookie, ""))
	if err := json.Unmarshal(env.Obj, &ivan); err != nil || !env.Success || ivan.TgId != 0 || ivan.TgConflict {
		t.Errorf("unlink: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/unlinkTelegram/@robot", cookie, ""))
	if env.Success {
		t.Error("robot was unlinked")
	}
}
