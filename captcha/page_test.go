package captcha

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPageNamesItsCallsUnderItsBase: the page loads the widget from beside
// itself, not from a CDN, and sends the challenge (through its own fetch,
// which carries the initData) and the solution to the paths under the base
// it is served at.
func TestPageNamesItsCallsUnderItsBase(t *testing.T) {
	rec := httptest.NewRecorder()
	ServePage(rec, "/third-party/s3cr3t/captcha/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("page: %d %v", rec.Code, rec.Header())
	}
	for _, want := range []string{`src="/third-party/s3cr3t/captcha/altcha.js"`,
		`challengeurl="/third-party/s3cr3t/captcha/challenge"`, `customfetch="captchaFetch"`, "window.captchaFetch",
		`data-base="/third-party/s3cr3t/captcha/"`, "tgWebAppData", "<altcha-widget", "Я не робот",
		`data-testid="captcha-status"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(body, "cdn.") || strings.Contains(body, "telegram.org") {
		t.Error("the page loads something from outside")
	}
}

// TestWidgetIsServedFromTheBinary: the widget comes from the binary as
// JavaScript and defines the altcha-widget element.
func TestWidgetIsServedFromTheBinary(t *testing.T) {
	rec := httptest.NewRecorder()
	ServeWidget(rec)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("widget: %d %v", rec.Code, rec.Header())
	}
	if !strings.Contains(rec.Body.String(), `customElements.define("altcha-widget"`) {
		t.Error("the widget does not define altcha-widget")
	}
}
