package sub

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// The captcha on the panel's sub server (#220): the page and its widget
// under the subscription path, beside the subscriptions themselves; the
// challenge; and the verification, which tells a forged initData (403) from
// a wrong or replayed solution (400).

const captchaTestToken = "123456:" + "c123456789c123456789c123456789c1234"

// newCaptchaRouter is the sub server's router with the subscriptions under
// /sub/ and the captcha beside them, over a database with the bot's token.
func newCaptchaRouter(t *testing.T) *gin.Engine {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
	if err := (&service.SettingService{}).SetTgBotToken(captchaTestToken); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	g := engine.Group("/")
	NewSUBController(g, "/sub/", "/json/", "/clash/", true, false, false, false, "", "10", "", "", "", "", "", "", "", "",
		false, "", "")
	NewCaptchaController(g, "/sub/")
	return engine
}

// signInitData is Telegram's initData for user id, signed now with the
// bot's token as core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app says.
func signInitData(id int64, at time.Time, token string) string {
	fields := map[string]string{"auth_date": strconv.FormatInt(at.Unix(), 10), "query_id": "AAQ",
		"user": fmt.Sprintf(`{"id":%d,"first_name":"Petr"}`, id)}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+fields[k])
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	values := url.Values{}
	for k, v := range fields {
		values.Set(k, v)
	}
	values.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return values.Encode()
}

// solveCaptcha fetches a challenge from the router and solves it as the
// widget does.
func solveCaptcha(t *testing.T, engine *gin.Engine) string {
	t.Helper()
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/captcha/challenge", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("challenge: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	var c captcha.Challenge
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	for n := int64(0); n <= c.MaxNumber; n++ {
		sum := sha256.Sum256([]byte(c.Salt + strconv.FormatInt(n, 10)))
		if hex.EncodeToString(sum[:]) == c.Challenge {
			raw, _ := json.Marshal(map[string]any{"algorithm": c.Algorithm, "challenge": c.Challenge, "number": n,
				"salt": c.Salt, "signature": c.Signature})
			return base64.StdEncoding.EncodeToString(raw)
		}
	}
	t.Fatalf("no solution: %+v", c)
	return ""
}

func postVerify(engine *gin.Engine, initData, payload string) int {
	body, _ := json.Marshal(map[string]string{"initData": initData, "payload": payload})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sub/captcha/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)
	return rec.Code
}

// TestCaptchaPageBesideTheSubscriptions: the page and the widget answer
// under the subscription path, and a subscription id still reaches the
// subscriptions.
func TestCaptchaPageBesideTheSubscriptions(t *testing.T) {
	engine := newCaptchaRouter(t)
	for path, want := range map[string]string{"/sub/captcha": "<altcha-widget", "/sub/captcha/altcha.js": "altcha-widget"} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/captcha", nil))
	if !strings.Contains(rec.Body.String(), `challengeurl="/sub/captcha/challenge"`) {
		t.Error("the page does not name its challenge under /sub/captcha/")
	}
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/unknown-sub-id", nil))
	if strings.Contains(rec.Body.String(), "altcha") {
		t.Errorf("a subscription id reached the captcha: %d", rec.Code)
	}
}

// TestCaptchaVerifyStatuses: a correct solution with a valid initData is
// 200, once; the same again 400; a wrong one 400; a forged or stale
// initData 403; no body 400.
func TestCaptchaVerifyStatuses(t *testing.T) {
	engine := newCaptchaRouter(t)
	initData := signInitData(5550001, time.Now(), captchaTestToken)

	payload := solveCaptcha(t, engine)
	if code := postVerify(engine, signInitData(5550001, time.Now(), "654321:"+strings.Repeat("x", 35)), payload); code != http.StatusForbidden {
		t.Errorf("another bot's initData: %d", code)
	}
	if code := postVerify(engine, signInitData(5550001, time.Now().Add(-time.Hour), captchaTestToken), payload); code != http.StatusForbidden {
		t.Errorf("a stale initData: %d", code)
	}
	if code := postVerify(engine, initData, payload); code != http.StatusOK {
		t.Fatalf("a correct solution: %d", code)
	}
	if code := postVerify(engine, initData, payload); code != http.StatusBadRequest {
		t.Errorf("the same solution again: %d", code)
	}
	if code := postVerify(engine, initData, base64.StdEncoding.EncodeToString([]byte(`{"number":1}`))); code != http.StatusBadRequest {
		t.Errorf("a wrong solution: %d", code)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sub/captcha/verify", strings.NewReader("not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no JSON: %d", rec.Code)
	}
}
