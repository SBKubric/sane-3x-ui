package service

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The xray client's card on the screen (#191): the upstream card and its
// actions — traffic reset and limit, expiry, IP limit, IP log and clearing,
// Telegram user, enable — moved out of answerCallback onto the screen, plus
// the way to the client's user and its links. Buttons address the client by
// its email; a probe's never reach here (probeCallback answers them first).

// clientCardCallback runs a button of the client card; false for data that
// is not the card's.
func (t *Tgbot) clientCardCallback(data string) (screenReply, bool) {
	fields := strings.Split(data, " ")
	if len(fields) < 2 || fields[1] == "" {
		return screenReply{}, false
	}
	action, email := fields[0], fields[1]
	// arg is the n-th number after the email, ok when it is there and parses.
	arg := func(n int) (int, bool) {
		if len(fields) <= 1+n {
			return 0, false
		}
		v, err := strconv.Atoi(fields[1+n])
		return v, err == nil
	}
	keyboard := func(kb *telego.InlineKeyboardMarkup) (screenReply, bool) {
		return screenReply{usersReply: usersReply{keyboard: kb}}, true
	}
	switch action {
	case screenClientAction:
		return t.clientCard(email, ""), true
	case "client_refresh":
		return t.clientCard(email, t.I18nBot("tgbot.answers.clientRefreshSuccess", "Email=="+email)), true
	case "client_cancel":
		return t.clientCard(email, t.I18nBot("tgbot.answers.canceled", "Email=="+email)), true
	case "client_links":
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation")},
			after: func(chatId int64) {
				t.sendClientIndividualLinks(chatId, email)
				t.sendClientQRLinks(chatId, email)
			}}, true

	case "reset_traffic":
		return keyboard(t.clientConfirm("tgbot.buttons.cancelReset", "client_cancel "+email,
			"tgbot.buttons.confirmResetTraffic", "reset_traffic_c "+email))
	case "reset_traffic_c":
		if err := t.inboundService.ResetClientTrafficByEmail(email); err != nil {
			return t.clientFailed(email), true
		}
		return t.clientCard(email, t.I18nBot("tgbot.answers.resetTrafficSuccess", "Email=="+email)), true

	case "limit_traffic":
		return keyboard(t.clientLimitTrafficKeyboard(email))
	case "limit_traffic_c":
		gb, ok := arg(1)
		if !ok {
			return t.clientFailed(email), true
		}
		needRestart, err := t.inboundService.ResetClientTrafficLimitByEmail(email, gb)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			return t.clientFailed(email), true
		}
		return t.clientCard(email, t.I18nBot("tgbot.answers.setTrafficLimitSuccess", "Email=="+email)), true
	case "limit_traffic_in":
		return t.clientKeypad(fields, "limit_traffic_in", "tgbot.buttons.confirmNumberAdd", "limit_traffic_c"), true

	case "reset_exp":
		return keyboard(t.clientExpiryKeyboard(email))
	case "reset_exp_c":
		days, ok := arg(1)
		if !ok {
			return t.clientFailed(email), true
		}
		return t.clientResetExpiry(email, int64(days)), true
	case "reset_exp_in":
		return t.clientKeypad(fields, "reset_exp_in", "tgbot.buttons.confirmNumber", "reset_exp_c"), true

	case "ip_limit":
		return keyboard(t.clientIPLimitKeyboard(email))
	case "ip_limit_c":
		count, ok := arg(1)
		if !ok {
			return t.clientFailed(email), true
		}
		needRestart, err := t.inboundService.ResetClientIpLimitByEmail(email, count)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			return t.clientFailed(email), true
		}
		return t.clientCard(email, t.I18nBot("tgbot.answers.resetIpSuccess", "Email=="+email, "Count=="+strconv.Itoa(count))), true
	case "ip_limit_in":
		return t.clientKeypad(fields, "ip_limit_in", "tgbot.buttons.confirmNumber", "ip_limit_c"), true

	case "ip_log":
		return t.clientIPs(email, t.I18nBot("tgbot.answers.getIpLog", "Email=="+email)), true
	case "ips_refresh":
		return t.clientIPs(email, t.I18nBot("tgbot.answers.IpRefreshSuccess", "Email=="+email)), true
	case "ips_cancel":
		return t.clientIPs(email, t.I18nBot("tgbot.answers.canceled", "Email=="+email)), true
	case "clear_ips":
		return keyboard(t.clientConfirm("tgbot.buttons.cancel", "ips_cancel "+email,
			"tgbot.buttons.confirmClearIps", "clear_ips_c "+email))
	case "clear_ips_c":
		if err := t.inboundService.ClearClientIps(email); err != nil {
			return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}, true
		}
		return t.clientIPs(email, t.I18nBot("tgbot.answers.clearIpSuccess", "Email=="+email)), true

	case "tg_user":
		reply := t.clientTelegramUser(email, t.I18nBot("tgbot.answers.getUserInfo", "Email=="+email))
		if reply.route != "" {
			reply.after = func(chatId int64) { t.clientAskTelegramUser(chatId, email) }
		}
		return reply, true
	case "tgid_refresh":
		return t.clientTelegramUser(email, t.I18nBot("tgbot.answers.TGIdRefreshSuccess", "Email=="+email)), true
	case "tgid_cancel":
		return t.clientTelegramUser(email, t.I18nBot("tgbot.answers.canceled", "Email=="+email)), true
	case "tgid_remove":
		return keyboard(t.clientConfirm("tgbot.buttons.cancel", "tgid_cancel "+email,
			"tgbot.buttons.confirmRemoveTGUser", "tgid_remove_c "+email))
	case "tgid_remove_c":
		traffic, err := t.inboundService.GetClientTrafficByEmail(email)
		if err != nil || traffic == nil {
			return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}, true
		}
		needRestart, err := t.inboundService.SetClientTelegramUserID(traffic.Id, EmptyTelegramUserID)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}, true
		}
		return t.clientTelegramUser(email, t.I18nBot("tgbot.answers.removedTGUserSuccess", "Email=="+email)), true

	case "toggle_enable":
		return keyboard(t.clientConfirm("tgbot.buttons.cancel", "client_cancel "+email,
			"tgbot.buttons.confirmToggle", "toggle_enable_c "+email))
	case "toggle_enable_c":
		enabled, needRestart, err := t.inboundService.ToggleClientEnableByEmail(email)
		if needRestart {
			t.xrayService.SetToNeedRestart()
		}
		if err != nil {
			return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}, true
		}
		toast := t.I18nBot("tgbot.answers.disableSuccess", "Email=="+email)
		if enabled {
			toast = t.I18nBot("tgbot.answers.enableSuccess", "Email=="+email)
		}
		return t.clientCard(email, toast), true
	}
	return screenReply{}, false
}

// clientCard is the xray client's card: the upstream text and actions, its
// links, and its user.
func (t *Tgbot) clientCard(email, toast string) screenReply {
	traffic, err := t.inboundService.GetClientTrafficByEmail(email)
	if err != nil {
		logger.Warning(err)
		return screenReply{usersReply: usersReply{toast: toast, text: t.I18nBot("tgbot.wentWrong")}}
	}
	if traffic == nil {
		return screenReply{usersReply: usersReply{toast: toast, text: t.I18nBot("tgbot.noResult")}}
	}
	button := func(key, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(t.I18nBot(key)).WithCallbackData(t.encodeQuery(data))
	}
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(button("tgbot.buttons.refresh", "client_refresh "+email)),
		tu.InlineKeyboardRow(button("tgbot.buttons.resetTraffic", "reset_traffic "+email), button("tgbot.buttons.limitTraffic", "limit_traffic "+email)),
		tu.InlineKeyboardRow(button("tgbot.buttons.resetExpire", "reset_exp "+email)),
		tu.InlineKeyboardRow(button("tgbot.buttons.ipLog", "ip_log "+email), button("tgbot.buttons.ipLimit", "ip_limit "+email)),
		tu.InlineKeyboardRow(button("tgbot.buttons.setTGUser", "tg_user "+email)),
		tu.InlineKeyboardRow(button("tgbot.buttons.toggle", "toggle_enable "+email)),
		tu.InlineKeyboardRow(button("tgbot.screen.clientLinks", "client_links "+email)),
	}
	if owner, err := (&SubUserService{}).Find(email); err == nil {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("👤 "+owner.Name).
			WithCallbackData(t.encodeQuery("usr_c "+owner.SubId))))
	}
	return screenReply{usersReply: usersReply{toast: toast, text: t.clientInfoMsg(traffic, true, true, true, true, true, true),
		keyboard: tu.InlineKeyboard(rows...), route: screenClientAction + " " + email}}
}

// clientFailed answers a change that did not go through: the error toast on
// the card as it is.
func (t *Tgbot) clientFailed(email string) screenReply {
	return t.clientCard(email, t.I18nBot("tgbot.answers.errorOperation"))
}

// clientConfirm is a confirmation in place of the card's buttons.
func (t *Tgbot) clientConfirm(cancelKey, cancelData, confirmKey, confirmData string) *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot(cancelKey)).WithCallbackData(t.encodeQuery(cancelData))),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot(confirmKey)).WithCallbackData(t.encodeQuery(confirmData))),
	)
}

// clientPresets is a preset keyboard: cancel, unlimited and custom, then the
// rows of values, each labelled by label and set with "<set> <email> <n>".
func (t *Tgbot) clientPresets(email, cancelKey, set, pad string, rows [][]int, label func(int) string) *telego.InlineKeyboardMarkup {
	return t.numberPresets(email, cancelKey, "client_cancel "+email, set, pad, rows, label)
}

// numberPresets is a preset keyboard for the client named id (an xray email or
// a tunnel uuid): cancel (cancelData), unlimited and custom, then the presets.
// A preset sends "<set> <id> <n>", custom opens the keypad "<pad> <id> 0".
func (t *Tgbot) numberPresets(id, cancelKey, cancelData, set, pad string, rows [][]int, label func(int) string) *telego.InlineKeyboardMarkup {
	at := func(text string, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(text).WithCallbackData(t.encodeQuery(data))
	}
	out := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(at(t.I18nBot(cancelKey), cancelData)),
		tu.InlineKeyboardRow(at(t.I18nBot("tgbot.unlimited"), set+" "+id+" 0"), at(t.I18nBot("tgbot.buttons.custom"), pad+" "+id+" 0")),
	}
	for _, row := range rows {
		var buttons []telego.InlineKeyboardButton
		for _, n := range row {
			buttons = append(buttons, at(label(n), fmt.Sprintf("%s %s %d", set, id, n)))
		}
		out = append(out, buttons)
	}
	return tu.InlineKeyboard(out...)
}

func (t *Tgbot) clientLimitTrafficKeyboard(email string) *telego.InlineKeyboardMarkup {
	return t.clientPresets(email, "tgbot.buttons.cancel", "limit_traffic_c", "limit_traffic_in",
		[][]int{{1, 5, 10}, {20, 30, 40}, {50, 60, 80}, {100, 150, 200}},
		func(n int) string { return fmt.Sprintf("%d GB", n) })
}

func (t *Tgbot) clientExpiryKeyboard(email string) *telego.InlineKeyboardMarkup {
	return t.clientPresets(email, "tgbot.buttons.cancelReset", "reset_exp_c", "reset_exp_in", expiryPresetDays, t.expiryPresetLabel)
}

// expiryPresetDays are the day presets of the expiry keyboards.
var expiryPresetDays = [][]int{{7, 10}, {14, 20}, {30, 90}, {180, 365}}

// expiryPresetLabel names an expiry preset: "Add 1 Month", "Add 3 Months", "Add 7 Days".
func (t *Tgbot) expiryPresetLabel(n int) string {
	switch {
	case n == 30:
		return t.I18nBot("tgbot.add") + " 1 " + t.I18nBot("tgbot.month")
	case n >= 90:
		return t.I18nBot("tgbot.add") + " " + strconv.Itoa(n/30) + " " + t.I18nBot("tgbot.months")
	}
	return t.I18nBot("tgbot.add") + " " + strconv.Itoa(n) + " " + t.I18nBot("tgbot.days")
}

func (t *Tgbot) clientIPLimitKeyboard(email string) *telego.InlineKeyboardMarkup {
	return t.clientPresets(email, "tgbot.buttons.cancelIpLimit", "ip_limit_c", "ip_limit_in",
		[][]int{{1, 2}, {3, 4}, {5, 6, 7}, {8, 9, 10}}, strconv.Itoa)
}

// clientKeypad is a key of a number keypad: fields are "<pad> <email> <n>
// [key]", the key a digit, -1 to delete one, -2 to clear. The keypad's
// confirm button sets the number with "<set> <email> <n>".
func (t *Tgbot) clientKeypad(fields []string, pad, confirmKey, set string) screenReply {
	email := fields[1]
	return t.numberKeypad(fields, pad, confirmKey, set, "client_cancel "+email, t.clientFailed(email))
}

// numberKeypad is a key of a number keypad for the client named fields[1]
// (an xray email or a tunnel uuid): fields are "<pad> <id> <n> [key]", the key
// a digit, -1 to delete one, -2 to clear; confirm sends "<set> <id> <n>",
// cancel sends cancelData, and malformed fields answer failed.
func (t *Tgbot) numberKeypad(fields []string, pad, confirmKey, set, cancelData string, failed screenReply) screenReply {
	email := fields[1]
	if len(fields) < 3 {
		return failed
	}
	n, err := strconv.Atoi(fields[2])
	if err != nil {
		return failed
	}
	if len(fields) == 4 {
		next := n
		if key, err := strconv.Atoi(fields[3]); err == nil {
			switch key {
			case -2:
				next = 0
			case -1:
				next = n / 10
			default:
				next = n*10 + key
			}
		}
		if next == n {
			return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation")}}
		}
		if next >= 999999 {
			return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}
		}
		n = next
	}
	num := strconv.Itoa(n)
	key := func(label string, k int) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(t.encodeQuery(fmt.Sprintf("%s %s %d %d", pad, email, n, k)))
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery(cancelData))),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot(confirmKey, "Num=="+num)).WithCallbackData(t.encodeQuery(set+" "+email+" "+num))),
		tu.InlineKeyboardRow(key("1", 1), key("2", 2), key("3", 3)),
		tu.InlineKeyboardRow(key("4", 4), key("5", 5), key("6", 6)),
		tu.InlineKeyboardRow(key("7", 7), key("8", 8), key("9", 9)),
		tu.InlineKeyboardRow(key("🔄", -2), key("0", 0), key("⬅️", -1)),
	)
	return screenReply{usersReply: usersReply{keyboard: kb}}
}

// extendedExpiry is an expiry (ms; 0 unlimited, negative N days from first
// use) after adding days, as the client cards set it: from the expiry date
// while that is ahead, N days from first use once it has passed or when it
// already counts from first use; 0 days makes it unlimited.
func extendedExpiry(expiry, days, nowMs int64) int64 {
	if days <= 0 {
		return 0
	}
	span := days * 24 * 60 * 60000
	switch {
	case expiry > 0 && expiry < nowMs:
		return -span
	case expiry > 0:
		return expiry + span
	}
	return expiry - span
}

// clientResetExpiry adds days to the client's expiry: from its expiry date
// while that is ahead, from first use when it has passed or counts from
// first use already; 0 makes it unlimited.
func (t *Tgbot) clientResetExpiry(email string, days int64) screenReply {
	var date int64
	if days > 0 {
		traffic, err := t.inboundService.GetClientTrafficByEmail(email)
		if err != nil {
			logger.Warning(err)
			return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.wentWrong")}}
		}
		if traffic == nil {
			return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.noResult")}}
		}
		date = extendedExpiry(traffic.ExpiryTime, days, time.Now().UnixMilli())
	}
	needRestart, err := t.inboundService.ResetClientExpiryTimeByEmail(email, date)
	if needRestart {
		t.xrayService.SetToNeedRestart()
	}
	if err != nil {
		return t.clientFailed(email)
	}
	return t.clientCard(email, t.I18nBot("tgbot.answers.expireResetSuccess", "Email=="+email))
}

// clientIPs is the client's IP log, with refresh and clearing.
func (t *Tgbot) clientIPs(email, toast string) screenReply {
	ips, err := t.inboundService.GetInboundClientIps(email)
	if err != nil || len(ips) == 0 {
		ips = t.I18nBot("tgbot.noIpRecord")
	}
	formattedIps := ips
	if err == nil && len(ips) > 0 {
		type ipWithTimestamp struct {
			IP        string `json:"ip"`
			Timestamp int64  `json:"timestamp"`
		}
		var ipsWithTime []ipWithTimestamp
		if json.Unmarshal([]byte(ips), &ipsWithTime) == nil && len(ipsWithTime) > 0 {
			lines := make([]string, 0, len(ipsWithTime))
			for _, item := range ipsWithTime {
				if item.IP == "" {
					continue
				}
				if item.Timestamp > 0 {
					lines = append(lines, fmt.Sprintf("%s (%s)", item.IP, time.Unix(item.Timestamp, 0).Format("2006-01-02 15:04:05")))
					continue
				}
				lines = append(lines, item.IP)
			}
			if len(lines) > 0 {
				formattedIps = strings.Join(lines, "\n")
			}
		} else {
			var oldIps []string
			if json.Unmarshal([]byte(ips), &oldIps) == nil && len(oldIps) > 0 {
				formattedIps = strings.Join(oldIps, "\n")
			}
		}
	}
	text := t.I18nBot("tgbot.messages.email", "Email=="+email) +
		t.I18nBot("tgbot.messages.ips", "IPs=="+html.EscapeString(formattedIps)) +
		t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("ips_refresh "+email))),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.clearIPs")).WithCallbackData(t.encodeQuery("clear_ips "+email))),
	)
	return screenReply{usersReply: usersReply{toast: toast, text: text, keyboard: kb, route: "ip_log " + email}}
}

// clientTelegramUser is the client's Telegram user, with refresh and removal.
func (t *Tgbot) clientTelegramUser(email, toast string) screenReply {
	_, client, err := t.inboundService.GetClientByEmail(email)
	if err != nil {
		logger.Warning(err)
		return screenReply{usersReply: usersReply{toast: toast, text: t.I18nBot("tgbot.wentWrong")}}
	}
	if client == nil {
		return screenReply{usersReply: usersReply{toast: toast, text: t.I18nBot("tgbot.noResult")}}
	}
	tgId := "None"
	if client.TgID != 0 {
		tgId = strconv.FormatInt(client.TgID, 10)
	}
	text := t.I18nBot("tgbot.messages.email", "Email=="+email) +
		t.I18nBot("tgbot.messages.TGUser", "TelegramID=="+tgId) +
		t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("tgid_refresh "+email))),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.removeTGUser")).WithCallbackData(t.encodeQuery("tgid_remove "+email))),
	)
	return screenReply{usersReply: usersReply{toast: toast, text: text, keyboard: kb, route: "tg_user " + email}}
}

// clientAskTelegramUser sends the keyboard that picks a Telegram user for the
// client: Telegram's user picker lives in a reply keyboard, which only a
// message of its own can carry. The pick comes back as UsersShared.
func (t *Tgbot) clientAskTelegramUser(chatId int64, email string) {
	traffic, err := t.inboundService.GetClientTrafficByEmail(email)
	if err != nil || traffic == nil {
		return
	}
	requestUser := telego.KeyboardButtonRequestUsers{RequestID: int32(traffic.Id), UserIsBot: new(bool)}
	keyboard := tu.Keyboard(
		tu.KeyboardRow(tu.KeyboardButton(t.I18nBot("tgbot.buttons.selectTGUser")).WithRequestUsers(&requestUser)),
		tu.KeyboardRow(tu.KeyboardButton(t.I18nBot("tgbot.buttons.closeKeyboard"))),
	).WithIsPersistent().WithResizeKeyboard()
	t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.buttons.selectOneTGUser"), keyboard)
}
