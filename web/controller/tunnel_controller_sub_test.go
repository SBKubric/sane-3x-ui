package controller

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/coinman-dev/3ax-ui/v2/web/session"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// newUsersRouter mounts the AWG, WG and users APIs the way
// APIController.initRouter does, over a fresh database, and returns it with
// a logged-in session cookie.
func newUsersRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(sessions.Sessions("3ax-ui", cookie.NewStore([]byte("users-test-secret"))))
	r.GET("/test-login", func(c *gin.Context) {
		if err := session.SetLoginUser(c, &model.User{Id: 1, Username: "admin"}); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	a := &APIController{}
	api := r.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	NewAwgController(api.Group("/awg"))
	NewWgController(api.Group("/wg"))
	NewSubUserController(api.Group("/users"))
	return r, monUILogin(t, r)
}

// storeInbound writes a disabled inbound with clients straight to the database.
func storeInbound(t *testing.T, id int, protocol model.Protocol, remark string, clients ...model.Client) {
	t.Helper()
	settings := map[string]any{"clients": clients}
	if protocol == model.VLESS {
		settings["decryption"] = "none"
	}
	raw, _ := json.Marshal(settings)
	if protocol == model.AmneziaWG {
		raw = []byte(`{"clients":[]}`)
	}
	ib := &model.Inbound{Id: id, Port: 30000 + id, Protocol: protocol, Tag: "inbound-t" + strconv.Itoa(id), Remark: remark,
		Settings: string(raw), StreamSettings: "{}", Sniffing: "{}"}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatal(err)
	}
}

type tunnelClientJSON struct {
	Id       int    `json:"id"`
	ServerId int    `json:"serverId"`
	UUID     string `json:"uuid"`
	Email    string `json:"email"`
	SubId    string `json:"subId"`
}

// TestTunnelAPISubIdAndUserRules: the AWG/WG client API takes an optional
// subId (docs/spec/tunnel-subscription.md §4) and goes through the user rules.
func TestTunnelAPISubIdAndUserRules(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl", model.Client{ID: "aaaaaaaa-0000-0000-0000-000000000001", Email: "petr-nl", SubID: "s-petr"})
	if err := (&service.SettingService{}).SetMonProbeSubId("probesub"); err != nil {
		t.Fatal(err)
	}

	// A name an xray client has is refused, naming it.
	env := monUIDecode(t, chainPost(r, "/panel/api/awg/client/add", cookie, `{"name":"x","email":"PETR-NL","enable":true}`))
	if env.Success || !strings.Contains(env.Msg, `"PETR-NL"`) || !strings.Contains(env.Msg, "inbound nl") {
		t.Errorf("add with an xray name: %+v", env)
	}
	// The probe subId is monitoring's.
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/add", cookie, `{"name":"x","email":"x","enable":true,"subId":"probesub"}`))
	if env.Success || !strings.Contains(env.Msg, "monitoring") {
		t.Errorf("add with the probe subId: %+v", env)
	}

	// Add with a subId links the client and answers with it.
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/add", cookie, `{"name":"petr-awg","email":"petr-awg","enable":true,"subId":"s-petr"}`))
	if !env.Success {
		t.Fatalf("add petr-awg: %s", env.Msg)
	}
	var added tunnelClientJSON
	if err := json.Unmarshal(env.Obj, &added); err != nil || added.SubId != "s-petr" || added.UUID == "" {
		t.Fatalf("add answer: %+v, %v", added, err)
	}
	// Add without one leaves it unlinked.
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/add", cookie, `{"name":"solo","email":"solo","enable":true}`))
	if !env.Success {
		t.Fatalf("add solo: %s", env.Msg)
	}

	clients := func() map[string]tunnelClientJSON {
		t.Helper()
		env := monUIDecode(t, monUIGet(r, "/panel/api/awg/clients", cookie))
		var list []tunnelClientJSON
		if err := json.Unmarshal(env.Obj, &list); err != nil {
			t.Fatalf("clients: %v (%s)", err, env.Obj)
		}
		out := map[string]tunnelClientJSON{}
		for _, c := range list {
			out[c.Email] = c
		}
		return out
	}
	got := clients()
	if got["petr-awg"].SubId != "s-petr" || got["solo"].SubId != "" {
		t.Errorf("GET clients: %+v", got)
	}
	user, err := (&service.SubUserService{}).Get("s-petr")
	if err != nil || len(user.Clients) != 2 {
		t.Errorf("petr after the AWG add: %+v, %v", user, err)
	}

	// Update without subId keeps the link; with "" drops it; renaming into a
	// taken name is refused.
	// The panel sends the whole client back on update; so do we.
	id := strconv.Itoa(got["petr-awg"].Id)
	server := strconv.Itoa(got["petr-awg"].ServerId)
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/update/"+id, cookie,
		`{"uuid":"`+added.UUID+`","serverId":`+server+`,"name":"petr-awg","email":"petr-awg","enable":false}`))
	if !env.Success || clients()["petr-awg"].SubId != "s-petr" {
		t.Errorf("update without subId: %+v, link %q", env, clients()["petr-awg"].SubId)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/updateByUuid/"+added.UUID, cookie,
		`{"serverId":`+server+`,"name":"petr-awg","email":"solo","enable":false}`))
	if env.Success || !strings.Contains(env.Msg, `"solo"`) {
		t.Errorf("rename into a taken name: %+v", env)
	}
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/updateByUuid/"+added.UUID, cookie,
		`{"serverId":`+server+`,"name":"petr-awg","email":"petr-awg","enable":false,"subId":""}`))
	if !env.Success || clients()["petr-awg"].SubId != "" {
		t.Errorf("update with subId \"\": %+v, link %q", env, clients()["petr-awg"].SubId)
	}
}

// TestTunnelAPIWritesDropTheSubscriptionCache: an edit through the AWG/WG
// client API shows on /tun at once, whether or not it sends subId
// (docs/spec/tunnel-subscription.md §3).
func TestTunnelAPIWritesDropTheSubscriptionCache(t *testing.T) {
	r, cookie := newUsersRouter(t)
	service.InvalidateTunnelSubCache()
	t.Cleanup(service.InvalidateTunnelSubCache)
	subs := &service.TunnelSubscriptionService{}
	enabled := func() []bool {
		t.Helper()
		entries, err := subs.ClientsBySubId("s-ivan")
		if err != nil {
			t.Fatal(err)
		}
		var out []bool
		for _, e := range entries {
			out = append(out, e.Client.Enable)
		}
		return out
	}

	env := monUIDecode(t, chainPost(r, "/panel/api/awg/client/add", cookie, `{"name":"ivan-awg","email":"ivan-awg","enable":true,"subId":"s-ivan"}`))
	if !env.Success {
		t.Fatalf("add: %s", env.Msg)
	}
	var added tunnelClientJSON
	if err := json.Unmarshal(env.Obj, &added); err != nil {
		t.Fatal(err)
	}
	if got := enabled(); len(got) != 1 || !got[0] {
		t.Fatalf("after add: %v", got)
	}

	server := strconv.Itoa(added.ServerId)
	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/update/"+strconv.Itoa(added.Id), cookie,
		`{"uuid":"`+added.UUID+`","serverId":`+server+`,"name":"ivan-awg","email":"ivan-awg","enable":false}`))
	if !env.Success {
		t.Fatalf("update: %s", env.Msg)
	}
	if got := enabled(); len(got) != 1 || got[0] {
		t.Errorf("after update by id: %v, want the client off", got)
	}

	env = monUIDecode(t, chainPost(r, "/panel/api/awg/client/updateByUuid/"+added.UUID, cookie,
		`{"serverId":`+server+`,"name":"ivan-awg","email":"ivan-awg","enable":true}`))
	if !env.Success {
		t.Fatalf("update by uuid: %s", env.Msg)
	}
	if got := enabled(); len(got) != 1 || !got[0] {
		t.Errorf("after update by uuid: %v, want the client on", got)
	}
}
