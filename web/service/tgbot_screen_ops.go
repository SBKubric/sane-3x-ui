package service

import "strconv"

// The screens of #192 behind the admin's main menu: Online
// (tgbot_screen_online.go), Reports (tgbot_screen_reports.go), Monitoring
// (tgbot_screen_monitoring.go), Server and the admin link
// (tgbot_screen_server.go). Their buttons come here from
// screenMenuCallback; Online and Server keep the routes #191 gave them.

// Callback data of these screens.
const (
	screenReportsRoute    = "s_rep"   // the reports
	screenTrafficRoute    = "s_rtr"   // s_rtr <page>: the users by traffic
	screenRunningOutRoute = "s_rdp"   // s_rdp <page>: the users that run out soon
	screenMonitoringRoute = "s_mon"   // monitoring
	screenEventsRoute     = "s_mev"   // s_mev <before, ms>: the events before then
	screenRestartAsk      = "s_rx"    // «Restart xray?»
	screenRestartData     = "s_rxc"   // the restart, confirmed
	screenResetAllAsk     = "s_rall"  // «Reset the traffic of all clients?»
	screenResetAllData    = "s_rallc" // the reset, confirmed
	screenAdminLinkData   = "s_adm"   // the admin link, as a message
)

// screenOpsCallback runs the buttons of these screens; false for data that
// is not theirs.
func (t *Tgbot) screenOpsCallback(action, args string) (screenReply, bool) {
	page, _ := strconv.Atoi(args)
	switch action {
	case screenReportsRoute:
		return t.screenReports(), true
	case screenTrafficRoute:
		return t.screenTraffic(page), true
	case screenRunningOutRoute:
		return t.screenRunningOut(page), true
	case screenMonitoringRoute:
		return t.screenMonitoring(), true
	case screenEventsRoute:
		before, _ := strconv.ParseInt(args, 10, 64)
		return t.screenEvents(before), true
	case screenRestartAsk:
		return t.screenRestartConfirm(), true
	case screenRestartData:
		return t.screenRestart(), true
	case screenResetAllAsk:
		return t.screenConfirm(t.I18nBot("tgbot.ops.resetAllAsk"), screenResetAllData), true
	case screenResetAllData:
		return t.screenResetAll(), true
	case screenAdminLinkData:
		return t.screenAdminLink(), true
	}
	return screenReply{}, false
}
