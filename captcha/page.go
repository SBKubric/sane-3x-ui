package captcha

import (
	_ "embed"
	"html/template"
	"net/http"
	"strings"
)

// The page (#188 point 11): a Telegram Mini App that the bot's «Пройти
// проверку» button opens at <subPath>captcha on the active edge. It reads the
// person's initData from the URL fragment Telegram opens it with, lets the
// widget solve a challenge and posts both to <subPath>captcha/verify. It
// loads nothing from outside: the widget is served beside it, and Telegram's
// own telegram-web-app.js is not used — the fragment and the web_app_* events
// of the Mini App protocol are all it needs, and telegram.org may be out of
// reach from where the person is.

//go:embed page.html
var pageHTML string

// widgetJS is the ALTCHA widget, altcha 2.3.0 dist/altcha.umd.cjs from npm
// (MIT, altcha.LICENSE.txt), with its worker inlined: one file, no CDN.
//
//go:embed altcha.js
var widgetJS []byte

var pageTmpl = template.Must(template.New("captcha").Parse(pageHTML))

// Segment is the captcha's place under the subscription path.
const Segment = "captcha"

// The parts of the captcha under <subPath>captcha.
const (
	PartPage      = "page"      // the page itself
	PartWidget    = "altcha.js" // the widget
	PartChallenge = "challenge" // GET: a new challenge
	PartVerify    = "verify"    // POST: {initData, payload}
)

// Route tells which part of the captcha path is, under subPath; false for
// a path that is not the captcha's.
func Route(path, subPath string) (string, bool) {
	if subPath == "" {
		return "", false
	}
	if !strings.HasSuffix(subPath, "/") {
		subPath += "/"
	}
	rest, ok := strings.CutPrefix(path, subPath+Segment)
	if !ok {
		return "", false
	}
	switch rest {
	case "", "/":
		return PartPage, true
	case "/" + PartWidget, "/" + PartChallenge, "/" + PartVerify:
		return rest[1:], true
	}
	return "", false
}

// base is the captcha's own path under subPath, with the slash: the page
// names its widget and calls under it.
func base(subPath string) string {
	if !strings.HasSuffix(subPath, "/") {
		subPath += "/"
	}
	return subPath + Segment + "/"
}

// ServePage writes the page served under subPath; its widget and calls are
// under <subPath>captcha/.
func ServePage(w http.ResponseWriter, subPath string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = pageTmpl.Execute(w, struct{ Base string }{base(subPath)})
}

// ServeWidget writes the widget.
func ServeWidget(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(widgetJS)
}
