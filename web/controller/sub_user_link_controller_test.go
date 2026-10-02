package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

// photoBotAPI stands in for Telegram: it records the method and the chat of
// every call, multipart ones (a photo) included.
type photoBotAPI struct {
	mu    sync.Mutex
	calls []string
}

func (f *photoBotAPI) Call(_ context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
	method := url[strings.LastIndex(url, "/")+1:]
	chat := ""
	if data.BodyStream != nil {
		_, params, _ := mime.ParseMediaType(data.ContentType)
		r := multipart.NewReader(data.BodyStream, params["boundary"])
		for {
			part, err := r.NextPart()
			if err != nil {
				break
			}
			value, _ := io.ReadAll(part)
			if part.FormName() == "chat_id" {
				chat = string(value)
			}
		}
	} else {
		var p struct {
			ChatID any `json:"chat_id"`
		}
		_ = json.Unmarshal(data.BodyRaw, &p)
		chat = fmt.Sprint(p.ChatID)
	}
	f.mu.Lock()
	f.calls = append(f.calls, method+" "+chat)
	f.mu.Unlock()
	return &ta.Response{Ok: true, Result: json.RawMessage(`{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}`)}, nil
}

func (f *photoBotAPI) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// TestUsersBroadcastLinksAPI (#222): the users page's «Send links» starts
// the broadcast — the link goes to the users with Telegram — and answers
// with its number and audience; with the bot off it is refused.
func TestUsersBroadcastLinksAPI(t *testing.T) {
	r, cookie := newUsersRouter(t)
	storeInbound(t, 1, model.VLESS, "nl")
	t.Cleanup(service.UseTelegramBotForTest(nil))
	if env := monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"anna","tgId":7101,"inboundIds":[1]}`)); !env.Success {
		t.Fatalf("create anna: %+v", env)
	}
	if env := monUIDecode(t, chainPost(r, "/panel/api/users/create", cookie, `{"name":"ivan","inboundIds":[1]}`)); !env.Success {
		t.Fatalf("create ivan: %+v", env)
	}

	env := monUIDecode(t, chainPost(r, "/panel/api/users/broadcastLinks", cookie, ``))
	if env.Success || !strings.Contains(env.Msg, "not running") {
		t.Errorf("with the bot off: %+v", env)
	}
	if w := chainPost(r, "/panel/api/users/broadcastLinks", "", ``); w.Code != http.StatusNotFound {
		t.Errorf("without a session: %d", w.Code)
	}

	fake := &photoBotAPI{}
	b, err := telego.NewBot("123456:"+strings.Repeat("a", 35), telego.WithAPICaller(fake), telego.WithDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.UseTelegramBotForTest(b))
	env = monUIDecode(t, chainPost(r, "/panel/api/users/broadcastLinks", cookie, ``))
	var started service.SubLinkStarted
	if err := json.Unmarshal(env.Obj, &started); err != nil || !env.Success || started.Id == 0 ||
		started.Recipients != 1 || started.NoTelegram != 1 {
		t.Fatalf("started: %+v %+v", env, started)
	}
	// The broadcast runs on its own: wait for the journal to close it.
	deadline := time.Now().Add(10 * time.Second)
	var broadcast model.SubLinkBroadcast
	for time.Now().Before(deadline) {
		database.GetDB().First(&broadcast, started.Id)
		if broadcast.FinishedAt != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if broadcast.Trigger != model.SubLinkTriggerAPI || broadcast.StartedBy != "panel" || broadcast.Sent != 1 || broadcast.NoTelegram != 1 {
		t.Errorf("journal: %+v", broadcast)
	}
	if calls := fake.sent(); len(calls) != 1 || calls[0] != "sendMessage 7101" {
		t.Errorf("Telegram got %q", calls)
	}
}
