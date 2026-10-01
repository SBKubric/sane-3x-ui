package sub

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"

	"github.com/gin-gonic/gin"
)

// TestTheSubscriptionPathHasNoCaptcha (#220 amended): the bot's captcha left
// the subscriptions for /third-party/<secret>/ on the panel's port. Under
// the subscription path «captcha» is a subscription id like any other, and
// the sub server has nothing under /third-party/.
func TestTheSubscriptionPathHasNoCaptcha(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewSUBController(engine.Group("/"), "/sub/", "/json/", "/clash/", true, false, false, false, "", "10", "", "", "", "",
		"", "", "", "", false, "", "")

	for _, c := range []struct {
		method, path string
	}{
		{http.MethodGet, "/sub/captcha"},
		{http.MethodGet, "/sub/captcha/altcha.js"},
		{http.MethodGet, "/sub/captcha/challenge"},
		{http.MethodPost, "/sub/captcha/verify"},
		{http.MethodGet, "/third-party/s3cr3t/captcha"},
	} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader("{}")))
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "altcha") {
			t.Errorf("%s %s: %d %.200q", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}
