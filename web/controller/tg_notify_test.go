package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/coinman-dev/3ax-ui/v2/web/session"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

// fakeBotAPI stands in for Telegram: it records the chat of every message
// and answers with refusal when one is set, as Telegram answers a chat the
// bot cannot post to.
type fakeBotAPI struct {
	chats   []string
	refusal string
}

func (f *fakeBotAPI) Call(_ context.Context, _ string, data *ta.RequestData) (*ta.Response, error) {
	var params struct {
		ChatID any `json:"chat_id"`
	}
	_ = json.Unmarshal(data.BodyRaw, &params)
	if s, ok := params.ChatID.(string); ok {
		f.chats = append(f.chats, s)
	} else {
		raw, _ := json.Marshal(params.ChatID)
		f.chats = append(f.chats, string(raw))
	}
	if f.refusal != "" {
		return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 400, Description: f.refusal}}, nil
	}
	return &ta.Response{Ok: true, Result: json.RawMessage(`{"message_id":1,"date":0,"chat":{"id":-1001234567890,"type":"channel"}}`)}, nil
}

// newTgNotifyRouter builds the panel API with the tgbot group the way
// APIController.initRouter hangs it, behind checkAPIAuth, and returns it
// with a logged-in session cookie.
func newTgNotifyRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("3ax-ui", cookie.NewStore([]byte("tg-notify-test-secret"))))
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
	NewTgNotifyController(api.Group("/tgbot"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test-login", nil))
	return r, w.Header().Get("Set-Cookie")
}

// postNotifyTest presses «Send test» with chatId typed in the field.
func postNotifyTest(t *testing.T, r *gin.Engine, cookie, chatId string) (int, monUIEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/panel/api/tgbot/notifyTest",
		strings.NewReader(url.Values{"chatId": {chatId}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env monUIEnvelope
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, env
}

func useFakeBotAPI(t *testing.T, f *fakeBotAPI) {
	t.Helper()
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(f), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.UseTelegramBotForTest(b))
}

// TestTgNotifyTestSend: «Send test» posts to the channel typed in the form
// and reports success; Telegram's refusal, a malformed channel and a stopped
// bot come back as the error; without a session the route does not exist.
func TestTgNotifyTestSend(t *testing.T) {
	r, cookie := newTgNotifyRouter(t)

	if code, _ := postNotifyTest(t, r, "", "@my_channel"); code != http.StatusNotFound {
		t.Errorf("without a session: status %d, want 404", code)
	}

	fake := &fakeBotAPI{}
	useFakeBotAPI(t, fake)
	for _, chat := range []string{"@my_channel", "-1001234567890"} {
		fake.chats = nil
		if _, env := postNotifyTest(t, r, cookie, chat); !env.Success {
			t.Errorf("%s: %+v", chat, env)
		}
		if len(fake.chats) != 1 || fake.chats[0] != chat {
			t.Errorf("%s: posted to %q", chat, fake.chats)
		}
	}

	fake.chats = nil
	for _, bad := range []string{"", "my_channel"} {
		if _, env := postNotifyTest(t, r, cookie, bad); env.Success || !strings.Contains(env.Msg, "notification channel") {
			t.Errorf("%q: %+v", bad, env)
		}
	}
	if len(fake.chats) != 0 {
		t.Errorf("a malformed channel reached Telegram: %q", fake.chats)
	}

	fake.refusal = "Bad Request: chat not found"
	if _, env := postNotifyTest(t, r, cookie, "@my_channel"); env.Success || !strings.Contains(env.Msg, "chat not found") {
		t.Errorf("refused: %+v", env)
	}

	t.Cleanup(service.UseTelegramBotForTest(nil))
	if _, env := postNotifyTest(t, r, cookie, "@my_channel"); env.Success || !strings.Contains(env.Msg, "not running") {
		t.Errorf("bot stopped: %+v", env)
	}
}
