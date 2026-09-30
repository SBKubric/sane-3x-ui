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
)

// «📊 Reports» (#192): the users' traffic, most first (the upstream sorted
// traffic report, per user now), and who runs out soon (upstream's
// deplete_soon, per user, each opening the user's card). A user is the
// subscription the operator deals with, so both count users, robot among
// them for the clients nobody owns; monitoring's probes are left out.

// reportUsers are the users the reports count: all but monitoring.
func reportUsers() ([]*SubUserView, error) {
	all, err := (&SubUserService{}).List()
	if err != nil {
		return nil, err
	}
	users := make([]*SubUserView, 0, len(all))
	for _, v := range all {
		if v.SubId != model.SubUserMonitoringKey {
			users = append(users, v)
		}
	}
	return users, nil
}

// byTraffic sorts users by the traffic used, most first; a tie by name.
func byTraffic(users []*SubUserView) {
	slices.SortStableFunc(users, func(a, b *SubUserView) int {
		if d := (b.Up + b.Down) - (a.Up + a.Down); d != 0 {
			if d > 0 {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
}

// screenReports is the reports' screen: the traffic of all users, the top
// three, and the two reports.
func (t *Tgbot) screenReports() screenReply {
	users, err := reportUsers()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	var total int64
	for _, v := range users {
		total += v.Up + v.Down
	}
	byTraffic(users)
	var top []string
	for _, v := range users[:min(3, len(users))] {
		top = append(top, html.EscapeString(v.Name)+" "+usersGB(v.Up+v.Down))
	}
	if len(top) == 0 {
		top = []string{"—"}
	}
	running := len(t.runningOut(users))
	button := func(label, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(data)
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.ops.trafficButton"), screenTrafficRoute+" 0")),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.ops.runningOutButton", "Count=="+strconv.Itoa(running)), screenRunningOutRoute+" 0")),
	)
	text := t.I18nBot("tgbot.ops.reportsTitle", "Total=="+usersGB(total), "Top=="+strings.Join(top, " · "))
	return screenReply{usersReply: usersReply{text: text, keyboard: kb, route: screenReportsRoute}}
}

// screenTraffic is a page of the users by traffic, most first.
func (t *Tgbot) screenTraffic(page int) screenReply {
	users, err := reportUsers()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	byTraffic(users)
	page, pages, from, to := screenPageOf(page, len(users), screenPage)
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.ops.trafficTitle", "Count=="+strconv.Itoa(len(users))))
	for i, v := range users[from:to] {
		fmt.Fprintf(&b, "\n%d. %s %s", from+i+1, html.EscapeString(v.Name), t.usersTrafficShort(v.Up+v.Down, v.Total))
	}
	var kb *telego.InlineKeyboardMarkup
	if pager := screenPager(screenTrafficRoute, page, pages); pager != nil {
		kb = tu.InlineKeyboard(pager)
	}
	return screenReply{usersReply: usersReply{text: b.String(), keyboard: kb, route: fmt.Sprintf("%s %d", screenTrafficRoute, page)}}
}

// runningOutUser is a user that runs out soon: the worst of its enabled
// clients by traffic (percent used, -1 when none is close) and by date (days
// left, -1 when none is close).
type runningOutUser struct {
	user    *SubUserView
	percent int
	days    int
}

// runningOut are the users with an enabled client that runs out soon, in
// the users' order, as upstream's deplete_soon counts it: less traffic left
// than the trafficDiff setting (GB), or fewer days left than expireDiff.
func (t *Tgbot) runningOut(users []*SubUserView) []runningOutUser {
	var trafficDiff, expireDiff int64
	if gb, err := t.settingService.GetTrafficDiff(); err == nil && gb > 0 {
		trafficDiff = int64(gb) << 30
	}
	if days, err := t.settingService.GetExpireDiff(); err == nil && days > 0 {
		expireDiff = int64(days) * 86400000
	}
	now := time.Now().UnixMilli()
	var out []runningOutUser
	for _, v := range users {
		r := runningOutUser{user: v, percent: -1, days: -1}
		for _, c := range v.Clients {
			if !c.Enable {
				continue
			}
			if used := c.Up + c.Down; c.TotalGB > 0 && c.TotalGB-used < trafficDiff {
				r.percent = max(r.percent, int(math.Floor(float64(used)*100/float64(c.TotalGB))))
			}
			if left := c.ExpiryTime - now; c.ExpiryTime > 0 && left < expireDiff {
				days := int(math.Ceil(float64(left) / 86400000))
				if r.days < 0 || days < r.days {
					r.days = max(days, 0)
				}
			}
		}
		if r.percent >= 0 || r.days >= 0 {
			out = append(out, r)
		}
	}
	return out
}

// screenRunningOut is a page of the users that run out soon, each opening
// its card.
func (t *Tgbot) screenRunningOut(page int) screenReply {
	users, err := reportUsers()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	running := t.runningOut(users)
	route := fmt.Sprintf("%s %d", screenRunningOutRoute, page)
	if len(running) == 0 {
		return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.ops.runningOutNone"), route: route}}
	}
	page, pages, from, to := screenPageOf(page, len(running), screenPage)
	route = fmt.Sprintf("%s %d", screenRunningOutRoute, page)
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.ops.runningOutTitle", "Count=="+strconv.Itoa(len(running))))
	var rows [][]telego.InlineKeyboardButton
	for _, r := range running[from:to] {
		var why []string
		if r.percent >= 0 {
			why = append(why, t.I18nBot("tgbot.ops.runningOutTraffic", "Percent=="+strconv.Itoa(r.percent)))
		}
		if r.days >= 0 {
			why = append(why, t.I18nBot("tgbot.ops.runningOutDays", "Days=="+strconv.Itoa(r.days)))
		}
		fmt.Fprintf(&b, "\n• %s — %s", html.EscapeString(r.user.Name), strings.Join(why, ", "))
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("⏳ "+r.user.Name).
			WithCallbackData(t.encodeQuery("usr_c "+r.user.SubId))))
	}
	if pager := screenPager(screenRunningOutRoute, page, pages); pager != nil {
		rows = append(rows, pager)
	}
	return screenReply{usersReply: usersReply{text: b.String(), keyboard: tu.InlineKeyboard(rows...), route: route}}
}
