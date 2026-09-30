package service

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The Users section: the user card and the operations on a user (the list
// and the search are in tgbot_users_list.go). Buttons address the user by its
// key — its subId, or a technical user's "@robot"/"@monitoring" — so an old
// card never acts on another user. The screen adds the way back.

// usersRobotPage is how many robot clients one page of the list shows.
const usersRobotPage = 20

// usersCardReply shows the card of the user under key.
func (t *Tgbot) usersCardReply(key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	return t.usersCardOf(v)
}

// usersCardOf is the reply that shows v's card.
func (t *Tgbot) usersCardOf(v *SubUserView) usersReply {
	text, kb := t.usersCard(v)
	return usersReply{text: text, keyboard: kb, route: "usr_c " + v.SubId}
}

// usersCard is a user's card: its subscription's status, its clients by
// protocol, and the buttons the service allows on it — the subscription,
// each client's card, ➕/➖ protocol, pause or resume, delete.
func (t *Tgbot) usersCard(v *SubUserView) (string, *telego.InlineKeyboardMarkup) {
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.users.card", "Name=="+html.EscapeString(v.Name)))
	if v.Technical {
		b.WriteString(t.I18nBot("tgbot.users.technical"))
		b.WriteString(t.I18nBot("tgbot.users.noSubscription"))
		enabled := t.I18nBot("tgbot.messages.no")
		if v.Enable {
			enabled = t.I18nBot("tgbot.messages.yes")
		}
		b.WriteString(t.I18nBot("tgbot.messages.enabled", "Enable=="+enabled))
		b.WriteString(t.I18nBot("tgbot.users.traffic", "UpDown=="+common.FormatTraffic(v.Up+v.Down), "Total=="+t.usersTraffic(v.Total)))
	} else {
		b.WriteString(t.usersSubscriptionLine(v))
		if v.TgId != 0 {
			b.WriteString(t.I18nBot("tgbot.screen.telegram", "TgId=="+strconv.FormatInt(v.TgId, 10)))
		}
		if v.ContactEmail != "" {
			b.WriteString(t.I18nBot("tgbot.users.contactEmail", "Email=="+html.EscapeString(v.ContactEmail)))
		}
	}
	if v.Comment != "" {
		b.WriteString(t.I18nBot("tgbot.users.comment", "Comment=="+html.EscapeString(v.Comment)))
	}
	if len(v.Clients) == 0 {
		b.WriteString(t.I18nBot("tgbot.users.noClients"))
	} else {
		b.WriteString(t.I18nBot("tgbot.users.clients", "Count=="+strconv.Itoa(len(v.Clients))))
		// The technical users can own hundreds of clients: both list them
		// page by page, monitoring's probes read-only (#183).
		if !v.Technical {
			b.WriteString(t.usersClientLines(v.Clients))
		}
	}

	var rows [][]telego.InlineKeyboardButton
	button := func(label, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(t.encodeQuery(data))
	}
	switch {
	case v.SubId == model.SubUserRobotKey:
		if len(v.Clients) > 0 {
			rows = append(rows, tu.InlineKeyboardRow(button(t.I18nBot("tgbot.users.robotClients"), "usr_rob 0")))
		}
	case v.SubId == model.SubUserMonitoringKey:
		// Its probes open read-only (#183).
		if row := t.probeListRow(v); row != nil {
			rows = append(rows, row)
		}
	case !v.Technical:
		rows = append(rows, tu.InlineKeyboardRow(button(t.I18nBot("tgbot.screen.showSub"), usersSubAction+" "+v.SubId)))
		for _, c := range v.Clients {
			data := screenClientAction + " " + c.Name
			if c.Kind != SubUserClientXray {
				data = "tun_c " + c.Key
			}
			rows = append(rows, tu.InlineKeyboardRow(button(protocolLabel(c.Protocol)+" · "+c.Name, data)))
		}
		var row []telego.InlineKeyboardButton
		if len(t.usersMissingInbounds(v)) > 0 {
			row = append(row, button(t.I18nBot("tgbot.users.addProtocol"), "usr_apm "+v.SubId))
		}
		if len(v.Clients) > 0 {
			row = append(row, button(t.I18nBot("tgbot.users.removeProtocol"), "usr_rpm "+v.SubId))
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
		row = nil
		if len(v.Clients) > 0 {
			if v.Enable {
				row = append(row, button(t.I18nBot("tgbot.screen.suspend"), "usr_en "+v.SubId+" 0"))
			} else {
				row = append(row, button(t.I18nBot("tgbot.screen.resume"), "usr_en "+v.SubId+" 1"))
			}
		}
		rows = append(rows, append(row, button(t.I18nBot("tgbot.users.delete"), "usr_del "+v.SubId)))
	}
	var kb *telego.InlineKeyboardMarkup
	if len(rows) > 0 {
		kb = tu.InlineKeyboard(rows...)
	}
	return b.String(), kb
}

// usersClientLines lists clients grouped by protocol, in the order the
// protocols first appear.
func (t *Tgbot) usersClientLines(clients []SubUserClient) string {
	var protocols []string
	byProtocol := map[string][]SubUserClient{}
	for _, c := range clients {
		if _, seen := byProtocol[c.Protocol]; !seen {
			protocols = append(protocols, c.Protocol)
		}
		byProtocol[c.Protocol] = append(byProtocol[c.Protocol], c)
	}
	var b strings.Builder
	for _, p := range protocols {
		fmt.Fprintf(&b, "<b>%s</b>\r\n", html.EscapeString(p))
		for _, c := range byProtocol[p] {
			status := "❌"
			if c.Enable {
				status = "✅"
			}
			fmt.Fprintf(&b, "%s <code>%s</code> · %s · ↑↓%s / %s\r\n", status, html.EscapeString(c.Name),
				html.EscapeString(c.InboundRemark), common.FormatTraffic(c.Up+c.Down), t.usersTraffic(c.TotalGB))
		}
	}
	return b.String()
}

// usersMissingInbounds are the inbounds the user has no client in yet.
func (t *Tgbot) usersMissingInbounds(v *SubUserView) []SubUserInbound {
	inbounds, err := (&SubUserService{}).Inbounds()
	if err != nil {
		return nil
	}
	have := map[int]bool{}
	for _, c := range v.Clients {
		have[c.InboundId] = true
	}
	var out []SubUserInbound
	for _, ib := range inbounds {
		if !have[ib.Id] {
			out = append(out, ib)
		}
	}
	return out
}

// usersRefused shows the service's refusal as it is worded, on top of the
// user's card.
func (t *Tgbot) usersRefused(key string, err error) usersReply {
	refusal := t.usersError(err)
	v, getErr := (&SubUserService{}).Get(key)
	if getErr != nil {
		return refusal
	}
	reply := t.usersCardOf(v)
	reply.toast, reply.text = refusal.toast, refusal.text+"\r\n\r\n"+reply.text
	return reply
}

// usersDone shows the user's card after a change.
func (t *Tgbot) usersDone(v *SubUserView) usersReply {
	reply := t.usersCardOf(v)
	reply.toast = t.I18nBot("tgbot.answers.successfulOperation")
	return reply
}

// --- add protocol ------------------------------------------------------------

// usersAddMenu offers the inbounds the user has no client in.
func (t *Tgbot) usersAddMenu(key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	missing := t.usersMissingInbounds(v)
	if v.Technical || len(missing) == 0 {
		return usersReply{toast: t.I18nBot("tgbot.users.nothingToAdd", "Name=="+v.Name)}
	}
	var rows [][]telego.InlineKeyboardButton
	for _, ib := range missing {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(fmt.Sprintf("%s (%s)", ib.Remark, ib.Protocol)).
			WithCallbackData(t.encodeQuery(fmt.Sprintf("usr_ap %s %d", v.SubId, ib.Id)))))
	}
	return usersReply{text: t.I18nBot("tgbot.users.chooseAdd", "Name=="+html.EscapeString(v.Name)),
		keyboard: tu.InlineKeyboard(rows...), route: "usr_apm " + v.SubId}
}

// usersAddProtocol gives the user a client in the inbound, with the
// parameters the service copies from its other clients.
func (t *Tgbot) usersAddProtocol(key string, inboundId int, linkExisting bool) usersReply {
	v, err := (&SubUserService{}).AddProtocol(key, inboundId, linkExisting)
	if conflict, ok := awgLinkable(err); ok {
		return usersReply{text: t.I18nBot("tgbot.users.linkAwg", "Client=="+html.EscapeString(conflict.Client)),
			keyboard: tu.InlineKeyboard(
				tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.users.link")).
					WithCallbackData(t.encodeQuery(fmt.Sprintf("usr_apl %s %d", key, inboundId)))))}
	}
	if err != nil {
		return t.usersRefused(key, err)
	}
	return t.usersDone(v)
}

// --- remove protocol ---------------------------------------------------------

// usersRemoveMenu offers the user's clients, one button per inbound.
func (t *Tgbot) usersRemoveMenu(key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	var rows [][]telego.InlineKeyboardButton
	seen := map[int]bool{}
	for _, c := range v.Clients {
		if seen[c.InboundId] {
			continue
		}
		seen[c.InboundId] = true
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(fmt.Sprintf("%s (%s)", c.InboundRemark, c.Protocol)).
			WithCallbackData(t.encodeQuery(fmt.Sprintf("usr_rp %s %d", v.SubId, c.InboundId)))))
	}
	return usersReply{text: t.I18nBot("tgbot.users.chooseRemove", "Name=="+html.EscapeString(v.Name)),
		keyboard: tu.InlineKeyboard(rows...), route: "usr_rpm " + v.SubId}
}

// usersRemoveConfirm asks before a client goes.
func (t *Tgbot) usersRemoveConfirm(key string, inboundId int) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	var names []string
	remark := ""
	for _, c := range v.Clients {
		if c.InboundId == inboundId {
			names = append(names, c.Name)
			remark = c.InboundRemark
		}
	}
	if len(names) == 0 {
		return t.usersCardReply(key)
	}
	return usersReply{
		text: t.I18nBot("tgbot.users.confirmRemove", "Client=="+html.EscapeString(strings.Join(names, ", ")),
			"Inbound=="+html.EscapeString(remark), "Name=="+html.EscapeString(v.Name)),
		keyboard: tu.InlineKeyboard(
			tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.users.confirm")).
				WithCallbackData(t.encodeQuery(fmt.Sprintf("usr_rpc %s %d", key, inboundId))))),
		route: fmt.Sprintf("usr_rp %s %d", key, inboundId)}
}

func (t *Tgbot) usersRemoveProtocol(key string, inboundId int) usersReply {
	v, err := (&SubUserService{}).RemoveProtocol(key, inboundId)
	if err != nil {
		return t.usersRefused(key, err)
	}
	return t.usersDone(v)
}

// --- enable, delete ----------------------------------------------------------

func (t *Tgbot) usersSetEnable(key string, enable bool) usersReply {
	v, err := (&SubUserService{}).SetEnable(key, enable)
	if err != nil {
		return t.usersRefused(key, err)
	}
	return t.usersDone(v)
}

func (t *Tgbot) usersDeleteConfirm(key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	return usersReply{text: t.I18nBot("tgbot.users.confirmDelete", "Name=="+html.EscapeString(v.Name)),
		keyboard: tu.InlineKeyboard(
			tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.users.confirm")).
				WithCallbackData(t.encodeQuery("usr_delc " + key)))),
		route: "usr_del " + key}
}

// usersDelete deletes the user and shows the list without it: its card is
// gone, so is the way back to it.
func (t *Tgbot) usersDelete(chatId int64, key string) usersReply {
	users := &SubUserService{}
	v, err := users.Get(key)
	if err != nil {
		return t.usersError(err)
	}
	if err := users.Delete(key); err != nil {
		return t.usersRefused(key, err)
	}
	reply := t.usersList(chatId, 0)
	reply.toast = t.I18nBot("tgbot.users.deleted", "Name=="+v.Name)
	reply.text = t.I18nBot("tgbot.users.deleted", "Name=="+html.EscapeString(v.Name)) + "\r\n\r\n" + reply.text
	reply.root = true
	return reply
}

// --- robot's clients ---------------------------------------------------------

// usersRobotList is a page of robot's clients, each to be assigned to a user.
func (t *Tgbot) usersRobotList(page int) usersReply {
	v, err := (&SubUserService{}).Get(model.SubUserRobotKey)
	if err != nil {
		return t.usersError(err)
	}
	total := len(v.Clients)
	if total == 0 {
		return t.usersCardReply(model.SubUserRobotKey)
	}
	pages := (total + usersRobotPage - 1) / usersRobotPage
	page = max(0, min(page, pages-1))
	from, to := page*usersRobotPage, min((page+1)*usersRobotPage, total)

	var rows [][]telego.InlineKeyboardButton
	for _, c := range v.Clients[from:to] {
		label := fmt.Sprintf("%s · %s", c.Name, t.I18nBot("tgbot.users.assign"))
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).WithCallbackData(t.encodeQuery("usr_as "+c.Name))))
	}
	var nav []telego.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, tu.InlineKeyboardButton("⬅️").WithCallbackData(fmt.Sprintf("usr_rob %d", page-1)))
	}
	if page < pages-1 {
		nav = append(nav, tu.InlineKeyboardButton("➡️").WithCallbackData(fmt.Sprintf("usr_rob %d", page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	return usersReply{text: t.I18nBot("tgbot.users.robotList", "From=="+strconv.Itoa(from+1), "To=="+strconv.Itoa(to),
		"Count=="+strconv.Itoa(total)), keyboard: tu.InlineKeyboard(rows...), route: fmt.Sprintf("usr_rob %d", page)}
}

// usersAssign gives the robot client the chat picked to the user it named.
func (t *Tgbot) usersAssign(chatId int64, query string) usersReply {
	var client string
	usersSessions.with(chatId, func(s *usersSession) { client, s.assignClient = s.assignClient, "" })
	if client == "" {
		return t.usersExpired()
	}
	users := &SubUserService{}
	target, err := users.Find(query)
	if err != nil {
		return t.usersError(err)
	}
	v, err := users.Assign(target.SubId, client)
	if err != nil {
		return t.usersError(err)
	}
	return t.usersCardOf(v)
}
