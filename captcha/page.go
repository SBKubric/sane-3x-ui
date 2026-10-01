package captcha

import (
	_ "embed"
	"html/template"
	"net/http"
)

// The page (#188 point 11): a Telegram Mini App that the bot's «Пройти
// проверку» button opens at /third-party/<secret>/captcha on the active
// edge's 443 front. It reads the person's initData from the URL fragment
// Telegram opens it with, fetches a challenge made for that account (the
// initData goes with the request), lets the widget solve it and posts both
// to …/captcha/verify. It loads nothing from outside: the widget is served
// beside it, and Telegram's own telegram-web-app.js is not used — the
// fragment and the web_app_* events of the Mini App protocol are all it
// needs, and telegram.org may be out of reach from where the person is.

//go:embed page.html
var pageHTML string

// widgetJS is the ALTCHA widget, altcha 2.3.0 dist/altcha.umd.cjs from npm
// (MIT, altcha.LICENSE.txt), with its worker inlined: one file, no CDN.
//
//go:embed altcha.js
var widgetJS []byte

var pageTmpl = template.Must(template.New("captcha").Parse(pageHTML))

// Segment is the captcha's place under the bot's path.
const Segment = "captcha"

// The parts below the captcha's base, <bot path>captcha/.
const (
	PartWidget    = "altcha.js" // GET: the widget
	PartChallenge = "challenge" // POST {initData}: a new challenge for that account
	PartVerify    = "verify"    // POST {initData, payload}
)

// ServePage writes the page whose widget and calls are under base, the
// captcha's own path with its slash (<bot path>captcha/).
func ServePage(w http.ResponseWriter, base string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = pageTmpl.Execute(w, struct{ Base string }{base})
}

// ServeWidget writes the widget.
func ServeWidget(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(widgetJS)
}
