package service

import (
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"
)

// The domain renewal reminder (#225, map SBKubric/sane-3x-ui-orchestrator#52
// decision 8, docs/spec/users.md §15). DNSExit renews nothing through its
// API, so the owner renews by hand; the panel reminds them in the
// notification channel 30 days and 7 days before the registration expiry
// date (domainExpiry) and on the day itself. Each reminder goes once per
// date: what was posted is kept in domainExpiryReminded ("<date>:<stages>"),
// so a restart does not repeat it and a new date starts over. When the panel
// first looks inside a window — a date entered 5 days ahead — only that
// window's reminder goes, not the earlier ones too. A day already past gets
// one «expired» post. Nothing is posted, and nothing marked, while the bot
// is off or no channel is set: the reminder waits for them.

// domainExpiryNow is the reminder's clock (a test seam).
var domainExpiryNow = time.Now

// domainExpiryRemindedKey is the reminders' state, outside entity.AllSetting.
const domainExpiryRemindedKey = "domainExpiryReminded"

// The reminder stages, the earliest first.
var domainExpiryStages = []struct {
	name string
	days int // the stage is due this many days before the date or fewer
}{{"30", 30}, {"7", 7}, {"0", 0}}

// CheckDomainExpiry posts the reminder that is due and not yet posted
// (job.DomainExpiryJob, hourly).
func (t *Tgbot) CheckDomainExpiry() {
	raw, _ := t.settingService.GetDomainExpiry()
	date, ok, err := entity.ParseDomainExpiry(raw)
	if err != nil || !ok {
		return
	}
	now := domainExpiryNow()
	if loc, err := t.settingService.GetTimeLocation(); err == nil && loc != nil {
		now = now.In(loc)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(date.Sub(today).Hours() / 24)
	due := -1
	for i, stage := range domainExpiryStages {
		if days <= stage.days {
			due = i
		}
	}
	if due < 0 {
		return
	}
	stored, _ := t.settingService.getString(domainExpiryRemindedKey)
	sent := domainExpirySent(stored, raw)
	if slices.Contains(sent, domainExpiryStages[due].name) {
		return
	}
	if !t.notifyChannelReady() {
		return
	}
	t.SendMsgToNotifyChannel(t.domainExpiryText(raw, days))
	// This stage and the earlier ones are done.
	sent = sent[:0]
	for _, stage := range domainExpiryStages[:due+1] {
		sent = append(sent, stage.name)
	}
	if err := t.settingService.setString(domainExpiryRemindedKey, raw+":"+strings.Join(sent, ",")); err != nil {
		logger.Warning("domain expiry reminder:", err)
	}
}

// domainExpirySent are the stages already posted for date, as stored.
func domainExpirySent(stored, date string) []string {
	at, stages, ok := strings.Cut(stored, ":")
	if !ok || at != date || stages == "" {
		return nil
	}
	return strings.Split(stages, ",")
}

// domainExpiryText is the reminder: which domain, which date, how many days
// are left.
func (t *Tgbot) domainExpiryText(date string, days int) string {
	domain := "—"
	if name, _ := t.settingService.GetVPNName(); name != "" {
		domain = vpnNameZone(name)
	}
	params := []string{"Domain==" + html.EscapeString(domain), "Date==" + date}
	switch {
	case days > 0:
		return t.I18nBot("tgbot.domainexpiry.soon", append(params, "Days=="+strconv.Itoa(days))...)
	case days == 0:
		return t.I18nBot("tgbot.domainexpiry.today", params...)
	}
	return t.I18nBot("tgbot.domainexpiry.expired", append(params, "Days=="+strconv.Itoa(-days))...)
}
