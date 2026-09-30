package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

// meBotAPI stands in for Telegram as far as getMe: the bot is @invite_bot.
type meBotAPI struct{}

func (meBotAPI) Call(context.Context, string, *ta.RequestData) (*ta.Response, error) {
	return &ta.Response{Ok: true, Result: json.RawMessage(`{"id":4242,"is_bot":true,"first_name":"Bot","username":"invite_bot"}`)}, nil
}

// TestUsersTelegramBindingAPI (#219): the users API binds a Telegram by its
// @nick on create and on setTelegram; a nick the bot has not seen and an id
// another user has come back as conflicts the page acts on; moveTelegram
// takes the id over.
func TestUsersTelegramBindingAPI(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl")
	for id, nick := range map[int64]string{7101: "anna_tg", 7102: "ivan_tg"} {
		// Accounts the bot has seen.
		if err := database.GetDB().Create(&model.TgAccount{TgId: id, Username: nick}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var user struct {
		SubId string `json:"subId"`
		TgId  int64  `json:"tgId"`
	}
	env := monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"anna","tgNick":"@anna_tg","inboundIds":[1]}`))
	if err := json.Unmarshal(env.Obj, &user); err != nil || !env.Success || user.TgId != 7101 {
		t.Fatalf("create by nick: %+v", env)
	}
	anna := user.SubId
	env = monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"ivan","inboundIds":[1]}`))
	if err := json.Unmarshal(env.Obj, &user); err != nil || !env.Success {
		t.Fatalf("create: %+v", env)
	}
	ivan := user.SubId

	var conflict struct {
		Code       string `json:"code"`
		TgId       int64  `json:"tgId"`
		Owner      string `json:"owner"`
		OwnerSubId string `json:"ownerSubId"`
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/setTelegram/"+ivan, cookie, `{"tgNick":"@petr_tg"}`))
	if err := json.Unmarshal(env.Obj, &conflict); err != nil || env.Success || conflict.Code != "tg_nick_unknown" {
		t.Errorf("unknown nick: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/setTelegram/"+ivan, cookie, `{"tgNick":"anna_tg"}`))
	if err := json.Unmarshal(env.Obj, &conflict); err != nil || env.Success || conflict.Code != "tg_owned" ||
		conflict.Owner != "anna" || conflict.OwnerSubId != anna || conflict.TgId != 7101 {
		t.Errorf("anna's nick: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/moveTelegram/"+ivan, cookie, `{"tgId":7101}`))
	if err := json.Unmarshal(env.Obj, &user); err != nil || !env.Success || user.TgId != 7101 {
		t.Errorf("move: %+v", env)
	}
	env = monUIDecode(t, monUIGet(r, "/panel/api/users/get/"+anna, cookie))
	if err := json.Unmarshal(env.Obj, &user); err != nil || user.TgId != 0 {
		t.Errorf("anna after the move: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/setTelegram/"+anna, cookie, `{"tgNick":"@ivan_tg"}`))
	if err := json.Unmarshal(env.Obj, &user); err != nil || !env.Success || user.TgId != 7102 {
		t.Errorf("anna by nick: %+v", env)
	}
}

// TestUsersTelegramInviteAPI (#219): the user's invite link through the
// users API — none until asked for, the same one while it is open, a new
// one on reissue; the link needs the bot's @username.
func TestUsersTelegramInviteAPI(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl")
	env := monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"ivan","inboundIds":[1]}`))
	var user struct {
		SubId string `json:"subId"`
	}
	if err := json.Unmarshal(env.Obj, &user); err != nil || !env.Success {
		t.Fatalf("create: %+v", env)
	}
	path := "/panel/api/users/telegramInvite/" + user.SubId

	if w := monUIGet(r, path, ""); w.Code != http.StatusNotFound {
		t.Errorf("without a session: %d", w.Code)
	}
	if env := monUIDecode(t, monUIGet(r, path, cookie)); !env.Success || string(env.Obj) != "null" {
		t.Errorf("before any: %+v", env)
	}

	var invite struct {
		Token     string `json:"token"`
		Link      string `json:"link"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	// No bot running: the invite is made, its link waits for the bot.
	env = monUIDecode(t, chainPost(r, path, cookie, ""))
	if err := json.Unmarshal(env.Obj, &invite); err != nil || !env.Success || invite.Token == "" || invite.Link != "" {
		t.Fatalf("invite without a bot: %+v", env)
	}
	first := invite.Token

	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(meBotAPI{}), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.UseTelegramBotForTest(b))
	env = monUIDecode(t, monUIGet(r, path, cookie))
	if err := json.Unmarshal(env.Obj, &invite); err != nil || invite.Token != first ||
		invite.Link != "https://t.me/invite_bot?start="+first || invite.ExpiresAt == 0 {
		t.Errorf("the open invite: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/reissueTelegramInvite/"+user.SubId, cookie, ""))
	if err := json.Unmarshal(env.Obj, &invite); err != nil || invite.Token == first ||
		invite.Link != "https://t.me/invite_bot?start="+invite.Token {
		t.Errorf("reissue: %+v", env)
	}
	if env := monUIDecode(t, chainPost(r, "/panel/api/users/telegramInvite/@robot", cookie, "")); env.Success {
		t.Error("robot got an invite")
	}
}
