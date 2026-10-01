package proxy

import (
	"bytes"
	"io"
	"net/http"

	"github.com/coinman-dev/3ax-ui/v2/logger"

	"github.com/gin-gonic/gin"
)

// The bot's own path on a hop (#220, docs/spec/users.md §12): the panel's
// Telegram bot opens its Mini App — the captcha — at /third-party/<secret>/
// captcha on the active edge's front. The hop knows the path from its
// document (nextHop.thirdPartyPath), as it knows the subscription paths,
// and passes every request under it on to its next hop as it came — the
// path, the query, the body — until the panel on real answers. The answer
// comes back as it is, a refusal too. Nothing here is a subscription: the
// path is not under subPath, and no subId is involved.

// Bounds of what passes through: a post carries an initData and a payload,
// well under a kilobyte each; an answer is at most the widget (some 70 KB).
const (
	thirdPartyBodyLimit   = 16 << 10
	thirdPartyAnswerLimit = 1 << 20
)

// thirdPartyAnswerHeaders are copied back from the next hop's answer.
var thirdPartyAnswerHeaders = []string{"Content-Type", "Cache-Control"}

// thirdPartyPath is the bot's path of the current document, with its
// slashes; "" while there is no document, or a document from a panel that
// names none.
func (s *SubServer) thirdPartyPath() string {
	doc := s.state.Document()
	if doc == nil {
		return ""
	}
	return thirdPartyPathOf(doc)
}

// handleThirdParty passes one request under the bot's path on to the next
// hop.
func (s *SubServer) handleThirdParty(c *gin.Context) {
	method := c.Request.Method
	if method != http.MethodGet && method != http.MethodPost {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	var body io.Reader
	if method == http.MethodPost {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, thirdPartyBodyLimit))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		body = bytes.NewReader(raw)
	}
	base, _, _ := s.nextHop()
	target := base + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		target += "?" + c.Request.URL.RawQuery
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
	if ct := c.GetHeader("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		logger.Warning("proxy-front: the bot's path via the next hop:", err)
		c.Status(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, thirdPartyAnswerLimit))
	if err != nil {
		c.Status(http.StatusBadGateway)
		return
	}
	for _, h := range thirdPartyAnswerHeaders {
		if v := resp.Header.Get(h); v != "" {
			c.Header(h, v)
		}
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), answer)
}
