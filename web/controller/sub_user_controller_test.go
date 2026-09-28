package controller

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

type userJSON struct {
	SubId     string `json:"subId"`
	Name      string `json:"name"`
	Technical bool   `json:"technical"`
	Enable    bool   `json:"enable"`
	Total     int64  `json:"total"`
	Clients   []struct {
		Kind      string `json:"kind"`
		InboundId int    `json:"inboundId"`
		Name      string `json:"name"`
		Enable    bool   `json:"enable"`
		SubId     string `json:"subId"`
	} `json:"clients"`
}

func decodeUser(t *testing.T, env monUIEnvelope) userJSON {
	t.Helper()
	if !env.Success {
		t.Fatalf("refused: %s", env.Msg)
	}
	var u userJSON
	if err := json.Unmarshal(env.Obj, &u); err != nil {
		t.Fatalf("user: %v (%s)", err, env.Obj)
	}
	return u
}

// TestUsersAPI walks the users API the panel page (#170) uses.
func TestUsersAPI(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "other-nl", SubID: "s-other", Enable: true})
	storeInbound(t, 2, model.VLESS, "de",
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000002", Email: "legacy-de", Enable: true},
		model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000003", Email: "keeper-de", SubID: "s-keeper", Enable: true})
	storeInbound(t, 5, model.AmneziaWG, "awg")

	// Without a session the API does not exist.
	if w := monUIGet(r, "/panel/api/users/list", ""); w.Code != http.StatusNotFound {
		t.Errorf("list without a session: %d", w.Code)
	}

	// The inbounds a user can have.
	env := monUIDecode(t, monUIGet(r, "/panel/api/users/inbounds", cookie))
	var inbounds []service.SubUserInbound
	if err := json.Unmarshal(env.Obj, &inbounds); err != nil || len(inbounds) != 3 {
		t.Errorf("inbounds: %s, %v", env.Obj, err)
	}

	// Create with two protocols.
	u := decodeUser(t, monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie,
		`{"name":"ivan","inboundIds":[1,5],"totalGB":1073741824}`)))
	if u.Name != "ivan" || len(u.Clients) != 2 || u.Total != 2<<30 {
		t.Fatalf("created: %+v", u)
	}
	sub := "/" + url.PathEscape(u.SubId)

	// A name clash is refused with a message naming the user.
	env = monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"Ivan"}`))
	if env.Success || env.Msg != "user ivan already exists" {
		t.Errorf("duplicate create: %+v", env)
	}
	// A linkable AWG client comes back as a conflict code.
	if _, err := (&service.AwgService{}).GetServer(); err != nil {
		t.Fatal(err)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/add", cookie, `{"name":"olga-awg","email":"olga-awg","enable":true}`))
	if !env.Success {
		t.Fatal(env.Msg)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"olga","inboundIds":[5]}`))
	var conflict service.SubUserConflict
	if env.Success || json.Unmarshal(env.Obj, &conflict) != nil || conflict.Code != service.SubUserConflictAwgLinkable || conflict.Client != "olga-awg" {
		t.Errorf("linkable create: %+v", env)
	}

	// get, find, list.
	if got := decodeUser(t, monUIDecode(t, monUIGet(r, "/panel/api/users/get"+sub, cookie))); got.Name != "ivan" {
		t.Errorf("get: %+v", got)
	}
	if got := decodeUser(t, monUIDecode(t, monUIGet(r, "/panel/api/users/find?q=ivan-nl", cookie))); got.SubId != u.SubId {
		t.Errorf("find by client name: %+v", got)
	}
	if got := decodeUser(t, monUIDecode(t, monUIGet(r, "/panel/api/users/get/"+url.PathEscape(model.SubUserRobotKey), cookie))); !got.Technical || got.Name != "robot" {
		t.Errorf("get robot: %+v", got)
	}
	env = monUIDecode(t, monUIGet(r, "/panel/api/users/list", cookie))
	var list []userJSON
	if err := json.Unmarshal(env.Obj, &list); err != nil || len(list) != 5 || list[0].Name != "monitoring" && list[0].Name != "robot" {
		t.Errorf("list: %s, %v", env.Obj, err)
	}

	// addProtocol, enable, removeProtocol, assign.
	u = decodeUser(t, monUIDecode(t, chainPost(r, "/panel/api/users/addProtocol"+sub, cookie, `{"inboundId":2}`)))
	if len(u.Clients) != 3 {
		t.Errorf("after addProtocol: %+v", u)
	}
	u = decodeUser(t, monUIDecode(t, chainPost(r, "/panel/api/users/enable"+sub, cookie, `{"enable":false}`)))
	if u.Enable {
		t.Errorf("after disable: %+v", u)
	}
	u = decodeUser(t, monUIDecode(t, chainPost(r, "/panel/api/users/removeProtocol"+sub, cookie, `{"inboundId":`+strconv.Itoa(5)+`}`)))
	if len(u.Clients) != 2 {
		t.Errorf("after removeProtocol: %+v", u)
	}
	u = decodeUser(t, monUIDecode(t, chainPost(r, "/panel/api/users/assign"+sub, cookie, `{"client":"legacy-de"}`)))
	if len(u.Clients) != 3 {
		t.Errorf("after assign: %+v", u)
	}

	// Technical users cannot be deleted; ivan can.
	env = monUIDecode(t, chainPost(r, "/panel/api/users/del/"+url.PathEscape(model.SubUserMonitoringKey), cookie, ``))
	if env.Success {
		t.Error("deleting monitoring succeeded")
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/users/del"+sub, cookie, ``))
	if !env.Success {
		t.Fatalf("del: %s", env.Msg)
	}
	env = monUIDecode(t, monUIGet(r, "/panel/api/users/get"+sub, cookie))
	if env.Success {
		t.Error("the deleted user is still there")
	}
	// Bad bodies are refused, not guessed at.
	env = monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"x","bogus":1}`))
	if env.Success {
		t.Error("create with an unknown field succeeded")
	}
}
