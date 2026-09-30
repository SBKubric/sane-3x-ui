package service

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// «🟢 Online» (#192): the users with a client online, each opening the
// user's card. Online is what the panel shows: a client seen within
// onlineWindow, xray and tunnel alike (InboundService.GetOnlineClients),
// probes left out. A client that belongs to nobody (robot's) is listed on
// its own and opens its own card.

// screenOnlineLine is a line of the online list.
type screenOnlineLine struct {
	label, data string
}

// screenOnline is a page of the online users.
func (t *Tgbot) screenOnline(page int) screenReply {
	lines, err := t.onlineLines()
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	page, pages, from, to := screenPageOf(page, len(lines), screenPage)
	var rows [][]telego.InlineKeyboardButton
	for _, l := range lines[from:to] {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(l.label).WithCallbackData(t.encodeQuery(l.data))))
	}
	if pager := screenPager(screenOnlineRoute, page, pages); pager != nil {
		rows = append(rows, pager)
	}
	route := fmt.Sprintf("%s %d", screenOnlineRoute, page)
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(route)))
	return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.ops.onlineTitle", "Count=="+strconv.Itoa(len(lines))),
		keyboard: tu.InlineKeyboard(rows...), route: route}}
}

// onlineLines are the users online, in the users' order, then robot's
// clients online.
func (t *Tgbot) onlineLines() ([]screenOnlineLine, error) {
	online := map[string]bool{}
	for _, key := range t.inboundService.GetOnlineClients() {
		online[key] = true
	}
	users, err := (&SubUserService{}).List()
	if err != nil {
		return nil, err
	}
	var lines, strays []screenOnlineLine
	for _, v := range users {
		if v.SubId == model.SubUserMonitoringKey {
			continue
		}
		var protocols []string
		for _, c := range v.Clients {
			if !online[onlineKey(c)] {
				continue
			}
			if v.SubId == model.SubUserRobotKey {
				strays = append(strays, screenOnlineLine{label: "🤖 " + c.Name + " · " + protocolLabel(c.Protocol), data: clientCardData(c)})
				continue
			}
			if p := protocolLabel(c.Protocol); !slices.Contains(protocols, p) {
				protocols = append(protocols, p)
			}
		}
		if len(protocols) > 0 {
			lines = append(lines, screenOnlineLine{label: "🟢 " + v.Name + " · " + strings.Join(protocols, "+"), data: "usr_c " + v.SubId})
		}
	}
	return append(lines, strays...), nil
}

// onlineKey is how GetOnlineClients names a client: an xray client by its
// email, a tunnel client by its uuid.
func onlineKey(c SubUserClient) string {
	if c.Kind == SubUserClientXray {
		return c.Name
	}
	return c.Key
}

// clientCardData opens the card of a client: an xray client's by its email,
// a tunnel client's by its uuid.
func clientCardData(c SubUserClient) string {
	if c.Kind == SubUserClientXray {
		return screenClientAction + " " + c.Name
	}
	return "tun_c " + c.Key
}
