package controller

import (
	"github.com/coinman-dev/3ax-ui/v2/web/service"
	"github.com/gin-gonic/gin"
)

// TgNotifyController serves the «Send test» button of the notification
// channel on the Telegram settings tab (#195), under
// <webBasePath>panel/api/tgbot. It hangs off the /panel/api group, so it
// inherits APIController.checkAPIAuth: no session, a bare 404.
type TgNotifyController struct {
	tgbot service.Tgbot
}

// NewTgNotifyController registers the route on g.
func NewTgNotifyController(g *gin.RouterGroup) *TgNotifyController {
	a := &TgNotifyController{}
	g.POST("/notifyTest", a.notifyTest)
	return a
}

// tgNotifyTestForm is the channel as typed in the settings form, saved or not.
type tgNotifyTestForm struct {
	ChatId string `json:"chatId" form:"chatId"`
}

// notifyTest posts a test message to the channel and answers with
// Telegram's refusal as the error, so the operator sees why a channel does
// not work — the bot not being its admin, a wrong id — before saving it.
func (a *TgNotifyController) notifyTest(c *gin.Context) {
	form := &tgNotifyTestForm{}
	if err := c.ShouldBind(form); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tgNotifyChannelTestFail"), err)
		return
	}
	if err := a.tgbot.SendNotifyTest(form.ChatId); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tgNotifyChannelTestFail"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.tgNotifyChannelTestOk"), nil)
}
