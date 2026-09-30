package service

import (
	"html"
	"net"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// «⚙️ Server» (#192): the server's status (the old usage and /status) with
// the backup and the ban logs as files, «🔄 Restart xray» and «♻️ Reset all
// traffic» behind a confirmation, «🔗 Chain» (/proxy) and «📣 Notification
// channel» (#202, tgbot_screen_notify.go). «🔧 Admin panel»
// of the main menu sends the panel's address as a message of its own that
// goes after five minutes.

// screenAdminLinkTTL is how long the admin link stays in the chat, in
// seconds.
const screenAdminLinkTTL = 5 * 60

// screenRestartXray restarts xray: false when it is not running. A test
// seam; xray itself never runs in a test.
var screenRestartXray = func(t *Tgbot) (bool, error) {
	if !t.xrayService.IsXrayRunning() {
		return false, nil
	}
	return true, t.xrayService.RestartXray(true)
}

// screenServer is the server's status with its actions.
func (t *Tgbot) screenServer() screenReply {
	button := func(label, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(data)
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.screen.backup"), screenBackupData),
			button(t.I18nBot("tgbot.ops.restartXray"), screenRestartAsk)),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.screen.chain"), screenChainRoute),
			button(t.I18nBot("tgbot.screen.banLogs"), screenBanLogsData)),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.ops.resetAll"), screenResetAllAsk)),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.notify.button"), screenNotifyRoute)), // #202
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.buttons.refresh"), screenServerRoute)),
	)
	return screenReply{usersReply: usersReply{text: t.prepareServerUsageInfo(), keyboard: kb, route: screenServerRoute}}
}

// screenConfirm asks before an action: text, and «✅ Confirm» that runs
// data. Back leads to the view it covers.
func (t *Tgbot) screenConfirm(text, data string) screenReply {
	return screenReply{usersReply: usersReply{text: text, keyboard: tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.users.confirm")).WithCallbackData(data)))}}
}

// screenRestartConfirm asks before xray restarts.
func (t *Tgbot) screenRestartConfirm() screenReply {
	return t.screenConfirm(t.I18nBot("tgbot.ops.restartAsk"), screenRestartData)
}

// screenServerSaying is the server screen with a line on top: how an action
// went.
func (t *Tgbot) screenServerSaying(result string) screenReply {
	reply := t.screenServer()
	reply.text = result + "\r\n\r\n" + reply.text
	return reply
}

// screenRestart restarts xray and says how it went.
func (t *Tgbot) screenRestart() screenReply {
	running, err := screenRestartXray(t)
	switch {
	case !running:
		return t.screenServerSaying(t.I18nBot("tgbot.commands.xrayNotRunning"))
	case err != nil:
		return t.screenServerSaying(t.I18nBot("tgbot.commands.restartFailed", "Error=="+html.EscapeString(err.Error())))
	}
	return t.screenServerSaying(t.I18nBot("tgbot.ops.restarted"))
}

// screenResetAll zeroes the traffic of every user's clients, robot's
// included, xray and tunnel alike; monitoring's probes keep theirs.
func (t *Tgbot) screenResetAll() screenReply {
	users, err := reportUsers()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	done := 0
	var failed []string
	for _, v := range users {
		for _, c := range v.Clients {
			var err error
			if c.Kind == SubUserClientXray {
				err = t.inboundService.ResetClientTrafficByEmail(c.Name)
			} else {
				err = tunnelClientsOf(c.Kind).ResetClientTrafficByUUID(c.Key)
			}
			if err != nil {
				failed = append(failed, "❗ "+html.EscapeString(c.Name)+": "+html.EscapeString(err.Error()))
				continue
			}
			done++
		}
	}
	result := t.I18nBot("tgbot.ops.resetDone", "Count=="+strconv.Itoa(done))
	if len(failed) > 0 {
		result += "\r\n" + strings.Join(failed, "\r\n")
	}
	return t.screenServerSaying(result)
}

// screenAdminLink sends the panel's address in a message of its own, deleted
// after five minutes; the screen stays as it is.
func (t *Tgbot) screenAdminLink() screenReply {
	return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.ops.adminLinkSent")},
		after: func(chatId int64) {
			t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.ops.adminLink", "URL=="+t.panelURL()), screenAdminLinkTTL)
		}}
}

// panelURL is the panel's address as the panel knows it: its domain, else
// the address it listens on, else the server's public IPv4 (as the last
// status found it), else the host name; https when it has a certificate;
// the port unless it is the scheme's own; and the base path.
func (t *Tgbot) panelURL() string {
	host, _ := t.settingService.GetWebDomain()
	if host == "" {
		if listen, _ := t.settingService.GetListen(); listen != "" && !net.ParseIP(listen).IsUnspecified() {
			host = listen
		}
	}
	if host == "" && t.lastStatus != nil {
		if ip := t.lastStatus.PublicIP.IPv4; net.ParseIP(ip) != nil {
			host = ip
		}
	}
	if host == "" {
		host = monFirstNotEmpty(hostname, "localhost")
	}
	scheme := "http"
	cert, _ := t.settingService.GetCertFile()
	key, _ := t.settingService.GetKeyFile()
	if cert != "" && key != "" {
		scheme = "https"
	}
	port, _ := t.settingService.GetPort()
	if (scheme == "https" && port == 443) || (scheme == "http" && port == 80) {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	} else {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	base, _ := t.settingService.GetBasePath()
	return scheme + "://" + host + base
}
