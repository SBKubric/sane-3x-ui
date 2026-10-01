package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/coinman-dev/3ax-ui/v2/web/entity"
	"github.com/coinman-dev/3ax-ui/v2/web/service"

	"github.com/gin-gonic/gin"
)

// SubUserController serves users (docs/spec/users.md §6) under
// <webBasePath>panel/api/users for the panel's users page. It hangs off the
// /panel/api group and inherits its session check. Every handler parses its
// parameters and calls one SubUserService method; the service owns the rules.
// A user is addressed by its subId, a technical user by its key (@robot,
// @monitoring).
type SubUserController struct {
	users   service.SubUserService
	invites service.TgInviteService
	tgbot   service.Tgbot
}

// NewSubUserController registers the users routes on g.
func NewSubUserController(g *gin.RouterGroup) *SubUserController {
	a := &SubUserController{}
	g.GET("/list", a.list)
	g.GET("/inbounds", a.inbounds)
	g.GET("/get/:subId", a.get)
	g.GET("/find", a.find)
	g.GET("/search", a.search)
	g.POST("/create", a.create)
	g.POST("/addProtocol/:subId", a.addProtocol)
	g.POST("/removeProtocol/:subId", a.removeProtocol)
	g.POST("/enable/:subId", a.enable)
	g.POST("/del/:subId", a.del)
	g.POST("/assign/:subId", a.assign)
	a.initTelegramRoutes(g) // #186: sub_user_telegram_controller.go
	a.initTgInviteRoutes(g) // #219: sub_user_tg_invite_controller.go
	a.initLinkRoutes(g)     // #222: sub_user_link_controller.go
	return a
}

// usersMaxBodyBytes caps a request body; the largest is a create.
const usersMaxBodyBytes = 64 << 10

// usersAnswer is the panel envelope: the object on success; on a refusal the
// service's own message, which names the conflicting user or client, and a
// SubUserConflict as the object when the operator can resolve it.
func usersAnswer(c *gin.Context, obj any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, entity.Msg{Success: true, Obj: obj})
		return
	}
	msg := entity.Msg{Msg: err.Error()}
	var conflict *service.SubUserConflict
	if errors.As(err, &conflict) {
		msg.Obj = conflict
	}
	c.JSON(http.StatusOK, msg)
}

// readUsersBody decodes a JSON body strictly: unknown fields and trailing
// data are refused; an empty body is the zero value.
func readUsersBody(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, usersMaxBodyBytes)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		usersAnswer(c, nil, errors.New("invalid body: "+err.Error()))
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		usersAnswer(c, nil, errors.New("invalid body: trailing data"))
		return false
	}
	return true
}

func (a *SubUserController) list(c *gin.Context) {
	users, err := a.users.List()
	usersAnswer(c, users, err)
}

func (a *SubUserController) inbounds(c *gin.Context) {
	inbounds, err := a.users.Inbounds()
	usersAnswer(c, inbounds, err)
}

func (a *SubUserController) get(c *gin.Context) {
	user, err := a.users.Get(c.Param("subId"))
	usersAnswer(c, user, err)
}

func (a *SubUserController) find(c *gin.Context) {
	user, err := a.users.Find(c.Query("q"))
	usersAnswer(c, user, err)
}

// search is the fuzzy search of the users page and the bot, best first; nobody
// is an empty list.
func (a *SubUserController) search(c *gin.Context) {
	users, err := a.users.Search(c.Query("q"))
	if users == nil {
		users = []*service.SubUserView{}
	}
	usersAnswer(c, users, err)
}

func (a *SubUserController) create(c *gin.Context) {
	var req service.SubUserCreate
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.Create(req)
	usersAnswer(c, user, err)
}

// usersInboundRequest is the body of addProtocol and removeProtocol.
type usersInboundRequest struct {
	InboundId    int  `json:"inboundId"`
	LinkExisting bool `json:"linkExisting"`
}

func (a *SubUserController) addProtocol(c *gin.Context) {
	var req usersInboundRequest
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.AddProtocol(c.Param("subId"), req.InboundId, req.LinkExisting)
	usersAnswer(c, user, err)
}

func (a *SubUserController) removeProtocol(c *gin.Context) {
	var req usersInboundRequest
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.RemoveProtocol(c.Param("subId"), req.InboundId)
	usersAnswer(c, user, err)
}

func (a *SubUserController) enable(c *gin.Context) {
	var req struct {
		Enable bool `json:"enable"`
	}
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.SetEnable(c.Param("subId"), req.Enable)
	usersAnswer(c, user, err)
}

func (a *SubUserController) del(c *gin.Context) {
	usersAnswer(c, nil, a.users.Delete(c.Param("subId")))
}

func (a *SubUserController) assign(c *gin.Context) {
	var req struct {
		Client string `json:"client"`
	}
	if !readUsersBody(c, &req) {
		return
	}
	user, err := a.users.Assign(c.Param("subId"), req.Client)
	usersAnswer(c, user, err)
}
