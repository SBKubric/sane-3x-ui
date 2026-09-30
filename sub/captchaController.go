package sub

import (
	"errors"
	"net/http"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// CaptchaController is the captcha before a request for a subscription
// (#188 points 9–11, #220, docs/spec/users.md §12) on the panel's sub server,
// under the subscription path: <subPath>captcha is the page, and below it
// the widget, the challenge and the verification. Behind a chain the active
// edge serves the page itself and passes the challenge and the verification
// on, hop by hop, to these (proxy/subserver_captcha.go). A controller of its
// own beside SUBController (ADR 0002).
type CaptchaController struct {
	svc service.SubRequestCaptchaService
}

// captchaVerifyLimit bounds the verification's body: an initData and a
// payload are well under a kilobyte each.
const captchaVerifyLimit = 16 << 10

// NewCaptchaController registers the captcha under subPath.
func NewCaptchaController(g *gin.RouterGroup, subPath string) *CaptchaController {
	a := &CaptchaController{}
	base := strings.TrimSuffix(subPath, "/") + "/" + captcha.Segment
	g.GET(base, func(c *gin.Context) { captcha.ServePage(c.Writer, subPath) })
	g.GET(base+"/"+captcha.PartWidget, func(c *gin.Context) { captcha.ServeWidget(c.Writer) })
	g.GET(base+"/"+captcha.PartChallenge, a.challenge)
	g.POST(base+"/"+captcha.PartVerify, a.verify)
	return a
}

func (a *CaptchaController) challenge(c *gin.Context) {
	ch, err := a.svc.Challenge()
	if err != nil {
		logger.Warning("captcha challenge:", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, ch)
}

// verify answers the page: 200 when the check is passed, 403 for an initData
// that is not our bot's or not fresh, 400 for a wrong or used solution.
func (a *CaptchaController) verify(c *gin.Context) {
	var body struct {
		InitData string `json:"initData"`
		Payload  string `json:"payload"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, captchaVerifyLimit)
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return
	}
	err := a.svc.Verify(body.InitData, body.Payload)
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
