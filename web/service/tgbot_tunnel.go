package service

import (
	"context"
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
	"github.com/skip2/go-qrcode"
)

// The tunnel clients (AmneziaWG, WireGuard) of the admin's screens (#182,
// #191): their card and its buttons. Their clients live in tunnel_clients,
// not in the inbound's settings; the screen's inbound list reads them from
// there. Buttons address a client by its uuid; probe accounts are left out.

// tunnelFile is a document for the chat.
type tunnelFile struct {
	name string
	data []byte
}

// tunnelCallback runs a button of the tunnel flows. ok is false for data
// that is not theirs.
func (t *Tgbot) tunnelCallback(data string) (reply screenReply, ok bool) {
	action, args, _ := strings.Cut(data, " ")
	switch action {
	case "tun_c":
		return t.tunnelCard(args), true
	case "tun_r":
		return t.tunnelCard(args), true
	case "tun_en":
		clientUUID, flag, _ := strings.Cut(args, " ")
		return t.tunnelSetEnable(clientUUID, flag == "1"), true
	case "tun_rt":
		return t.tunnelResetAsk(args), true
	case "tun_rtc":
		return t.tunnelReset(args), true
	case "tun_cf":
		return t.tunnelConfig(args), true
	case "tun_ex":
		if _, ok := findTunnelPeer(args); !ok {
			return t.tunnelNoResult(), true
		}
		return screenReply{usersReply: usersReply{keyboard: t.numberPresets(args, "tgbot.buttons.cancelReset", "tun_r "+args,
			"tun_exc", "tun_exi", expiryPresetDays, t.expiryPresetLabel)}}, true
	case "tun_exi":
		fields := strings.Fields(data)
		if len(fields) < 2 {
			return t.tunnelNoResult(), true
		}
		return t.numberKeypad(fields, "tun_exi", "tgbot.buttons.confirmNumber", "tun_exc", "tun_r "+fields[1], t.tunnelNoResult()), true
	case "tun_exc":
		clientUUID, num, _ := strings.Cut(args, " ")
		days, err := strconv.Atoi(num)
		if err != nil || days < 0 {
			return t.tunnelNoResult(), true
		}
		return t.tunnelSetExpiry(clientUUID, int64(days)), true
	}
	return screenReply{}, false
}

// tunnelKindOf is the tunnel kind of an inbound protocol.
func tunnelKindOf(protocol model.Protocol) (string, bool) {
	switch protocol {
	case model.AmneziaWG:
		return model.TunnelKindAwg, true
	case model.NativeWG:
		return model.TunnelKindWg, true
	}
	return "", false
}

// tunnelClients is what the bot asks of a tunnel kind's service.
type tunnelClients interface {
	GetClients() ([]model.TunnelClient, error)
	GetClientByUUID(clientUUID string) (*model.TunnelClient, error)
	ToggleClientByUUID(clientUUID string, enable bool) error
	ResetClientTrafficByUUID(clientUUID string) error
	GetClientConfigByUUID(clientUUID string) (string, error)
	UpdateClientByUUID(clientUUID string, client *model.TunnelClient) error
}

// tunnelKinds are the tunnel kinds in the order the bot looks a uuid up.
var tunnelKinds = []string{model.TunnelKindAwg, model.TunnelKindWg}

// tunnelClientsOf is the service of a tunnel kind.
func tunnelClientsOf(kind string) tunnelClients {
	if kind == model.TunnelKindWg {
		return &WgService{}
	}
	return &AwgService{}
}

// tunnelPeer is a tunnel client with its kind.
type tunnelPeer struct {
	kind   string
	client *model.TunnelClient
}

// findTunnelPeer finds the tunnel client under uuid. A probe account is not
// found: its buttons would edit it.
func findTunnelPeer(clientUUID string) (*tunnelPeer, bool) {
	for _, kind := range tunnelKinds {
		c, err := tunnelClientsOf(kind).GetClientByUUID(clientUUID)
		if err != nil {
			continue
		}
		if IsProbeAccount(c.Email) {
			return nil, false
		}
		return &tunnelPeer{kind: kind, client: c}, true
	}
	return nil, false
}

// tunnelNoResult answers a button whose client is gone or is a probe account.
func (t *Tgbot) tunnelNoResult() screenReply {
	return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.noResult"), text: t.I18nBot("tgbot.noResult")}}
}

// tunnelInboundLabel names the inbound row of a tunnel kind: "remark
// (protocol)", or the protocol alone when there is no row.
func tunnelInboundLabel(kind string) string {
	protocol := tunnelProtocol(kind)
	var ib model.Inbound
	if err := database.GetDB().Where("protocol = ?", protocol).Order("id asc").First(&ib).Error; err != nil {
		return protocol
	}
	return fmt.Sprintf("%s (%s)", ib.Remark, protocol)
}

// tunnelOwner is the user the client belongs to: the user of its
// subscription, robot without one. nil when the user cannot be read.
func tunnelOwner(clientUUID string) *SubUserView {
	subIds, err := (&TunnelSubscriptionService{}).SubIdsByUUIDs([]string{clientUUID})
	if err != nil {
		return nil
	}
	key := subIds[clientUUID]
	if key == "" {
		key = model.SubUserRobotKey
	}
	v, err := (&SubUserService{}).Get(key)
	if err != nil {
		return nil
	}
	return v
}

// tunnelCard is a tunnel client's card: its state, its user and the user's
// subscription, and the buttons a tunnel client has — enable/disable, traffic
// reset, the expiry (as the xray card changes it), its config and the way to
// its user. The xray card's IP limit, IP log, traffic limit and Telegram user
// are not offered: the tunnel model has no bot flow for them.
func (t *Tgbot) tunnelCard(clientUUID string) screenReply {
	p, ok := findTunnelPeer(clientUUID)
	if !ok {
		return t.tunnelNoResult()
	}
	c := p.client
	var b strings.Builder
	b.WriteString(t.I18nBot("tgbot.tunnel.card", "Name=="+html.EscapeString(c.Email), "Inbound=="+html.EscapeString(tunnelInboundLabel(p.kind))))
	enabled := t.I18nBot("tgbot.messages.no")
	if c.Enable {
		enabled = t.I18nBot("tgbot.messages.yes")
	}
	b.WriteString(t.I18nBot("tgbot.messages.enabled", "Enable=="+enabled))
	online := c.Enable && c.LastOnline > time.Now().Add(-onlineWindow).UnixMilli()
	if online {
		b.WriteString(t.I18nBot("tgbot.messages.online", "Status=="+t.I18nBot("tgbot.online")))
	} else {
		b.WriteString(t.I18nBot("tgbot.messages.online", "Status=="+t.I18nBot("tgbot.offline")))
		if c.LastOnline > 0 {
			b.WriteString(t.I18nBot("tgbot.messages.lastOnline", "Time=="+time.UnixMilli(c.LastOnline).Format("2006-01-02 15:04:05")))
		}
	}
	b.WriteString(t.I18nBot("tgbot.users.traffic", "UpDown=="+common.FormatTraffic(c.Upload+c.Download), "Total=="+t.usersTraffic(c.TotalGB)))
	b.WriteString(t.I18nBot("tgbot.users.expire", "Time=="+t.usersExpiry(c.ExpiryTime)))
	owner := tunnelOwner(c.UUID)
	if owner != nil {
		b.WriteString(t.I18nBot("tgbot.tunnel.user", "Name=="+html.EscapeString(owner.Name)))
		if owner.Technical {
			b.WriteString(t.I18nBot("tgbot.users.noSubscription"))
		} else {
			subURL, _ := t.subscriptionURLs(owner.SubId)
			b.WriteString(t.I18nBot("tgbot.users.subscription", "Url=="+html.EscapeString(subURL)))
		}
	}
	if c.Comment != "" {
		b.WriteString(t.I18nBot("tgbot.users.comment", "Comment=="+html.EscapeString(c.Comment)))
	}

	button := func(label, data string) telego.InlineKeyboardButton {
		return tu.InlineKeyboardButton(label).WithCallbackData(t.encodeQuery(data))
	}
	toggle := button(t.I18nBot("tgbot.users.disable"), "tun_en "+c.UUID+" 0")
	if !c.Enable {
		toggle = button(t.I18nBot("tgbot.users.enable"), "tun_en "+c.UUID+" 1")
	}
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.buttons.refresh"), "tun_r "+c.UUID)),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.buttons.resetTraffic"), "tun_rt "+c.UUID), toggle),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.buttons.resetExpire"), "tun_ex "+c.UUID)),
		tu.InlineKeyboardRow(button(t.I18nBot("tgbot.tunnel.config"), "tun_cf "+c.UUID)),
	}
	if owner != nil {
		rows = append(rows, tu.InlineKeyboardRow(button("👤 "+owner.Name, "usr_c "+owner.SubId)))
	}
	return screenReply{usersReply: usersReply{text: b.String(), keyboard: tu.InlineKeyboard(rows...), route: "tun_c " + c.UUID}}
}

// tunnelSetEnable switches the client on or off.
func (t *Tgbot) tunnelSetEnable(clientUUID string, enable bool) screenReply {
	p, ok := findTunnelPeer(clientUUID)
	if !ok {
		return t.tunnelNoResult()
	}
	if err := tunnelClientsOf(p.kind).ToggleClientByUUID(clientUUID, enable); err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	InvalidateTunnelSubCache()
	reply := t.tunnelCard(clientUUID)
	if enable {
		reply.toast = t.I18nBot("tgbot.answers.enableSuccess", "Email=="+p.client.Email)
	} else {
		reply.toast = t.I18nBot("tgbot.answers.disableSuccess", "Email=="+p.client.Email)
	}
	return reply
}

// tunnelResetAsk puts the reset's confirmation in place of the card's
// buttons.
func (t *Tgbot) tunnelResetAsk(clientUUID string) screenReply {
	if _, ok := findTunnelPeer(clientUUID); !ok {
		return t.tunnelNoResult()
	}
	return screenReply{usersReply: usersReply{keyboard: tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).
			WithCallbackData(t.encodeQuery("tun_r "+clientUUID))),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmResetTraffic")).
			WithCallbackData(t.encodeQuery("tun_rtc "+clientUUID))),
	)}}
}

// tunnelReset zeroes the client's traffic counters.
func (t *Tgbot) tunnelReset(clientUUID string) screenReply {
	p, ok := findTunnelPeer(clientUUID)
	if !ok {
		return t.tunnelNoResult()
	}
	if err := tunnelClientsOf(p.kind).ResetClientTrafficByUUID(clientUUID); err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	InvalidateTunnelSubCache()
	reply := t.tunnelCard(clientUUID)
	reply.toast = t.I18nBot("tgbot.answers.resetTrafficSuccess", "Email=="+p.client.Email)
	return reply
}

// tunnelSetExpiry changes the client's expiry by days (extendedExpiry, the
// xray card's rule). A client switched off because its expiry passed, still
// within its traffic, is switched back on.
func (t *Tgbot) tunnelSetExpiry(clientUUID string, days int64) screenReply {
	p, ok := findTunnelPeer(clientUUID)
	if !ok {
		return t.tunnelNoResult()
	}
	c := *p.client
	now := time.Now().UnixMilli()
	expiredOff := !c.Enable && c.ExpiryTime > 0 && c.ExpiryTime < now && (c.TotalGB == 0 || c.Upload+c.Download < c.TotalGB)
	c.ExpiryTime = extendedExpiry(c.ExpiryTime, days, now)
	if expiredOff {
		c.Enable = true
	}
	if err := tunnelClientsOf(p.kind).UpdateClientByUUID(clientUUID, &c); err != nil {
		return screenReply{usersReply: t.usersError(err)}
	}
	InvalidateTunnelSubCache()
	reply := t.tunnelCard(clientUUID)
	reply.toast = t.I18nBot("tgbot.answers.expireResetSuccess", "Email=="+c.Email)
	return reply
}

// tunnelConfig sends the chat the client's .conf and its QR.
func (t *Tgbot) tunnelConfig(clientUUID string) screenReply {
	p, ok := findTunnelPeer(clientUUID)
	if !ok {
		return t.tunnelNoResult()
	}
	conf, err := tunnelClientsOf(p.kind).GetClientConfigByUUID(clientUUID)
	if err != nil || conf == "" {
		if err == nil {
			err = common.NewError("empty config")
		}
		return screenReply{usersReply: t.usersError(err)}
	}
	files := tunnelConfigFiles(p.kind, p.client.Email, conf)
	return screenReply{usersReply: usersReply{toast: t.I18nBot("tgbot.answers.successfulOperation")}, files: files}
}

// tunnelConfigFiles are a client's .conf and, when it encodes, the QR of the
// same text for in-app scan import: <email>.conf and <email>.conf.png.
func tunnelConfigFiles(kind, email, conf string) []tunnelFile {
	files := []tunnelFile{tunnelConfFile(kind, email, conf)}
	if qr, ok := tunnelQRFile(files[0]); ok {
		files = append(files, qr)
	}
	return files
}

// tunnelConfFile is a client's .conf, named after the client.
func tunnelConfFile(kind, email, conf string) tunnelFile {
	name := email + ".conf"
	if email == "" {
		name = tunnelProtocol(kind) + ".conf"
	}
	return tunnelFile{name: name, data: []byte(conf)}
}

// tunnelQRFile is the QR of a .conf; false when the text does not encode.
func tunnelQRFile(conf tunnelFile) (tunnelFile, bool) {
	png, err := qrcode.Encode(string(conf.data), qrcode.Medium, 320)
	if err != nil {
		return tunnelFile{}, false
	}
	return tunnelFile{name: conf.name + ".png", data: png}, true
}

// sendTunnelFiles sends the files as documents, in order; it stops at the
// first that fails.
func (t *Tgbot) sendTunnelFiles(chatId int64, files []tunnelFile) error {
	for _, f := range files {
		doc := tu.Document(tu.ID(chatId), tu.FileFromBytes(f.data, f.name))
		if _, err := bot.SendDocument(context.Background(), doc); err != nil {
			return err
		}
	}
	return nil
}
