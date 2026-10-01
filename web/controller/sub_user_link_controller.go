package controller

import "github.com/gin-gonic/gin"

// The users page's «Send links» (#222, docs/spec/users.md §13): the bot
// sends every enabled user with Telegram their subscription link, and the
// admins the report. The page asks before it calls.
func (a *SubUserController) initLinkRoutes(g *gin.RouterGroup) {
	g.POST("/broadcastLinks", a.broadcastLinks)
}

// broadcastLinks starts the broadcast and answers with its number and
// audience; the bot not running is the refusal.
func (a *SubUserController) broadcastLinks(c *gin.Context) {
	started, err := a.tgbot.BroadcastLinksFromPanel()
	usersAnswer(c, started, err)
}
