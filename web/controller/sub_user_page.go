package controller

import "github.com/gin-gonic/gin"

// subUsers renders the users page (docs/spec/users.md §9, #170). The page
// holds no data of its own: everything it shows and changes goes through
// /panel/api/users (sub_user_controller.go).
func (a *XUIController) subUsers(c *gin.Context) {
	html(c, "users.html", "pages.subUsers.title", nil)
}
