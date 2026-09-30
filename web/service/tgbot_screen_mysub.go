package service

import (
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The client's side of the bot (#194, docs/spec/users.md §10): the screens
// of someone who is no admin. Their chat is one screen as well
// (tgbot_screen.go), showing their own users only, as telegramSubUsers finds
// them:
//   - «My subscription», the main menu of someone with one user: on or
//     paused, the expiry, the traffic, the protocols; «🔗 Show subscription»
//     (the link, its QR as a file), «📄 My configs» (the clients; a tunnel
//     client's .conf and QR, the xray links and their QRs, as files) and
//     «🔄 Refresh»;
//   - the pick among several users that carry the same Telegram ID (legacy
//     data: the rule is one user per account, #178);
//   - «No subscription» for someone with none. «📝 Leave a request» stays
//     hidden until requests exist (#188).
//
// A route names its user by subId and a tunnel client by uuid. The access
// list (tgbot_access.go) lets a client press only routes that name their
// own; each handler looks the user up among theirs again. The buttons of the
// old client menu open these screens (mysubAlias).

// Callback data of the client's screens; the main menu is screenMenuRoute.
const (
	mysubUserRoute    = "my_u"   // my_u <subId>: one of the client's users, picked among several
	mysubSubRoute     = "my_sub" // my_sub <subId>: its subscription, the QR as a file
	mysubConfigsRoute = "my_cfg" // my_cfg <subId>: its clients
	mysubLinksAction  = "my_lnk" // my_lnk <subId>: the xray links and their QRs, as messages
	mysubTunnelAction = "my_tun" // my_tun <uuid>: a tunnel client's .conf and QR, as files
)

// telegramSubUsers are the users of a Telegram account: today, the regular
// users one of whose clients carries its ID, as clientsOfTelegram reads it
// from the parsed settings (#201). This is the one place that ties
// an account to users; #186 moves it to the Telegram account itself.
func telegramSubUsers(tgId int64) ([]*SubUserView, error) {
	owned, err := clientsOfTelegram(tgId)
	if err != nil || owned.empty() {
		return nil, err
	}
	all, err := (&SubUserService{}).List()
	if err != nil {
		return nil, err
	}
	var out []*SubUserView
	for _, v := range all {
		if !v.Technical && slices.ContainsFunc(v.Clients, owned.has) {
			out = append(out, v)
		}
	}
	return out, nil
}

// telegramSubUser is the account's user of that subId; nil when it is not
// one of theirs.
func telegramSubUser(tgId int64, subId string) *SubUserView {
	return telegramUserWith(tgId, func(v *SubUserView) bool { return v.SubId == subId })
}

// telegramClientUser is the account's user that has the client of that name
// (xray email); nil when none has it.
func telegramClientUser(tgId int64, name string) *SubUserView {
	return telegramUserWith(tgId, func(v *SubUserView) bool {
		return slices.ContainsFunc(v.Clients, func(c SubUserClient) bool { return c.Name == name })
	})
}

// telegramTunnelUser is the account's user that has the tunnel client of that
// uuid; nil when none has it.
func telegramTunnelUser(tgId int64, clientUUID string) *SubUserView {
	return telegramUserWith(tgId, func(v *SubUserView) bool {
		return slices.ContainsFunc(v.Clients, func(c SubUserClient) bool {
			return c.Kind != SubUserClientXray && c.Key == clientUUID
		})
	})
}

func telegramUserWith(tgId int64, match func(*SubUserView) bool) *SubUserView {
	users, err := telegramSubUsers(tgId)
	if err != nil {
		return nil
	}
	for _, v := range users {
		if match(v) {
			return v
		}
	}
	return nil
}

// --- the screen's hooks -------------------------------------------------------

// screenHome is the screen's main menu: the admin's, or the client's own.
func (t *Tgbot) screenHome(sc *botScreen) screenReply {
	if sc.client != 0 {
		return t.mysubHome(sc.client)
	}
	return t.screenMainMenu()
}

// screenRouteOf runs a press on the screen: a client's screen has the
// client's routes only.
func (t *Tgbot) screenRouteOf(chatId int64, sc *botScreen, data string) screenReply {
	if sc.client != 0 {
		return t.mysubRoute(sc.client, data)
	}
	return t.screenRoute(chatId, data)
}

// clientPress is a press of someone who is no admin, once clientMayPress let
// it through: a button of the old client menu opens its screen anew at the
// bottom; the rest go to the chat's screen.
func (t *Tgbot) clientPress(query *telego.CallbackQuery) {
	if data, err := t.decodeQuery(query.Data); err == nil {
		if reply, ok := t.mysubAlias(query.From.ID, data); ok {
			t.sendCallbackAnswerTgBot(query.ID, "")
			t.clientOpen(query.Message.GetChat().ID, reply)
			return
		}
	}
	t.screenPressAs(query, query.From.ID)
}

// answerClientCommand runs a command of someone who is no admin that opens
// the screen: /start and /help, their main menu. false for the others.
func (t *Tgbot) answerClientCommand(chatId, from int64, command string) bool {
	if command != "start" && command != "help" {
		return false
	}
	t.clientOpen(chatId, t.mysubHome(from))
	return true
}

// clientOpen shows a client's view in a new screen at the bottom. It never
// hands screenOpen an empty view, which would show the admin's menu.
func (t *Tgbot) clientOpen(chatId int64, reply screenReply) {
	if reply.text == "" {
		reply.text = t.I18nBot("tgbot.noResult")
	}
	t.screenOpen(chatId, reply)
}

// --- routes --------------------------------------------------------------------

// mysubRoute runs a press on a client's screen. A route that names no user or
// client of theirs shows their main menu with «No result».
func (t *Tgbot) mysubRoute(tgId int64, data string) screenReply {
	action, arg, _ := strings.Cut(data, " ")
	if action == screenMenuRoute {
		return t.mysubHome(tgId)
	}
	if action == mysubTunnelAction {
		if telegramTunnelUser(tgId, arg) != nil {
			return t.tunnelConfig(arg)
		}
		return t.mysubRefused(tgId)
	}
	v := telegramSubUser(tgId, arg)
	if v == nil {
		return t.mysubRefused(tgId)
	}
	switch action {
	case mysubUserRoute:
		return t.mysubUser(v, mysubUserRoute+" "+v.SubId)
	case mysubSubRoute:
		return t.mysubSubscription(v)
	case mysubConfigsRoute:
		return t.mysubConfigs(v)
	case mysubLinksAction:
		return t.mysubLinks(v)
	}
	return t.mysubRefused(tgId)
}

// mysubRefused answers a press that names nothing of the client's.
func (t *Tgbot) mysubRefused(tgId int64) screenReply {
	reply := t.mysubHome(tgId)
	reply.toast = t.I18nBot("tgbot.noResult")
	return reply
}

// mysubAlias opens the screen a button of the old client menu stands for:
// usage and commands the main menu, the subscription links «Show
// subscription», the individual and QR links (and a client's usage)
// «My configs». With a client's email the user is that client's; without
// one it is the client's only user, or the main menu picks. false for data
// that is no old client button.
func (t *Tgbot) mysubAlias(tgId int64, data string) (screenReply, bool) {
	action, email, _ := strings.Cut(data, " ")
	var route string
	switch action {
	case "client_traffic", "client_commands":
		return t.mysubHome(tgId), true
	case "client_sub_links":
		route = mysubSubRoute
	case "client_individual_links", "client_qr_links", screenClientAction:
		route = mysubConfigsRoute
	default:
		return screenReply{}, false
	}
	var v *SubUserView
	if email != "" {
		v = telegramClientUser(tgId, email)
	} else if users, err := telegramSubUsers(tgId); err == nil && len(users) == 1 {
		v = users[0]
	}
	if v == nil {
		return t.mysubHome(tgId), true
	}
	return t.mysubRoute(tgId, route+" "+v.SubId), true
}

// --- screens -------------------------------------------------------------------

// mysubHome is the client's main menu: «My subscription» of their one user,
// the pick among several, or «No subscription».
func (t *Tgbot) mysubHome(tgId int64) screenReply {
	users, err := telegramSubUsers(tgId)
	if err != nil {
		reply := screenReply{usersReply: t.usersError(err)}
		reply.route = screenMenuRoute
		return reply
	}
	switch len(users) {
	case 0:
		return t.mysubNone(tgId)
	case 1:
		return t.mysubUser(users[0], screenMenuRoute)
	}
	return t.mysubPick(users)
}

// mysubButton is a button of the client's screens.
func (t *Tgbot) mysubButton(label, data string) telego.InlineKeyboardButton {
	return tu.InlineKeyboardButton(label).WithCallbackData(t.encodeQuery(data))
}

// mysubUser is «My subscription» of the user v; route is how it shows again:
// the main menu, or the user picked among several.
func (t *Tgbot) mysubUser(v *SubUserView, route string) screenReply {
	status := t.I18nBot("tgbot.screen.subActive")
	if !v.Enable {
		status = t.I18nBot("tgbot.screen.subPaused")
	}
	text := t.I18nBot("tgbot.mysub.card", "Name=="+html.EscapeString(v.Name), "Status=="+status,
		"Exp=="+t.mysubExpiry(v), "Traffic=="+t.usersTrafficShort(v.Up+v.Down, v.Total), "Protocols=="+usersProtocols(v))
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.screen.showSub"), mysubSubRoute+" "+v.SubId)),
		tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.mysub.configs"), mysubConfigsRoute+" "+v.SubId)),
		tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.buttons.refresh"), route)),
	)
	return screenReply{usersReply: usersReply{text: text, keyboard: kb, route: route}}
}

// mysubExpiry words the subscription's expiry for its owner: until a date,
// N days from the first connection, none — or «—» when the clients' expiries
// differ.
func (t *Tgbot) mysubExpiry(v *SubUserView) string {
	switch {
	case v.ExpiryTime > 0:
		return t.I18nBot("tgbot.mysub.until", "Date=="+t.usersExpiryShort(v.ExpiryTime, time.Now()))
	case v.ExpiryTime < 0:
		return t.I18nBot("tgbot.mysub.afterFirstUse", "Days=="+strconv.FormatInt(v.ExpiryTime/-86400000, 10))
	case slices.ContainsFunc(v.Clients, func(c SubUserClient) bool { return c.ExpiryTime != 0 }):
		return "—"
	}
	return t.I18nBot("tgbot.mysub.noExpiry")
}

// mysubPick lists the client's users, each opening its «My subscription».
func (t *Tgbot) mysubPick(users []*SubUserView) screenReply {
	now := time.Now()
	var rows [][]telego.InlineKeyboardButton
	for _, v := range users {
		rows = append(rows, tu.InlineKeyboardRow(t.mysubButton(t.usersListLine(v, now), mysubUserRoute+" "+v.SubId)))
	}
	return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.mysub.pick", "Count=="+strconv.Itoa(len(users))),
		keyboard: tu.InlineKeyboard(rows...), route: screenMenuRoute}}
}

// mysubNone is «No subscription»: the person's Telegram ID for the admin, and
// the way to look again.
func (t *Tgbot) mysubNone(tgId int64) screenReply {
	kb := tu.InlineKeyboard(tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.buttons.refresh"), screenMenuRoute)))
	return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.mysub.none", "TgId=="+strconv.FormatInt(tgId, 10)),
		keyboard: kb, route: screenMenuRoute}}
}

// mysubSubscription is «Show subscription» for the client: the links and the
// status on the screen, the QR of the link as a file.
func (t *Tgbot) mysubSubscription(v *SubUserView) screenReply {
	shown := t.usersSubscription(v.SubId)
	if shown.route == "" { // the user could not be read
		return screenReply{usersReply: shown}
	}
	reply := screenReply{usersReply: usersReply{text: shown.text + "\r\n" + t.I18nBot("tgbot.mysub.subHint"),
		route: mysubSubRoute + " " + v.SubId}}
	reply.files = t.usersSubscriptionQR(v.SubId).files
	return reply
}

// mysubConfigs is «My configs»: the user's clients with their state and
// traffic; a tunnel client's button sends its .conf and QR, the xray links'
// button the links and their QRs.
func (t *Tgbot) mysubConfigs(v *SubUserView) screenReply {
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.mysub.configsTitle", "Name=="+html.EscapeString(v.Name)))
	var rows [][]telego.InlineKeyboardButton
	xray := false
	for _, c := range v.Clients {
		status := "🟢"
		if !c.Enable {
			status = "⏸"
		}
		b.WriteString("\r\n" + status + " " + protocolLabel(c.Protocol) + " · " + html.EscapeString(c.Name) + " · " +
			t.usersTrafficShort(c.Up+c.Down, c.TotalGB))
		if c.Kind == SubUserClientXray {
			xray = true
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(t.mysubButton(
			t.I18nBot("tgbot.mysub.conf", "Name=="+protocolLabel(c.Protocol)+" · "+c.Name), mysubTunnelAction+" "+c.Key)))
	}
	if len(v.Clients) == 0 {
		b.WriteString("\r\n" + t.I18nBot("tgbot.mysub.noClients"))
	}
	if xray {
		rows = append(rows, tu.InlineKeyboardRow(t.mysubButton(t.I18nBot("tgbot.mysub.links"), mysubLinksAction+" "+v.SubId)))
	}
	var kb *telego.InlineKeyboardMarkup
	if len(rows) > 0 {
		kb = tu.InlineKeyboard(rows...)
	}
	return screenReply{usersReply: usersReply{text: b.String(), keyboard: kb, route: mysubConfigsRoute + " " + v.SubId}}
}

// mysubLinks sends the chat the individual links of the user's subscription
// and their QRs, as an xray client's card does.
func (t *Tgbot) mysubLinks(v *SubUserView) screenReply {
	i := slices.IndexFunc(v.Clients, func(c SubUserClient) bool { return c.Kind == SubUserClientXray })
	if i < 0 {
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.noResult")}}
	}
	email := v.Clients[i].Name
	return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation")},
		after: func(chatId int64) {
			t.sendClientIndividualLinks(chatId, email)
			t.sendClientQRLinks(chatId, email)
		}}
}
