package service

import (
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// «📡 Monitoring» (#192): what the Monitoring page shows, read through the
// same MonitoringService calls (UITargets, UIEvents): how fresh the data
// is, the mon-clients, and for each enabled inbound the state of its paths
// with their 24 h uptime; «📜 Events», the feed page by page; and
// «📡 Probe accounts», monitoring's probes (#183).

// screenEventsPage is the events one page of the feed shows.
const screenEventsPage = 10

// screenMonitoring is the monitoring screen.
func (t *Tgbot) screenMonitoring() screenReply {
	button := func(label, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(data)
	}
	kb := tu.InlineKeyboard(
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.ops.monEvents"), screenEventsRoute+" 0"),
			button(t.I18nBot("tgbot.buttons.refresh"), screenMonitoringRoute)),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.probe.list"), probeListAction+" 0")),
	)
	reply := screenReply{usersReply: usersReply{keyboard: kb, route: screenMonitoringRoute}}
	if on, err := t.settingService.GetMonEnable(); err != nil || !on {
		reply.text = t.I18nBot("tgbot.ops.monOff")
		return reply
	}
	now := time.Now()
	targets, err := t.monitoringService.UITargets(now)
	if err != nil {
		logger.Warning("tgbot monitoring screen:", err)
		reply.text = t.I18nBot("tgbot.answers.errorOperation") + "\r\n" + html.EscapeString(err.Error())
		return reply
	}
	lines := []string{t.I18nBot("tgbot.ops.monTitle", "Freshness=="+t.monFreshness(targets, now)), t.monClientsLine(targets.MonClients)}
	for _, ib := range targets.Inbounds {
		if ib.Enable {
			lines = append(lines, t.monInboundLine(ib))
		}
	}
	reply.text = strings.Join(lines, "\r\n")
	return reply
}

// monFreshness says how old the data is: fresh (and how many seconds),
// stale since mon-server went quiet, or none yet.
func (t *Tgbot) monFreshness(targets *MonUITargets, now time.Time) string {
	switch {
	case targets.Stale:
		return t.I18nBot("tgbot.ops.monStale", "Since=="+time.UnixMilli(targets.StaleSince).Format("15:04"))
	case targets.LastContact == 0:
		return t.I18nBot("tgbot.ops.monNoContact")
	}
	ago := now.Sub(time.UnixMilli(targets.LastContact)).Round(time.Second)
	return t.I18nBot("tgbot.ops.monFresh", "Ago=="+monHumanDuration(ago))
}

// monClientsLine lists the mon-clients with their state.
func (t *Tgbot) monClientsLine(clients []MonClient) string {
	if len(clients) == 0 {
		return t.I18nBot("tgbot.ops.monNoClients")
	}
	parts := make([]string, 0, len(clients))
	for _, c := range clients {
		mark := "⚪"
		switch c.State {
		case "ONLINE":
			mark = "🟢"
		case "OFFLINE":
			mark = "🔴"
		}
		parts = append(parts, html.EscapeString(monClientLabel(monFirstNotEmpty(c.Name, c.Id), c.Region))+" "+mark+" "+c.State)
	}
	return t.I18nBot("tgbot.ops.monClients", "Clients=="+strings.Join(parts, " · "))
}

// monInboundLine is an inbound's paths, each in the worst state its live
// targets report, direct first, and the range of their 24 h uptime.
func (t *Tgbot) monInboundLine(ib MonUIInbound) string {
	remark := "Remark==" + html.EscapeString(monInboundLabel(ib.Kind, ib.InboundId, ib.Remark))
	states := map[string][]string{}
	var paths []string
	lo, hi := 2.0, -1.0
	for _, tg := range ib.Targets {
		if tg.Retired {
			continue
		}
		if _, seen := states[tg.Path]; !seen {
			paths = append(paths, tg.Path)
		}
		states[tg.Path] = append(states[tg.Path], tg.State)
		if tg.Uptime24 != nil {
			lo, hi = min(lo, *tg.Uptime24), max(hi, *tg.Uptime24)
		}
	}
	if len(paths) == 0 {
		return t.I18nBot("tgbot.ops.monInboundNoData", remark)
	}
	slices.SortFunc(paths, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == "direct":
			return -1
		case b == "direct":
			return 1
		}
		return strings.Compare(a, b)
	})
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		parts = append(parts, html.EscapeString(p)+" "+worstOfStates(states[p]))
	}
	uptime := "—"
	switch {
	case hi < 0:
	case lo == hi:
		uptime = monUptimePercent(&hi)
	default:
		uptime = strconv.FormatFloat(lo*100, 'f', 1, 64) + "–" + monUptimePercent(&hi)
	}
	return t.I18nBot("tgbot.ops.monInboundLine", remark, "Paths=="+strings.Join(parts, " · "), "Uptime=="+uptime)
}

// screenEvents is a page of the monitoring events before the time before
// (ms; 0 = now), newest first, with the way to older ones.
func (t *Tgbot) screenEvents(before int64) screenReply {
	route := fmt.Sprintf("%s %d", screenEventsRoute, before)
	events, err := t.monitoringService.UIEvents(before, screenEventsPage+1, "", 0)
	if err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	if len(events) == 0 {
		return screenReply{usersReply: usersReply{text: t.I18nBot("tgbot.ops.monNoEvents"), route: route}}
	}
	more := len(events) > screenEventsPage
	events = events[:min(len(events), screenEventsPage)]
	lines := []string{t.I18nBot("tgbot.ops.monEventsTitle")}
	for i := range events {
		lines = append(lines, t.monEventLine(&events[i]))
	}
	var kb *telego.InlineKeyboardMarkup
	if more {
		kb = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.ops.monOlder")).
			WithCallbackData(fmt.Sprintf("%s %d", screenEventsRoute, events[len(events)-1].Ts))))
	}
	return screenReply{usersReply: usersReply{text: strings.Join(lines, "\r\n"), keyboard: kb, route: route}}
}

// monEventLine is one event of the feed.
func (t *Tgbot) monEventLine(ev *MonUIEvent) string {
	at := "Time==" + time.UnixMilli(ev.Ts).Format("02.01 15:04")
	from, to := "From=="+monFirstNotEmpty(ev.From, "—"), "To=="+ev.To
	reason := "Reason=="
	if ev.Reason != "" {
		reason += " (" + html.EscapeString(ev.Reason) + ")"
	}
	client := "Client==" + html.EscapeString(monClientLabel(monFirstNotEmpty(ev.MonClientName, ev.MonClientId), ev.Region))
	switch ev.Kind {
	case model.MonEventKindTarget:
		inbound := "Inbound==" + html.EscapeString(monInboundLabel(ev.InboundKind, ev.InboundId, ev.InboundRemark))
		return t.I18nBot("tgbot.ops.monEventTarget", at, inbound, "Path=="+html.EscapeString(ev.Path), from, to, reason, client)
	case model.MonEventKindMonClient:
		return t.I18nBot("tgbot.ops.monEventClient", at, client, from, to)
	}
	return t.I18nBot("tgbot.ops.monEventPanel", at, from, to, reason)
}
