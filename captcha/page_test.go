package captcha

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRouteTellsTheCaptchaPathsApart: under the subscription path the page
// answers at …/captcha (with or without the slash), and its widget,
// challenge and verification below it; anything else — a subscription id,
// a deeper path, another prefix — is not the captcha's.
func TestRouteTellsTheCaptchaPathsApart(t *testing.T) {
	for _, c := range []struct {
		path, subPath, want string
		ok                  bool
	}{
		{"/sub/captcha", "/sub/", PartPage, true},
		{"/sub/captcha/", "/sub/", PartPage, true},
		{"/sub/captcha/altcha.js", "/sub/", PartWidget, true},
		{"/sub/captcha/challenge", "/sub/", PartChallenge, true},
		{"/sub/captcha/verify", "/sub/", PartVerify, true},
		{"/s3cr3t/captcha/verify", "/s3cr3t", PartVerify, true}, // a path without its slash
		{"/captcha", "/", PartPage, true},
		{"/sub/captchas", "/sub/", "", false},
		{"/sub/abc", "/sub/", "", false},
		{"/sub/captcha/other", "/sub/", "", false},
		{"/sub/captcha/verify/x", "/sub/", "", false},
		{"/json/captcha", "/sub/", "", false},
		{"/sub/captcha", "", "", false},
	} {
		got, ok := Route(c.path, c.subPath)
		if got != c.want || ok != c.ok {
			t.Errorf("Route(%q, %q) = %q %v, want %q %v", c.path, c.subPath, got, ok, c.want, c.ok)
		}
	}
}

// TestPageNamesItsCallsUnderItsBase: the page loads the widget from beside
// itself, not from a CDN, and sends the challenge and the solution to the
// paths under the base it is served at.
func TestPageNamesItsCallsUnderItsBase(t *testing.T) {
	rec := httptest.NewRecorder()
	ServePage(rec, "/s3cr3t/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("page: %d %v", rec.Code, rec.Header())
	}
	for _, want := range []string{`src="/s3cr3t/captcha/altcha.js"`, `challengeurl="/s3cr3t/captcha/challenge"`,
		`data-base="/s3cr3t/captcha/"`, "tgWebAppData", "<altcha-widget", "Я не робот", `data-testid="captcha-status"`} {
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
