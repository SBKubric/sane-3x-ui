package service

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The read-only card of a probe account (#183). A probe is monitoring's, not
// a user's: the operator may look at it — traffic, online, last IP, inbound —
// but the bot offers nothing that changes it, and refuses any button that
// would, whoever crafted it. The xray probes are read off the inbound's
// settings and traffic row, the AmneziaWG probe peers off the tunnel table,
// both through monitoring's clients in SubUserService.

// Callback data of the probe card and of monitoring's list of probes.
const (
	probeCardAction = "prb_c" // prb_c <email>
	probeListAction = "prb_l" // prb_l <page>
)

// probeCallback answers a button that addresses a probe account: the card
// and the list, the upstream client card's buttons turned into the card, and
// a refusal for everything else. ok is false for data that does not concern
// a probe, and for the upstream buttons that only show one (its links).
func (t *Tgbot) probeCallback(data string) (reply usersReply, ok bool) {
	action, args, _ := strings.Cut(data, " ")
	email, _, _ := strings.Cut(args, " ")
	switch {
	case action == probeCardAction:
		return t.probeCardReply(email), true
	case action == probeListAction:
		page, _ := strconv.Atoi(args)
		return t.probeList(page), true
	case !IsProbeAccount(email) || probeShowsOnly[action]:
		return usersReply{}, false
	case action == "client_get_usage":
		return t.probeCardReply(email), true
	case action == "client_refresh" || action == "client_cancel":
		return t.probeCardReply(email), true
	}
	// Anything else on a probe changes it, or opens a screen that would:
	// refused, whether or not a card ever offered the button.
	reply = t.probeCardReply(email)
	reply.toast = t.I18nBot("tgbot.probe.readOnly", "Email=="+email)
	return reply, true
}

// probeShowsOnly are the upstream buttons that take a client's email and only
// show something about it; on a probe they stay upstream's.
var probeShowsOnly = map[string]bool{
	"client_sub_links":        true,
	"client_individual_links": true,
	"client_qr_links":         true,
}

// probeCardReply shows the card of the probe named email.
func (t *Tgbot) probeCardReply(email string) usersReply {
	c, err := probeAccount(email)
	if err != nil {
		return t.usersError(err)
	}
	return usersReply{text: t.probeCardText(c), keyboard: t.probeCardKeyboard(c.Name), route: probeCardAction + " " + c.Name}
}

// probeAccount finds monitoring's probe account named email, ignoring case.
func probeAccount(email string) (SubUserClient, error) {
	if !IsProbeAccount(email) {
		return SubUserClient{}, common.NewError("not a probe account:", email)
	}
	for _, c := range monitoringProbes() {
		if strings.EqualFold(c.Name, email) {
			return c, nil
		}
	}
	return SubUserClient{}, common.NewError("no probe account named", email)
}

// monitoringProbes lists monitoring's probe accounts in SubUserService's
// order: the xray probes by inbound, then the tunnel peers.
func monitoringProbes() []SubUserClient {
	v, err := (&SubUserService{}).Get(model.SubUserMonitoringKey)
	if err != nil {
		return nil
	}
	var out []SubUserClient
	for _, c := range v.Clients {
		if IsProbeAccount(c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// probeCardText words the card: what the upstream client card shows about
// traffic and presence, plus where the probe lives and whose it is.
func (t *Tgbot) probeCardText(c SubUserClient) string {
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.probe.card", "Name=="+html.EscapeString(c.Name)))
	b.WriteString(t.I18nBot("tgbot.messages.inbound", "Remark=="+html.EscapeString(fmt.Sprintf("%s (%s)", c.InboundRemark, c.Protocol))))
	b.WriteString(t.I18nBot("tgbot.probe.user", "User=="+model.SubUserMonitoring))
	enabled := t.I18nBot("tgbot.messages.no")
	if c.Enable {
		enabled = t.I18nBot("tgbot.messages.yes")
	}
	b.WriteString(t.I18nBot("tgbot.messages.enabled", "Enable=="+enabled))
	online := probeOnline(c)
	status := t.I18nBot("tgbot.offline")
	if online {
		status = t.I18nBot("tgbot.online")
	}
	b.WriteString(t.I18nBot("tgbot.messages.online", "Status=="+status))
	if !online && c.LastOnline > 0 {
		b.WriteString(t.I18nBot("tgbot.messages.lastOnline", "Time=="+time.UnixMilli(c.LastOnline).Format("2006-01-02 15:04:05")))
	}
	if ip := probeLastIP(c); ip != "" {
		b.WriteString(t.I18nBot("tgbot.probe.lastIp", "IP=="+html.EscapeString(ip)))
	}
	b.WriteString(t.I18nBot("tgbot.messages.upload", "Upload=="+common.FormatTraffic(c.Up)))
	b.WriteString(t.I18nBot("tgbot.messages.download", "Download=="+common.FormatTraffic(c.Down)))
	b.WriteString(t.I18nBot("tgbot.messages.total", "UpDown=="+common.FormatTraffic(c.Up+c.Down), "Total=="+t.usersTraffic(c.TotalGB)))
	b.WriteString(t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05")))
	return b.String()
}

// probeCardKeyboard is all the card offers: refresh (the screen adds the way
// back).
func (t *Tgbot) probeCardKeyboard(email string) *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).
			WithCallbackData(t.encodeQuery(probeCardAction + " " + email))))
}

// probeOnline is the panel's notion of online for every protocol: enabled
// and seen within onlineWindow.
func probeOnline(c SubUserClient) bool {
	return c.Enable && c.LastOnline > time.Now().Add(-onlineWindow).UnixMilli()
}

// probeLastIP is the address the probe was last seen from, "" when unknown:
// the newest entry of an xray client's IP log, a tunnel peer's endpoint.
func probeLastIP(c SubUserClient) string {
	if c.Kind != SubUserClientXray {
		var peer model.TunnelClient
		if err := database.GetDB().Where("uuid = ?", c.Key).First(&peer).Error; err != nil {
			return ""
		}
		return peer.LastIP
	}
	raw, err := (&InboundService{}).GetInboundClientIps(c.Name)
	if err != nil || raw == "" {
		return ""
	}
	var ips []struct {
		IP        string `json:"ip"`
		Timestamp int64  `json:"timestamp"`
	}
	if json.Unmarshal([]byte(raw), &ips) != nil {
		return ""
	}
	last, at := "", int64(-1)
	for _, e := range ips {
		if e.Timestamp >= at {
			last, at = e.IP, e.Timestamp
		}
	}
	return last
}

// probeListRow is the button of monitoring's card that lists its probes; nil
// when it has none.
func (t *Tgbot) probeListRow(v *SubUserView) []telego.InlineKeyboardButton {
	for _, c := range v.Clients {
		if IsProbeAccount(c.Name) {
			return tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.probe.list")).
				WithCallbackData(fmt.Sprintf("%s %d", probeListAction, 0)))
		}
	}
	return nil
}

// probeList is a page of monitoring's probe accounts, each opening its card.
func (t *Tgbot) probeList(page int) usersReply {
	probes := monitoringProbes()
	total := len(probes)
	if total == 0 {
		return t.usersCardReply(model.SubUserMonitoringKey)
	}
	pages := (total + usersRobotPage - 1) / usersRobotPage
	page = max(0, min(page, pages-1))
	from, to := page*usersRobotPage, min((page+1)*usersRobotPage, total)

	var rows [][]telego.InlineKeyboardButton
	for _, c := range probes[from:to] {
		label := fmt.Sprintf("%s · %s", c.Name, c.InboundRemark)
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).
			WithCallbackData(t.encodeQuery(probeCardAction+" "+c.Name))))
	}
	var nav []telego.InlineKeyboardButton
	if page > 0 {
		nav = append(nav, tu.InlineKeyboardButton("⬅️").WithCallbackData(fmt.Sprintf("%s %d", probeListAction, page-1)))
	}
	if page < pages-1 {
		nav = append(nav, tu.InlineKeyboardButton("➡️").WithCallbackData(fmt.Sprintf("%s %d", probeListAction, page+1)))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	return usersReply{text: t.I18nBot("tgbot.probe.listTitle", "From=="+strconv.Itoa(from+1), "To=="+strconv.Itoa(to),
		"Count=="+strconv.Itoa(total)), keyboard: tu.InlineKeyboard(rows...), route: fmt.Sprintf("%s %d", probeListAction, page)}
}

// probeSearch is the Users search for a probe's name: its card, or false
// when no probe account is named so.
func (t *Tgbot) probeSearch(text string) (usersReply, bool) {
	if _, err := probeAccount(text); err != nil {
		return usersReply{}, false
	}
	return t.probeCardReply(text), true
}
