package service

import (
	"fmt"
	"html"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"
)

// The Users screens (#191): the list page by page, the search one field
// wide, and «🔗 Show subscription». The list waits for a search: a text sent
// while it is on the screen looks a user up.

// Callback data of these screens.
const (
	usersListAction  = "usr_l"   // usr_l <page>
	usersFoundAction = "usr_f"   // usr_f <query>: the users a search found
	usersSubAction   = "usr_sub" // usr_sub <key>: the user's subscription
	usersSubQRAction = "usr_sqr" // usr_sqr <key>: its QR, as a file
	usersListPage    = 10        // users per page
)

// usersList is a page of the regular users, one line each, with the way to
// robot and monitoring; the chat then waits for a search.
func (t *Tgbot) usersList(chatId int64, page int) usersReply {
	all, err := (&SubUserService{}).List()
	if err != nil {
		return t.usersError(err)
	}
	var users []*SubUserView
	for _, v := range all {
		if !v.Technical {
			users = append(users, v)
		}
	}
	page, pages, from, to := screenPageOf(page, len(users), usersListPage)
	rows := t.usersLines(users[from:to])
	if pager := screenPager(usersListAction, page, pages); pager != nil {
		rows = append(rows, pager)
	}
	rows = append(rows, tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("🤖 "+model.SubUserRobot).WithCallbackData("usr_c "+model.SubUserRobotKey),
		tu.InlineKeyboardButton("📡 "+model.SubUserMonitoring).WithCallbackData("usr_c "+model.SubUserMonitoringKey),
	))
	userStates.set(chatId, usersStateSearch)
	return usersReply{text: t.I18nBot("tgbot.screen.usersTitle", "Count=="+strconv.Itoa(len(users))),
		keyboard: tu.InlineKeyboard(rows...), route: fmt.Sprintf("%s %d", usersListAction, page)}
}

// usersLines are the users as buttons, each opening its card.
func (t *Tgbot) usersLines(users []*SubUserView) [][]telego.InlineKeyboardButton {
	now := time.Now()
	var rows [][]telego.InlineKeyboardButton
	for _, v := range users {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.usersListLine(v, now)).
			WithCallbackData(t.encodeQuery("usr_c "+v.SubId))))
	}
	return rows
}

// usersListLine is a user in a list: `🟢 ivan · VLESS+AWG · 15.5/100 GB ·
// until 01.11` — on or paused, the protocols of its clients, the traffic
// used of the sum of its limits, and its expiry.
func (t *Tgbot) usersListLine(v *SubUserView, now time.Time) string {
	status := "🟢"
	if !v.Enable {
		status = "⏸"
	}
	return t.I18nBot("tgbot.screen.userLine", "Status=="+status, "Name=="+v.Name,
		"Protocols=="+usersProtocols(v), "Traffic=="+t.usersTrafficShort(v.Up+v.Down, v.Total), "Exp=="+t.usersExpiryShort(v.ExpiryTime, now))
}

// usersProtocols are the protocols of the user's clients, in the order they
// first appear: VLESS+AWG; "—" without clients.
func usersProtocols(v *SubUserView) string {
	var out []string
	for _, c := range v.Clients {
		if p := protocolLabel(c.Protocol); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return "—"
	}
	return strings.Join(out, "+")
}

// usersGB words bytes in GB to a tenth: 15.5, 48, 0.2.
func usersGB(bytes int64) string {
	return strconv.FormatFloat(math.Round(float64(bytes)/(1<<30)*10)/10, 'f', -1, 64)
}

// usersTrafficShort is the traffic used of the limit: 15.5/100 GB, or
// 0.2/∞ without a limit.
func (t *Tgbot) usersTrafficShort(used, total int64) string {
	if total == 0 {
		return usersGB(used) + "/∞"
	}
	return usersGB(used) + "/" + usersGB(total) + " " + t.I18nBot("tgbot.screen.gb")
}

// usersExpiryShort is an expiry as a list line has it: the day and month (and
// the year when it is not this one), +N days when it counts from first use,
// "—" without one.
func (t *Tgbot) usersExpiryShort(ms int64, now time.Time) string {
	switch {
	case ms == 0:
		return "—"
	case ms < 0:
		return t.I18nBot("tgbot.screen.afterFirstUse", "Days=="+strconv.FormatInt(ms/-86400000, 10))
	}
	at := time.UnixMilli(ms)
	if at.Year() != now.Year() {
		return at.Format("02.01.2006")
	}
	return at.Format("02.01")
}

// usersSubscriptionLine is the card's summary of the subscription: on or
// paused, until when, the traffic.
func (t *Tgbot) usersSubscriptionLine(v *SubUserView) string {
	status := t.I18nBot("tgbot.screen.subActive")
	if !v.Enable {
		status = t.I18nBot("tgbot.screen.subPaused")
	}
	return t.I18nBot("tgbot.screen.subStatus", "Status=="+status, "Exp=="+t.usersExpiryShort(v.ExpiryTime, time.Now()),
		"Traffic=="+t.usersTrafficShort(v.Up+v.Down, v.Total))
}

// --- search --------------------------------------------------------------------

// usersSearchReply looks up the users a text names: one opens its card,
// several are listed, none is said so. Unless a card opens, the chat waits
// for another search.
func (t *Tgbot) usersSearchReply(chatId int64, query string) usersReply {
	query = strings.TrimSpace(query)
	users, err := (&SubUserService{}).Search(query)
	if err != nil {
		userStates.set(chatId, usersStateSearch)
		return t.usersError(err)
	}
	switch len(users) {
	case 0:
		userStates.set(chatId, usersStateSearch)
		return usersReply{text: t.I18nBot("tgbot.screen.notFound", "Query=="+html.EscapeString(query))}
	case 1:
		return t.usersCardReply(users[0].SubId)
	}
	return t.usersFound(chatId, query)
}

// usersFound lists the users a search found.
func (t *Tgbot) usersFound(chatId int64, query string) usersReply {
	users, err := (&SubUserService{}).Search(query)
	if err != nil {
		return t.usersError(err)
	}
	userStates.set(chatId, usersStateSearch)
	var kb *telego.InlineKeyboardMarkup
	if rows := t.usersLines(users); len(rows) > 0 {
		kb = tu.InlineKeyboard(rows...)
	}
	return usersReply{text: t.I18nBot("tgbot.screen.found", "Query=="+html.EscapeString(query), "Count=="+strconv.Itoa(len(users))),
		keyboard: kb, route: usersFoundAction + " " + query}
}

// --- subscription --------------------------------------------------------------

// usersSubscription is «🔗 Show subscription»: the user's links and status.
func (t *Tgbot) usersSubscription(key string) usersReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return t.usersError(err)
	}
	if v.Technical {
		return t.usersCardReply(key)
	}
	subURL, jsonURL := t.subscriptionURLs(v.SubId)
	text := t.I18nBot("tgbot.screen.subTitle", "Name=="+html.EscapeString(v.Name)) +
		"<code>" + html.EscapeString(subURL) + "</code>\r\n"
	if jsonURL != "" {
		text += "JSON: <code>" + html.EscapeString(jsonURL) + "</code>\r\n"
	}
	text += t.usersSubscriptionLine(v)
	kb := tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.screen.subQR")).
		WithCallbackData(t.encodeQuery(usersSubQRAction + " " + v.SubId))))
	return usersReply{text: text, keyboard: kb, route: usersSubAction + " " + v.SubId}
}

// usersSubscriptionQR sends the QR of the user's subscription link as a file.
func (t *Tgbot) usersSubscriptionQR(key string) screenReply {
	v, err := (&SubUserService{}).Get(key)
	if err != nil || v.Technical {
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}
	}
	subURL, _ := t.subscriptionURLs(v.SubId)
	png, err := qrcode.Encode(subURL, qrcode.Medium, 320)
	if err != nil {
		return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.errorOperation")}}
	}
	return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation")},
		files: []tunnelFile{{name: v.Name + ".png", data: png}}}
}
