package controller

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// The bot's own path on the panel (#188 point 11 amended, #220,
// docs/spec/users.md §12): /third-party/<secret>/, served beside the panel
// on its port and outside its base path, sessions and domain check — the
// front's HTTP side passes it here from the 443 of real, and the hops pass
// it along the chain from the active edge's. It is the bot's, not the
// subscriptions': no subPath, no sub server, no subId.
//
// Below it is the captcha, a Telegram Mini App:
//   - GET  …/captcha            the page;
//   - GET  …/captcha/altcha.js  the widget;
//   - POST …/captcha/challenge  {initData}: a challenge for that account;
//   - POST …/captcha/verify     {initData, payload}.
//
// A wrong secret, and every path while the bot is off, is a bare 404.

// thirdPartyBodyLimit bounds a post's body: an initData and a payload are
// well under a kilobyte each.
const thirdPartyBodyLimit = 16 << 10

// ThirdPartyController serves the bot's path.
type ThirdPartyController struct {
	settings service.SettingService
	captcha  service.TgCaptchaService
}

// NewThirdPartyHandler is the bot's path as a handler of its own.
func NewThirdPartyHandler() http.Handler {
	a := &ThirdPartyController{}
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.RedirectTrailingSlash = false
	g := engine.Group(service.ThirdPartyRoot+":secret", a.checkSecret)
	base := "/" + captcha.Segment
	g.GET(base, a.page)
	g.GET(base+"/"+captcha.PartWidget, func(c *gin.Context) { captcha.ServeWidget(c.Writer) })
	g.POST(base+"/"+captcha.PartChallenge, a.challenge)
	g.POST(base+"/"+captcha.PartVerify, a.verify)
	engine.NoRoute(func(c *gin.Context) { c.AbortWithStatus(http.StatusNotFound) })
	return engine
}

// WithThirdParty puts the bot's path in front of the panel's handler next:
// a request under /third-party/ goes to the bot, everything else to next.
func WithThirdParty(next http.Handler) http.Handler {
	bot := NewThirdPartyHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, service.ThirdPartyRoot) {
			bot.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkSecret lets through the bot's own secret while the bot is on; a bare
// 404 otherwise, the answer of a path that is not there.
func (a *ThirdPartyController) checkSecret(c *gin.Context) {
	if on, err := a.settings.GetTgbotEnabled(); err != nil || !on {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	secret, err := a.settings.GetTgThirdPartySecret()
	if err != nil {
		logger.Warning("the bot's path:", err)
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.Param("secret")), []byte(secret)) != 1 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Next()
}

func (a *ThirdPartyController) page(c *gin.Context) {
	captcha.ServePage(c.Writer, service.ThirdPartyRoot+c.Param("secret")+"/"+captcha.Segment+"/")
}

// readThirdPartyPost reads the post's JSON into body; false (and 400) for one
// that is not.
func readThirdPartyPost(c *gin.Context, body any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, thirdPartyBodyLimit)
	if err := c.ShouldBindJSON(body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return false
	}
	return true
}

// challenge answers the widget: a challenge for the account initData names,
// 403 for an initData that is not our bot's or not fresh.
func (a *ThirdPartyController) challenge(c *gin.Context) {
	var body struct {
		InitData string `json:"initData"`
	}
	if !readThirdPartyPost(c, &body) {
		return
	}
	c.Header("Cache-Control", "no-store")
	ch, err := a.captcha.Challenge(body.InitData)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, ch)
	case errors.Is(err, service.ErrCaptchaInitData):
		c.JSON(http.StatusForbidden, gin.H{"ok": false})
	default:
		logger.Warning("captcha challenge:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
	}
}

// verify answers the page: 200 when the check is passed, 403 for an initData
// that is not our bot's or not fresh, 400 for a wrong or used solution.
func (a *ThirdPartyController) verify(c *gin.Context) {
	var body struct {
		InitData string `json:"initData"`
		Payload  string `json:"payload"`
	}
	if !readThirdPartyPost(c, &body) {
		return
	}
	c.Header("Cache-Control", "no-store")
	err := a.captcha.Verify(body.InitData, body.Payload)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"ok": true})
	case errors.Is(err, service.ErrCaptchaInitData):
		c.JSON(http.StatusForbidden, gin.H{"ok": false})
	case errors.Is(err, captcha.ErrWrong), errors.Is(err, captcha.ErrReplayed):
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
	default:
		logger.Warning("captcha verify:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
	}
}
