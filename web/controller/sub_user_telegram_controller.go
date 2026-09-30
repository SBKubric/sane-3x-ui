package controller

import "github.com/gin-gonic/gin"

// The user's Telegram (#186, docs/spec/users.md §11): what a conflict needs
// to be resolved — the tgIds on the user's clients and who else has each —
// «Назначить <tg_id>» and «Отвязать Telegram».
func (a *SubUserController) initTelegramRoutes(g *gin.RouterGroup) {
	g.GET("/telegram/:subId", a.telegram)
	g.POST("/setTelegram/:subId", a.setTelegram)
	g.POST("/unlinkTelegram/:subId", a.unlinkTelegram)
}

func (a *SubUserController) telegram(c *gin.Context) {
	tg, err := a.users.Telegram(c.Param("subId"))
	usersAnswer(c, tg, err)
}

func (a *SubUserController) setTelegram(c *gin.Context) {
	var req struct {
		TgId   int64  `json:"tgId"`
		TgNick string `json:"tgNick"` // an @nick instead of the id (#219)
	}
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.SetTelegramOf(c.Param("subId"), req.TgId, req.TgNick)
	usersAnswer(c, user, err)
}

func (a *SubUserController) unlinkTelegram(c *gin.Context) {
	user, err := a.users.UnlinkTelegram(c.Param("subId"))
	usersAnswer(c, user, err)
}
