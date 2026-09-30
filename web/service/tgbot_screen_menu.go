package service

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The admin's main menu and the screens behind it that are not the users'
// (#191): Inbounds and clients and the chain. Online, Reports, Monitoring,
// Server and the admin link are #192's (tgbot_screen_ops.go).

// Callback data of these screens.
const (
	screenInboundsRoute = "s_ibs"            // the inbounds
	screenInboundAction = "s_ib"             // s_ib <inbound id> <page>: its clients
	screenOnlineRoute   = "s_onl"            // the clients online
	screenServerRoute   = "s_srv"            // server status
	screenBackupData    = "s_bak"            // the DB backup, as files
	screenBanLogsData   = "s_ban"            // the ban logs, as files
	screenChainRoute    = "s_chain"          // the chain's hops, as /proxy lists them
	screenSoonData      = "s_soon"           // a screen still to come
	screenPage          = 20                 // list lines per page
	screenClientAction  = "client_get_usage" // client_get_usage <email>: an xray client's card
)

// screenMenuCallback runs the buttons of these screens; false for data that
// is not theirs.
func (t *Tgbot) screenMenuCallback(chatId int64, data string) (screenReply, bool) {
	action, args, _ := strings.Cut(data, " ")
	switch action {
	case screenMenuRoute:
		return t.screenMainMenu(), true
	case screenInboundsRoute:
		return t.screenInbounds(), true
	case screenInboundAction:
		idArg, pageArg, _ := strings.Cut(args, " ")
		id, _ := strconv.Atoi(idArg)
		page, _ := strconv.Atoi(pageArg)
		return t.screenInboundClients(id, page), true
	case screenOnlineRoute:
		page, _ := strconv.Atoi(args)
		return t.screenOnline(page), true
	case screenServerRoute:
		return t.screenServer(), true
	case screenBackupData:
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.buttons.dbBackup")}, after: t.sendBackup}, true
	case screenBanLogsData:
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.buttons.getBanLogs")},
			after: func(chatId int64) { t.sendBanLogs(chatId, true) }}, true
	case screenChainRoute:
		return t.screenChain(nil), true
	case chainSwitchCallback:
		// The "Switch" button of /proxy <name> (#139).
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation"),
			text: t.chainSwitchConfirmed(args)}}, true
	case screenSoonData:
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.screen.soon")}}, true
	case usersSubQRAction:
		return t.usersSubscriptionQR(args), true
	case tgInviteQRAction: // the QR of an invite link (#219)
		return t.usersInviteQR(args), true
	}
	return t.screenOpsCallback(action, args)
}

// answerScreenCommand runs an admin's command that opens a screen: /start
// and /help the main menu, /usage <name>, /inbound <remark> and /proxy
// theirs, /status the server and /restart its confirmation (#192). false
// for the other commands.
func (t *Tgbot) answerScreenCommand(chatId int64, command string, args []string) bool {
	switch {
	case command == "start" || command == "help":
		t.screenStart(chatId)
	case command == "usage" && len(args) > 0:
		t.screenOpen(chatId, t.screenUsage(chatId, args[0]))
	case command == "inbound" && len(args) > 0:
		t.screenOpen(chatId, t.screenInboundSearch(args[0]))
	case command == "proxy":
		t.screenOpen(chatId, t.screenChain(args))
	case command == "status":
		t.screenOpen(chatId, t.screenServer())
	case command == "restart" && len(args) == 0:
		t.screenOpen(chatId, t.screenRestartConfirm())
	default:
		return false
	}
	return true
}

// screenUsage is /usage <name>: the card of the client of that email (a
// probe's read-only), else the users the name finds.
func (t *Tgbot) screenUsage(chatId int64, name string) screenReply {
	if reply, ok := t.probeSearch(name); ok {
		return screenReply{usersReply: reply}
	}
	if traffic, err := t.inboundService.GetClientTrafficByEmail(name); err == nil && traffic != nil {
		return t.clientCard(name, "")
	}
	return screenReply{usersReply: t.usersSearchReply(chatId, name)}
}

// screenInboundSearch is /inbound <remark>: the clients of the one inbound
// whose remark has it, the inbounds when several do.
func (t *Tgbot) screenInboundSearch(remark string) screenReply {
	inbounds, err := t.inboundService.SearchInbounds(remark)
	switch {
	case err != nil:
		return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.wentWrong")}}
	case len(inbounds) == 0:
		return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.noInbounds")}}
	case len(inbounds) == 1:
		return t.screenInboundClients(inbounds[0].Id, 0)
	}
	return t.screenInbounds()
}

// screenMainMenu is the admin's main menu. «📥 Incoming requests» stays
// hidden until requests exist.
func (t *Tgbot) screenMainMenu() screenReply {
	button := func(key, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(t.I18nBot(key)).WithCallbackData(data)
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(button("tgbot.users.menu", usersListAction+" 0"), button("tgbot.users.newUser", "add_client")),
		tu.InlineKeyboardRow(button("tgbot.screen.inbounds", screenInboundsRoute), button("tgbot.screen.online", screenOnlineRoute)),
		tu.InlineKeyboardRow(button("tgbot.screen.reports", screenReportsRoute), button("tgbot.screen.monitoring", screenMonitoringRoute)),
		tu.InlineKeyboardRow(button("tgbot.screen.server", screenServerRoute), button("tgbot.screen.admin", screenAdminLinkData)),
	)
	return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.screen.menu", "Hostname=="+html.EscapeString(hostname)),
		keyboard: kb, route: screenMenuRoute}}
}

// screenPager is the row that pages a list: ◀, the page, ▶. prefix is the
// callback data the page number is appended to; nil for a single page.
func screenPager(prefix string, page, pages int) []telego.InlineKeyboardButton {
	if pages <= 1 {
		return nil
	}
	at := func(label string, p int) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(fmt.Sprintf("%s %d", prefix, p))
	}
	var row []telego.InlineKeyboardButton
	if page > 0 {
		row = append(row, at("◀", page-1))
	}
	row = append(row, at(fmt.Sprintf("%d/%d", page+1, pages), page))
	if page < pages-1 {
		row = append(row, at("▶", page+1))
	}
	return row
}

// screenPageOf clamps page into a list of total lines of size per page and
// returns it with the page count and the bounds of its lines.
func screenPageOf(page, total, size int) (int, int, int, int) {
	pages := max(1, (total+size-1)/size)
	page = max(0, min(page, pages-1))
	return page, pages, page * size, min((page+1)*size, total)
}

// protocolLabel is a protocol as the screens name it.
func protocolLabel(protocol string) string {
	switch model.Protocol(protocol) {
	case model.VLESS:
		return "VLESS"
	case model.VMESS:
		return "VMess"
	case model.Trojan:
		return "Trojan"
	case model.Shadowsocks:
		return "SS"
	case model.AmneziaWG:
		return "AWG"
	case model.NativeWG, model.WireGuard:
		return "WG"
	}
	return strings.ToUpper(protocol)
}

// --- inbounds and clients ------------------------------------------------------

// screenInbounds lists the inbounds, each opening its clients.
func (t *Tgbot) screenInbounds() screenReply {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil || len(inbounds) == 0 {
		return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.answers.getInboundsFailed"), route: screenInboundsRoute}}
	}
	var rows [][]telego.InlineKeyboardButton
	for _, ib := range inbounds {
		status := "❌"
		if ib.Enable {
			status = "✅"
		}
		label := fmt.Sprintf("%s %s · %s", status, protocolLabel(string(ib.Protocol)), ib.Remark)
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).
			WithCallbackData(fmt.Sprintf("%s %d 0", screenInboundAction, ib.Id))))
	}
	return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.screen.inboundsTitle", "Count=="+strconv.Itoa(len(inbounds))),
		keyboard: tu.InlineKeyboard(rows...), route: screenInboundsRoute}}
}

// screenInboundClient is a line of an inbound's client list.
type screenInboundClient struct {
	name, data string
	enable     bool
}

// screenInboundClients is a page of an inbound's clients, each opening its
// card: an xray client's by its email (a probe's read-only), a tunnel
// client's by its uuid (probes left out, as #182 lists them).
func (t *Tgbot) screenInboundClients(id, page int) screenReply {
	route := fmt.Sprintf("%s %d %d", screenInboundAction, id, page)
	inbound, err := t.inboundService.GetInbound(id)
	if err != nil {
		return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.answers.getInboundsFailed")}}
	}
	var clients []screenInboundClient
	if kind, isTunnel := tunnelKindOf(inbound.Protocol); isTunnel {
		peers, err := tunnelClientsOf(kind).GetClients()
		if err != nil {
			return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.answers.getClientsFailed"), route: route}}
		}
		for _, c := range peers {
			if !IsProbeAccount(c.Email) {
				clients = append(clients, screenInboundClient{name: c.Email, data: "tun_c " + c.UUID, enable: c.Enable})
			}
		}
	} else if xrayClients, err := t.inboundService.GetClients(inbound); err == nil {
		for _, c := range xrayClients {
			clients = append(clients, screenInboundClient{name: c.Email, data: screenClientAction + " " + c.Email, enable: c.Enable})
		}
	}
	page, pages, from, to := screenPageOf(page, len(clients), screenPage)
	route = fmt.Sprintf("%s %d %d", screenInboundAction, id, page)
	var rows [][]telego.InlineKeyboardButton
	for _, c := range clients[from:to] {
		status := "🟢"
		if !c.enable {
			status = "⏸"
		}
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(status+" "+c.name).WithCallbackData(t.encodeQuery(c.data))))
	}
	if pager := screenPager(fmt.Sprintf("%s %d", screenInboundAction, id), page, pages); pager != nil {
		rows = append(rows, pager)
	}
	title := html.EscapeString(fmt.Sprintf("%s (%s)", inbound.Remark, protocolLabel(string(inbound.Protocol))))
	text := t.I18nBot("tgbot.screen.inboundClients", "Inbound=="+title, "Count=="+strconv.Itoa(len(clients)))
	var kb *telego.InlineKeyboardMarkup
	if len(rows) > 0 {
		kb = tu.InlineKeyboard(rows...)
	}
	return screenReply{usersReply: usersReply{text: text, keyboard: kb, route: route}}
}

// --- chain ---------------------------------------------------------------------

// screenChain is /proxy on the screen: without arguments the hops (a view to
// come back to), with them the switch's answer and, when it has to be
// confirmed, its button.
func (t *Tgbot) screenChain(args []string) screenReply {
	text, kb := t.chainProxyCommand(args)
	reply := screenReply{usersReply: usersReply{text: text, keyboard: kb}}
	if len(args) == 0 {
		reply.route = screenChainRoute
	}
	return reply
}
