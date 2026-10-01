package service

import (
	"context"
	"errors"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/skip2/go-qrcode"
)

// The link broadcast itself (#214 points 3-5, #222): a queue that sends
// each recipient «Your subscription link has changed» — the link, its QR as
// a picture, «📱 My subscription» and how to update it in the app — with
// the .conf files of their tunnel clients when those changed since they last
// got them; then a report to the admin, and a journal of the broadcasts.
//
// The link goes to the subscription's owner only: the user's own Telegram
// (sub_users.tg_id), never the tgIds of the clients. Technical and disabled
// users get nothing; users without Telegram are listed in the report, with
// «📋 Links for manual sending».

// Test seams: how a broadcast runs (on its own goroutine), how it pauses,
// and the watcher's clock.
var (
	subLinkGo    = func(f func()) { go f() }
	subLinkSleep = time.Sleep
	subLinkNow   = time.Now
)

const (
	// subLinkInterval spaces the Bot API calls of a broadcast: about 25 a
	// second, under Telegram's 30.
	subLinkInterval = 40 * time.Millisecond
	// subLinkRetries is how many times a call refused with 429 is retried
	// after its retry_after.
	subLinkRetries = 5
	// subLinkReportLines caps each list of the report.
	subLinkReportLines = 30
)

// subLinkRunMu runs the broadcasts one after another: they share Telegram's
// limit.
var subLinkRunMu sync.Mutex

// subLinkJob is a broadcast to start.
type subLinkJob struct {
	trigger    string // model.SubLinkTrigger*
	reasons    []string
	startedBy  string  // the admin's Telegram id, or "panel"
	reportTo   []int64 // the chats the report goes to
	users      []*SubUserView
	noTelegram []*SubUserView
}

// SubLinkStarted is what the users page learns of a broadcast it started.
type SubLinkStarted struct {
	Id         int64 `json:"id"`
	Recipients int   `json:"recipients"`
	NoTelegram int   `json:"noTelegram"`
}

// subLinkStart writes the broadcast to the journal and starts it.
func (t *Tgbot) subLinkStart(job subLinkJob) (*model.SubLinkBroadcast, error) {
	if !t.IsRunning() {
		return nil, common.NewError("the Telegram bot is not running")
	}
	b := &model.SubLinkBroadcast{Trigger: job.trigger, Reasons: strings.Join(job.reasons, ","), StartedBy: job.startedBy,
		CreatedAt: time.Now().UnixMilli()}
	if err := database.GetDB().Create(b).Error; err != nil {
		return nil, err
	}
	subLinkGo(func() { t.subLinkRun(b, job) })
	return b, nil
}

// subLinkRun sends the job's messages, writes each delivery to the journal
// and sends the report.
func (t *Tgbot) subLinkRun(b *model.SubLinkBroadcast, job subLinkJob) {
	subLinkRunMu.Lock()
	defer subLinkRunMu.Unlock()
	db := database.GetDB()
	pacer := &subLinkPacer{}
	base := t.subLinkBase()
	for _, v := range job.users {
		status, reason := t.subLinkDeliver(pacer, v, base)
		switch status {
		case model.SubLinkSent:
			b.Sent++
		case model.SubLinkBlocked:
			b.Blocked++
		default:
			b.Failed++
		}
		d := model.SubLinkDelivery{BroadcastId: b.Id, SubId: v.SubId, Name: v.Name, TgId: v.TgId, Status: status, Error: reason}
		if err := db.Create(&d).Error; err != nil {
			logger.Warning("link broadcast: journal:", err)
		}
	}
	for _, v := range job.noTelegram {
		b.NoTelegram++
		d := model.SubLinkDelivery{BroadcastId: b.Id, SubId: v.SubId, Name: v.Name, Status: model.SubLinkNoTelegram}
		if err := db.Create(&d).Error; err != nil {
			logger.Warning("link broadcast: journal:", err)
		}
	}
	b.FinishedAt = time.Now().UnixMilli()
	if err := db.Model(b).Select("sent", "failed", "blocked", "no_telegram", "finished_at").Updates(b).Error; err != nil {
		logger.Warning("link broadcast: journal:", err)
	}
	t.subLinkReport(b, job.reportTo)
}

// subLinkDeliver sends v the new link, and the .conf files of v's tunnel
// clients when they changed since v last got them. It returns how it went.
func (t *Tgbot) subLinkDeliver(pacer *subLinkPacer, v *SubUserView, base string) (status, reason string) {
	link := base + v.SubId
	text := t.I18nBot("tgbot.sublink.message", "Link=="+html.EscapeString(link))
	kb := tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.sublink.mySub")).WithCallbackData("client_commands")))
	err := pacer.call(func(ctx context.Context) error {
		png, qrErr := qrcode.Encode(link, qrcode.Medium, 320)
		if qrErr != nil { // too long to encode: the link alone
			_, err := bot.SendMessage(ctx, tu.Message(tu.ID(v.TgId), text).WithParseMode("HTML").WithReplyMarkup(kb))
			return err
		}
		_, err := bot.SendPhoto(ctx, tu.Photo(tu.ID(v.TgId), tu.FileFromBytes(png, "subscription.png")).
			WithCaption(text).WithParseMode("HTML").WithReplyMarkup(kb))
		return err
	})
	if err != nil {
		return subLinkRefusal(err)
	}
	confHash := ""
	if files, hash := t.subLinkConfFiles(v); hash != "" && hash != subLinkKnownConf(v.TgId) {
		for _, f := range files {
			err := pacer.call(func(ctx context.Context) error {
				_, err := bot.SendDocument(ctx, tu.Document(tu.ID(v.TgId), tu.FileFromBytes(f.data, f.name)))
				return err
			})
			if err != nil {
				_, why := subLinkRefusal(err)
				reason = ".conf: " + why
				break
			}
		}
		if reason == "" {
			confHash = hash
		}
	}
	if err := subLinkDelivered(v.TgId, v.SubId, link, confHash); err != nil {
		logger.Warning("link broadcast: known link:", err)
	}
	return model.SubLinkSent, reason
}

// subLinkRefusal words Telegram's refusal: a person who blocked the bot, or
// another failure with Telegram's description.
func subLinkRefusal(err error) (status, reason string) {
	var apiErr *ta.Error
	if errors.As(err, &apiErr) {
		if apiErr.ErrorCode == 403 && strings.Contains(strings.ToLower(apiErr.Description), "blocked") {
			return model.SubLinkBlocked, apiErr.Description
		}
		return model.SubLinkFailed, apiErr.Description
	}
	return model.SubLinkFailed, err.Error()
}

// subLinkPacer keeps a broadcast's Bot API calls subLinkInterval apart and
// waits out Telegram's flood control.
type subLinkPacer struct {
	called bool
}

// call runs one Bot API call: after the pause since the previous one, and
// again after retry_after when Telegram answers 429.
func (p *subLinkPacer) call(fn func(ctx context.Context) error) error {
	for attempt := 0; ; attempt++ {
		if p.called {
			subLinkSleep(subLinkInterval)
		}
		p.called = true
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := fn(ctx)
		cancel()
		var apiErr *ta.Error
		if !errors.As(err, &apiErr) || apiErr.ErrorCode != 429 || attempt >= subLinkRetries {
			return err
		}
		wait := time.Second
		if apiErr.Parameters != nil && apiErr.Parameters.RetryAfter > 0 {
			wait = time.Duration(apiErr.Parameters.RetryAfter) * time.Second
		}
		logger.Warningf("link broadcast: Telegram asks to wait %v", wait)
		subLinkSleep(wait)
	}
}

// subLinkReport tells the admins in to how the broadcast went: sent, not
// sent and why, without Telegram; «📋 Links for manual sending» when anyone
// is left.
func (t *Tgbot) subLinkReport(b *model.SubLinkBroadcast, to []int64) {
	var deliveries []model.SubLinkDelivery
	if err := database.GetDB().Where("broadcast_id = ?", b.Id).Order("id").Find(&deliveries).Error; err != nil {
		logger.Warning("link broadcast: report:", err)
	}
	var failed, noTelegram []string
	for _, d := range deliveries {
		switch d.Status {
		case model.SubLinkBlocked:
			failed = append(failed, "• "+html.EscapeString(d.Name)+" — 🚫 "+t.I18nBot("tgbot.sublink.blocked"))
		case model.SubLinkFailed:
			failed = append(failed, "• "+html.EscapeString(d.Name)+" — "+html.EscapeString(d.Error))
		case model.SubLinkNoTelegram:
			noTelegram = append(noTelegram, html.EscapeString(d.Name))
		}
	}
	lines := []string{t.I18nBot("tgbot.sublink.report", "Id=="+strconv.FormatInt(b.Id, 10))}
	if reasons := t.subLinkReasonWords(b.Reasons); reasons != "" {
		lines = append(lines, t.I18nBot("tgbot.sublink.reasons", "Reasons=="+reasons))
	}
	lines = append(lines, "", t.I18nBot("tgbot.sublink.reportSent", "Count=="+strconv.Itoa(b.Sent)))
	if n := b.Failed + b.Blocked; n > 0 {
		lines = append(lines, t.I18nBot("tgbot.sublink.reportFailed", "Count=="+strconv.Itoa(n)))
		lines = append(lines, t.subLinkCapped(failed)...)
	}
	if b.NoTelegram > 0 {
		lines = append(lines, t.I18nBot("tgbot.sublink.reportNoTg", "Count=="+strconv.Itoa(b.NoTelegram)))
		lines = append(lines, strings.Join(t.subLinkCapped(noTelegram), ", "))
	}
	var kb []telego.ReplyMarkup
	if b.Failed+b.Blocked+b.NoTelegram > 0 {
		kb = append(kb, tu.InlineKeyboard(tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.sublink.manual")).WithCallbackData(subLinkManualAction+" "+strconv.FormatInt(b.Id, 10)))))
	}
	for _, chat := range to {
		t.SendMsgToTgbot(chat, strings.Join(lines, "\r\n"), kb...)
	}
}

// subLinkCapped is the first subLinkReportLines lines, and how many more.
func (t *Tgbot) subLinkCapped(lines []string) []string {
	if len(lines) <= subLinkReportLines {
		return lines
	}
	return append(lines[:subLinkReportLines:subLinkReportLines],
		t.I18nBot("tgbot.sublink.more", "Count=="+strconv.Itoa(len(lines)-subLinkReportLines)))
}

// subLinkReasonWords words a comma-separated list of reasons.
func (t *Tgbot) subLinkReasonWords(reasons string) string {
	var words []string
	for _, r := range strings.Split(reasons, ",") {
		if r != "" {
			words = append(words, t.I18nBot("tgbot.sublink.reason."+r))
		}
	}
	return strings.Join(words, ", ")
}

// subLinkManualLinks sends chat the links of the people a broadcast did not
// reach — refused, blocked or without Telegram — to pass on by hand: each
// user's link as it is now.
func (t *Tgbot) subLinkManualLinks(chatId int64, id int64) {
	var deliveries []model.SubLinkDelivery
	if err := database.GetDB().Where("broadcast_id = ? AND status <> ?", id, model.SubLinkSent).Order("id").
		Find(&deliveries).Error; err != nil {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.wentWrong"))
		return
	}
	blocks := []string{t.I18nBot("tgbot.sublink.manualTitle", "Id=="+strconv.FormatInt(id, 10))}
	base := t.subLinkBase()
	users := (&SubUserService{})
	for _, d := range deliveries {
		v, err := users.Get(d.SubId)
		if err != nil || v.Technical {
			continue
		}
		blocks = append(blocks, "<b>"+html.EscapeString(v.Name)+"</b>\r\n<code>"+html.EscapeString(base+v.SubId)+"</code>")
	}
	if len(blocks) == 1 {
		blocks = append(blocks, t.I18nBot("tgbot.sublink.manualNone"))
	}
	t.SendMsgToTgbot(chatId, strings.Join(blocks, "\r\n\r\n"))
}

// subLinkAudience are the users a broadcast to everyone reaches — the
// enabled regular users with Telegram — and the enabled ones without.
func subLinkAudience() (users, noTelegram []*SubUserView, err error) {
	all, err := subLinkUsers()
	if err != nil {
		return nil, nil, err
	}
	for _, v := range all {
		switch {
		case !v.Enable:
		case v.TgId == 0:
			noTelegram = append(noTelegram, v)
		default:
			users = append(users, v)
		}
	}
	return users, noTelegram, nil
}

// subLinkStartAll starts a broadcast to every user.
func (t *Tgbot) subLinkStartAll(trigger, startedBy string, reportTo []int64) (*SubLinkStarted, error) {
	users, noTelegram, err := subLinkAudience()
	if err != nil {
		return nil, err
	}
	if len(users) == 0 && len(noTelegram) == 0 {
		return nil, common.NewError("no enabled user to send the link to")
	}
	b, err := t.subLinkStart(subLinkJob{trigger: trigger, startedBy: startedBy, reportTo: reportTo, users: users, noTelegram: noTelegram})
	if err != nil {
		return nil, err
	}
	return &SubLinkStarted{Id: b.Id, Recipients: len(users), NoTelegram: len(noTelegram)}, nil
}

// BroadcastLinksFromPanel is the users page's «📣 Send links»: the link to
// every user with Telegram, the report to every admin.
func (t *Tgbot) BroadcastLinksFromPanel() (*SubLinkStarted, error) {
	return t.subLinkStartAll(model.SubLinkTriggerAPI, "panel", adminIds)
}
