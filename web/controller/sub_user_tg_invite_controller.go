package controller

import "github.com/gin-gonic/gin"

// Binding a user's Telegram (#187, #219, docs/spec/users.md §11): «Перенести
// сюда» for an id another user has, and the user's invite link. setTelegram
// and create take an @nick as tgNick (sub_user_telegram_controller.go,
// SubUserCreate).
func (a *SubUserController) initTgInviteRoutes(g *gin.RouterGroup) {
	g.POST("/moveTelegram/:subId", a.moveTelegram)
	g.GET("/telegramInvite/:subId", a.telegramInvite)
	g.POST("/telegramInvite/:subId", a.makeTelegramInvite)
	g.POST("/reissueTelegramInvite/:subId", a.reissueTelegramInvite)
}

func (a *SubUserController) moveTelegram(c *gin.Context) {
	var req struct {
		TgId int64 `json:"tgId"`
	}
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.MoveTelegram(c.Param("subId"), req.TgId)
	usersAnswer(c, user, err)
}

// telegramInvite is the user's open invite, null for none.
func (a *SubUserController) telegramInvite(c *gin.Context) {
	inv, err := a.invites.Open(c.Param("subId"))
	usersAnswer(c, a.invites.View(inv), err)
}

// makeTelegramInvite is the user's open invite, a new one when it has none.
func (a *SubUserController) makeTelegramInvite(c *gin.Context) {
	inv, err := a.invites.Invite(c.Param("subId"))
	usersAnswer(c, a.invites.View(inv), err)
}

func (a *SubUserController) reissueTelegramInvite(c *gin.Context) {
	inv, err := a.invites.Reissue(c.Param("subId"))
	usersAnswer(c, a.invites.View(inv), err)
}
