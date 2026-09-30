package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// The notification channel (#195, decisions on #185): the bot posts its
// notifications to one Telegram channel or group set in the panel
// (tgNotifyChatId), not to the admins' chats. The channel gets the periodic
// report (summary, monitoring digest, exhausted clients), monitoring alerts,
// panel logins and CPU alerts. The admins' chats keep the database backup,
// which holds keys and passwords; clients keep their own messages (AWG
// configs, their exhausted clients); files and answers to button presses
// stay in the chat the button was pressed in.
//
// With no channel set nothing is posted anywhere: the notifications are
// dropped, the log says so once, and the Telegram settings tab shows a
// warning.

// notifyChannelWarned is set once the missing channel has been logged, and
// cleared when a channel is found again, so that the log carries one warning
// per stretch without a channel instead of one per notification — the CPU
// check alone runs every ten seconds.
var notifyChannelWarned atomic.Bool

// errNoNotifyChannel is the error of an empty channel.
var errNoNotifyChannel = errors.New("no notification channel given")

// tgNotifyChat turns a tgNotifyChatId value into the chat Telegram is asked
// to post to: a numeric id, or an @username.
func tgNotifyChat(value string) (telego.ChatID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return telego.ChatID{}, errNoNotifyChannel
	}
	if !entity.ValidTgNotifyChatId(value) {
		return telego.ChatID{}, errors.New("notification channel must be a chat id (-100…) or an @username: " + value)
	}
	if strings.HasPrefix(value, "@") {
		return tu.Username(value), nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return telego.ChatID{}, err
	}
	return tu.ID(id), nil
}

// notifyChat is the configured notification channel, false when none is set
// (or the stored value cannot be addressed), which it logs once.
func (t *Tgbot) notifyChat() (telego.ChatID, bool) {
	value, err := t.settingService.GetTgNotifyChatId()
	if err == nil {
		var chat telego.ChatID
		if chat, err = tgNotifyChat(value); err == nil {
			notifyChannelWarned.Store(false)
			return chat, true
		}
	}
	if notifyChannelWarned.CompareAndSwap(false, true) {
		logger.Warning("Telegram: no notification channel is set, notifications are not sent:", err)
	}
	return telego.ChatID{}, false
}

// notifyChannelReady reports whether a notification would go anywhere: the
// bot runs and a channel is set.
func (t *Tgbot) notifyChannelReady() bool {
	if !t.IsRunning() {
		return false
	}
	_, ok := t.notifyChat()
	return ok
}

// SendMsgToNotifyChannel posts msg to the notification channel, or drops it
// when the bot is not running or no channel is set.
func (t *Tgbot) SendMsgToNotifyChannel(msg string) {
	if msg == "" || !t.IsRunning() {
		return
	}
	chat, ok := t.notifyChat()
	if !ok {
		return
	}
	if err := sendToChat(chat, msg); err != nil {
		logger.Warning("Telegram: could not post to the notification channel:", err)
	}
}

// sendExhaustedToNotifyChannel posts the exhausted inbounds and clients of
// the periodic report. Without the per-client buttons: the channel is for
// reading, the client cards are in the admin's chat with the bot.
func (t *Tgbot) sendExhaustedToNotifyChannel() {
	if !t.IsRunning() {
		return
	}
	report, _ := t.exhaustedReport()
	t.SendMsgToNotifyChannel(report)
}

// SendNotifyTest posts a test message to value — the channel as typed in
// the settings form, saved or not — and returns Telegram's refusal, if any,
// so the settings tab can show why a channel does not work. The bot's
// channel screen shows the outcome as the last test (#202).
func (t *Tgbot) SendNotifyTest(value string) (err error) {
	defer func() { notifyLastTest.record(value, err) }()
	chat, err := tgNotifyChat(value)
	if err != nil {
		return err
	}
	if !t.IsRunning() || bot == nil {
		return errors.New("the Telegram bot is not running: enable it, save and restart the panel")
	}
	return sendToChat(chat, t.I18nBot("tgbot.messages.notifyTest", "Hostname=="+hostname))
}

// notifyMessageLimit keeps each post under Telegram's 4096 characters.
const notifyMessageLimit = 4000

// sendToChat posts msg to chat as HTML, split at blank lines when it is too
// long for one message, and returns the first error Telegram gives.
func sendToChat(chat telego.ChatID, msg string) error {
	for i, part := range splitNotifyMessage(msg) {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := bot.SendMessage(ctx, &telego.SendMessageParams{ChatID: chat, Text: part, ParseMode: "HTML"})
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// splitNotifyMessage cuts msg at its blank lines ("\r\n\r\n", how the bot's
// messages separate their blocks) into parts of at most notifyMessageLimit
// bytes, as far as the blocks allow.
func splitNotifyMessage(msg string) []string {
	if len(msg) <= notifyMessageLimit {
		return []string{msg}
	}
	var parts []string
	for _, block := range strings.Split(msg, "\r\n\r\n") {
		if n := len(parts); n > 0 && len(parts[n-1])+len("\r\n\r\n")+len(block) <= notifyMessageLimit {
			parts[n-1] += "\r\n\r\n" + block
			continue
		}
		if strings.TrimSpace(block) != "" {
			parts = append(parts, block)
		}
	}
	return parts
}

// UseTelegramBotForTest points the bot at b and marks it running, as Start
// would, and returns the function that puts the previous bot back. Tests
// outside this package use it to put a fake Bot API behind a handler.
func UseTelegramBotForTest(b *telego.Bot) (restore func()) {
	tgBotMutex.Lock()
	prevBot, prevRunning := bot, isRunning
	bot, isRunning = b, b != nil
	tgBotMutex.Unlock()
	return func() {
		tgBotMutex.Lock()
		bot, isRunning = prevBot, prevRunning
		tgBotMutex.Unlock()
	}
}
