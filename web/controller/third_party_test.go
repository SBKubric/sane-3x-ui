package controller

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

// The bot's own path on the panel (#220, docs/spec/users.md §12):
// /third-party/<secret>/captcha and its widget, challenge and verification,
// beside the panel on its port and outside its base path. A wrong secret,
// or the bot off, is a bare 404; the challenge and the verification tell a
// forged initData (403) from a wrong or replayed solution (400).

const thirdPartyTestToken = "123456:" + "c123456789c123456789c123456789c1234"

// thirdPartyServer is the panel's handler with the bot's path in front of a
// panel that answers every other request 418, over a database with the bot
// on, its token, and the path /third-party/s3cr3t/.
func thirdPartyServer(t *testing.T) http.Handler {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
	settings := &service.SettingService{}
	if err := settings.SetTgBotToken(thirdPartyTestToken); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"tgBotEnable": "true", "tgThirdPartySecret": "s3cr3t"} {
		if err := database.GetDB().Exec("INSERT INTO settings (key, value) VALUES (?, ?)", key, value).Error; err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	return WithThirdParty(panel)
}

// signInitData is Telegram's initData for user id, signed at at with the
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

func thirdPartyDo(h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if s, ok := body.(string); ok {
		raw = []byte(s)
	} else if body != nil {
		raw, _ = json.Marshal(body)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	h.ServeHTTP(rec, req)
	return rec
}

// solveFrom fetches a challenge for initData and solves it as the widget does.
func solveFrom(t *testing.T, h http.Handler, initData string) string {
	t.Helper()
	rec := thirdPartyDo(h, http.MethodPost, "/third-party/s3cr3t/captcha/challenge", map[string]string{"initData": initData})
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

// TestThirdPartyServesTheCaptchaPage: the page and the widget answer at the
// bot's path, the page naming its calls there; everything else goes to the
// panel.
func TestThirdPartyServesTheCaptchaPage(t *testing.T) {
	h := thirdPartyServer(t)
	page := thirdPartyDo(h, http.MethodGet, "/third-party/s3cr3t/captcha", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `challengeurl="/third-party/s3cr3t/captcha/challenge"`) ||
		!strings.Contains(page.Body.String(), `src="/third-party/s3cr3t/captcha/altcha.js"`) {
		t.Fatalf("page: %d %.300q", page.Code, page.Body.String())
	}
	widget := thirdPartyDo(h, http.MethodGet, "/third-party/s3cr3t/captcha/altcha.js", nil)
	if widget.Code != http.StatusOK || !strings.Contains(widget.Body.String(), "altcha-widget") {
		t.Errorf("widget: %d", widget.Code)
	}
	for _, path := range []string{"/", "/panel/", "/sub/captcha", "/third-partyx/s3cr3t/captcha"} {
		if rec := thirdPartyDo(h, http.MethodGet, path, nil); rec.Code != http.StatusTeapot {
			t.Errorf("%s did not reach the panel: %d", path, rec.Code)
		}
	}
}

// TestThirdPartyRefusesAWrongSecret: another secret, no secret, an unknown
// part, and every path while the bot is off are a bare 404.
func TestThirdPartyRefusesAWrongSecret(t *testing.T) {
	h := thirdPartyServer(t)
	for _, path := range []string{"/third-party/other/captcha", "/third-party/S3CR3T/captcha", "/third-party//captcha",
		"/third-party/s3cr3t/", "/third-party/s3cr3t/captcha/other", "/third-party/s3cr3t/captcha/", "/third-party/"} {
		if rec := thirdPartyDo(h, http.MethodGet, path, nil); rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	if rec := thirdPartyDo(h, http.MethodPost, "/third-party/other/captcha/verify", map[string]string{}); rec.Code != http.StatusNotFound {
		t.Errorf("verify under another secret: %d", rec.Code)
	}
	if err := database.GetDB().Exec("UPDATE settings SET value = 'false' WHERE key = 'tgBotEnable'").Error; err != nil {
		t.Fatal(err)
	}
	if rec := thirdPartyDo(h, http.MethodGet, "/third-party/s3cr3t/captcha", nil); rec.Code != http.StatusNotFound {
		t.Errorf("the bot off: %d", rec.Code)
	}
}

// TestThirdPartyCaptchaStatuses: a challenge needs a valid initData (403
// otherwise); a correct solution with it is 200, once; the same again 400; a
// wrong one 400; a forged or stale initData 403; no JSON 400.
func TestThirdPartyCaptchaStatuses(t *testing.T) {
	h := thirdPartyServer(t)
	initData := signInitData(5550001, time.Now(), thirdPartyTestToken)
	forged := signInitData(5550001, time.Now(), "654321:"+strings.Repeat("x", 35))
	verify := func(initData, payload string) int {
		return thirdPartyDo(h, http.MethodPost, "/third-party/s3cr3t/captcha/verify",
			map[string]string{"initData": initData, "payload": payload}).Code
	}

	if rec := thirdPartyDo(h, http.MethodPost, "/third-party/s3cr3t/captcha/challenge", map[string]string{"initData": forged}); rec.Code != http.StatusForbidden {
		t.Errorf("a challenge for a forged initData: %d", rec.Code)
	}
	payload := solveFrom(t, h, initData)
	if code := verify(forged, payload); code != http.StatusForbidden {
		t.Errorf("another bot's initData: %d", code)
	}
	if code := verify(signInitData(5550001, time.Now().Add(-time.Hour), thirdPartyTestToken), payload); code != http.StatusForbidden {
		t.Errorf("a stale initData: %d", code)
	}
	if code := verify(signInitData(5550002, time.Now(), thirdPartyTestToken), payload); code != http.StatusBadRequest {
		t.Errorf("another account's solution: %d", code)
	}
	if code := verify(initData, payload); code != http.StatusOK {
		t.Fatalf("a correct solution: %d", code)
	}
	if passed, err := (&service.TgCaptchaService{}).Passed(5550001); err != nil || !passed {
		t.Errorf("the pass is not remembered: %v, %v", passed, err)
	}
	if code := verify(initData, payload); code != http.StatusBadRequest {
		t.Errorf("the same solution again: %d", code)
	}
	if code := verify(initData, base64.StdEncoding.EncodeToString([]byte(`{"number":1}`))); code != http.StatusBadRequest {
		t.Errorf("a wrong solution: %d", code)
	}
	if rec := thirdPartyDo(h, http.MethodPost, "/third-party/s3cr3t/captcha/verify", "not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("no JSON: %d", rec.Code)
	}
	if rec := thirdPartyDo(h, http.MethodGet, "/third-party/s3cr3t/captcha/verify", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET verify: %d", rec.Code)
	}
}
