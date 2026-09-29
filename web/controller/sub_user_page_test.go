package controller

import (
	"html/template"
	"net/http"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/web/session"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// newPanelPagesRouter builds the panel's page routes the way the server does,
// with a stand-in for each template that prints the page's title key: which
// template a route renders is the point here, what is in it is covered by
// the web package's TestPagesRender.
func newPanelPagesRouter(t *testing.T, pages ...string) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("3ax-ui", cookie.NewStore([]byte("pages-test-secret"))))
	r.GET("/test-login", func(c *gin.Context) {
		if err := session.SetLoginUser(c, &model.User{Id: 1, Username: "admin"}); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	tpl := template.New("")
	for _, page := range pages {
		template.Must(tpl.New(page).Parse(page + " {{ .title }}"))
	}
	r.SetHTMLTemplate(tpl)
	NewXUIController(r.Group("/"))
	return r, monUILogin(t, r)
}

// TestUsersPageRoute: the users page (#170) is /panel/users, rendered from
// users.html, and like every panel page it is only there behind a session.
func TestUsersPageRoute(t *testing.T) {
	r, cookie := newPanelPagesRouter(t, "users.html")

	w := monUIGet(r, "/panel/users", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /panel/users: status %d", w.Code)
	}
	if got, want := w.Body.String(), "users.html pages.subUsers.title"; got != want {
		t.Errorf("GET /panel/users rendered %q, want %q", got, want)
	}

	if w := monUIGet(r, "/panel/users", ""); w.Code != http.StatusTemporaryRedirect {
		t.Errorf("GET /panel/users without a session: status %d, want the login redirect", w.Code)
	}
}
