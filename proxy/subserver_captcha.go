package proxy

import (
	"bytes"
	"io"
	"net/http"

	"github.com/coinman-dev/3ax-ui/v2/captcha"
	"github.com/coinman-dev/3ax-ui/v2/logger"

	"github.com/gin-gonic/gin"
)

// The captcha before a request for a subscription (#188 point 11, #220,
// docs/spec/users.md §12): the bot opens <subPath>captcha on the active edge
// as a Telegram Mini App. The hop serves the page and the widget itself
// (package captcha) and passes the challenge and the verification on to its
// next hop under the same path, as it passes subscriptions, until the panel
// on real answers them. The answers come back as they are — a refusal too,
// which the front's HTTP side logs as a miss.

// captchaBodyLimit bounds a verification's body on its way through.
const captchaBodyLimit = 16 << 10

// handleCaptcha serves one part of the captcha path.
func (s *SubServer) handleCaptcha(c *gin.Context, part string) {
	switch part {
	case captcha.PartPage:
		captcha.ServePage(c.Writer, s.publicSubPath())
		return
	case captcha.PartWidget:
		captcha.ServeWidget(c.Writer)
		return
	}
	method := http.MethodGet
	if part == captcha.PartVerify {
		method = http.MethodPost
	}
	if c.Request.Method != method {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	var body io.Reader
	if method == http.MethodPost {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, captchaBodyLimit))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		body = bytes.NewReader(raw)
	}
	base, subPath, _ := s.nextHop()
	req, err := http.NewRequest(method, base+subPath+captcha.Segment+"/"+part, body)
	if err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
	if ct := c.GetHeader("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		logger.Warning("proxy-front: captcha", part, "via the next hop:", err)
		c.Status(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, captchaBodyLimit))
	if err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Cache-Control", "no-store")
	c.Data(resp.StatusCode, contentType, answer)
}
