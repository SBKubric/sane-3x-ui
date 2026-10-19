// dccheck: живая проверка Delta Chat бота на chatmail relay (тикет #268).
//
// Два процесса deltachat-rpc-server v2.56.0: «bot» (аккаунт с bot=1) и «user»
// (аккаунты user, user2 — играют роль клиентов подписчиков; клиенты Android/iOS/
// desktop построены на том же core). Аккаунты создаются через dcaccount:<relay>.
//
//	go run . -rpc /abs/path/deltachat-rpc-server -dir /abs/work/dir [-relay nine.testrun.org] [-relay2 mehl.cloud]
//
// Каталог -dir содержит базы и ключи аккаунтов: не коммитить.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	dc "github.com/chatmail/rpc-client-go/v2/deltachat"
)

var (
	rpcPath = flag.String("rpc", "deltachat-rpc-server", "path to deltachat-rpc-server")
	workDir = flag.String("dir", "", "work dir for accounts (absolute)")
	relay   = flag.String("relay", "nine.testrun.org", "chatmail relay for dcaccount:")
	relay2  = flag.String("relay2", "", "second relay to test a relay change (optional)")
	start   = time.Now()
)

func logf(format string, a ...any) {
	fmt.Printf("[%7.1fs] %s\n", time.Since(start).Seconds(), fmt.Sprintf(format, a...))
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// node = one rpc-server process with its event pump.
type node struct {
	name   string
	dir    string
	trans  *dc.IOTransport
	rpc    *dc.Rpc
	events chan dc.Event
	mu     sync.Mutex
	smtp   []string // SmtpMessageSent texts, with timestamps
	warns  []string // Warning/Error texts mentioning SMTP 4xx/5xx
}

func openNode(name string) *node {
	n := &node{name: name, dir: filepath.Join(*workDir, name), events: make(chan dc.Event, 100000)}
	n.trans = dc.NewIOTransport()
	n.trans.Cmd = *rpcPath
	n.trans.AccountsDir = filepath.Join(n.dir, "accounts")
	n.trans.Stderr = must(os.Create(filepath.Join(*workDir, name+".stderr.log")))
	if err := n.trans.Open(); err != nil {
		panic(err)
	}
	n.rpc = &dc.Rpc{Context: context.Background(), Transport: n.trans}
	go func() {
		for {
			ev, err := n.rpc.GetNextEvent()
			if err != nil {
				close(n.events)
				return
			}
			switch e := ev.Event.(type) {
			case *dc.EventTypeSmtpMessageSent:
				n.mu.Lock()
				n.smtp = append(n.smtp, fmt.Sprintf("%.1fs acc%d %s", time.Since(start).Seconds(), ev.ContextId, e.Msg))
				n.mu.Unlock()
			case *dc.EventTypeWarning:
				if strings.Contains(e.Msg, "SMTP") || strings.Contains(e.Msg, "Too much mail") {
					n.mu.Lock()
					n.warns = append(n.warns, fmt.Sprintf("%.1fs acc%d %s", time.Since(start).Seconds(), ev.ContextId, e.Msg))
					n.mu.Unlock()
				}
			case *dc.EventTypeInfo:
				if strings.Contains(e.Msg, "Too much mail") || strings.Contains(e.Msg, "Transient error") {
					n.mu.Lock()
					n.warns = append(n.warns, fmt.Sprintf("%.1fs acc%d %s", time.Since(start).Seconds(), ev.ContextId, e.Msg))
					n.mu.Unlock()
				}
			case *dc.EventTypeMsgFailed:
				logf("%s acc%d MsgFailed msg=%d", n.name, ev.ContextId, e.MsgId)
			}
			n.events <- ev
		}
	}()
	return n
}

func (n *node) close() { n.trans.Close() }

func (n *node) takeSmtp() (sent, warns []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sent, warns = n.smtp, n.warns
	n.smtp, n.warns = nil, nil
	return
}

// waitFor consumes events until pred matches one for account acc (acc 0 = any).
func (n *node) waitFor(acc uint32, timeout time.Duration, pred func(dc.EventType) bool) (dc.EventType, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-n.events:
			if !ok {
				return nil, false
			}
			if (acc == 0 || ev.ContextId == acc) && pred(ev.Event) {
				return ev.Event, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

func (n *node) drain() {
	for {
		select {
		case <-n.events:
		default:
			return
		}
	}
}

// account finds an account by display name or creates it on the relay.
func (n *node) account(label, relayHost string, bot bool) uint32 {
	for _, id := range must(n.rpc.GetAllAccountIds()) {
		if v, _ := n.rpc.GetConfig(id, "displayname"); v != nil && *v == label {
			if must(n.rpc.IsConfigured(id)) {
				must(0, n.rpc.StartIo(id))
				return id
			}
		}
	}
	id := must(n.rpc.AddAccount())
	must(0, n.rpc.SetConfig(id, "displayname", &label))
	if bot {
		one := "1"
		must(0, n.rpc.SetConfig(id, "bot", &one))
	}
	t := time.Now()
	must(0, n.rpc.AddTransportFromQr(id, "dcaccount:"+relayHost))
	logf("%s: account %q created on %s in %.1fs", n.name, label, relayHost, time.Since(t).Seconds())
	must(0, n.rpc.StartIo(id))
	return id
}

func addr(n *node, acc uint32) string {
	v := must(n.rpc.GetConfig(acc, "configured_addr"))
	if v == nil {
		return ""
	}
	return *v
}

// redact hides the local part of an address in the printed log.
func redact(s string) string {
	return regexp.MustCompile(`[a-z0-9]{6,}@`).ReplaceAllString(s, "…@")
}

var fprRe = regexp.MustCompile(`\(([^)]+)\):\n((?:[0-9A-F]{4}[ \n]?)+)`)

// fingerprintOf extracts the contact's fingerprint from GetContactEncryptionInfo text.
func fingerprintOf(n *node, acc, contactID uint32) string {
	info := must(n.rpc.GetContactEncryptionInfo(acc, contactID))
	c := must(n.rpc.GetContact(acc, contactID))
	for _, m := range fprRe.FindAllStringSubmatch(info, -1) {
		if m[1] == c.Address {
			return strings.NewReplacer(" ", "", "\n", "").Replace(m[2])
		}
	}
	return ""
}

func nextIncoming(n *node, acc uint32, timeout time.Duration) (dc.Message, bool) {
	ev, ok := n.waitFor(acc, timeout, func(e dc.EventType) bool { _, ok := e.(*dc.EventTypeIncomingMsg); return ok })
	if !ok {
		return dc.Message{}, false
	}
	return must(n.rpc.GetMessage(acc, ev.(*dc.EventTypeIncomingMsg).MsgId)), true
}

func joinerDone(e dc.EventType) bool {
	p, ok := e.(*dc.EventTypeSecurejoinJoinerProgress)
	if ok {
		logf("  JoinerProgress contact=%d progress=%d", p.ContactId, p.Progress)
	}
	return ok && p.Progress == 1000
}

// rss returns VmRSS (kB) and utime+stime (clock ticks) of the rpc-server child of this process.
func rpcServerStats() map[int][2]int64 {
	out := map[int][2]int64{}
	self := os.Getpid()
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		var pid int
		if _, err := fmt.Sscan(e.Name(), &pid); err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		s := string(stat)
		rest := strings.Fields(s[strings.LastIndex(s, ")")+2:])
		var ppid int
		var ut, st int64
		fmt.Sscan(rest[1], &ppid)
		fmt.Sscan(rest[11], &ut)
		fmt.Sscan(rest[12], &st)
		if ppid != self || !strings.Contains(s, "deltachat") {
			continue
		}
		status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		var rss int64
		for _, l := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(l, "VmRSS:") {
				fmt.Sscan(strings.Fields(l)[1], &rss)
			}
		}
		out[pid] = [2]int64{rss, ut + st}
	}
	return out
}

func writePNG(path string) {
	img := image.NewGray(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			if (x/32+y/32)%2 == 0 {
				img.Set(x, y, color.Black)
			} else {
				img.Set(x, y, color.White)
			}
		}
	}
	f := must(os.Create(path))
	defer f.Close()
	must(0, png.Encode(f, img))
}

func ptr[T any](v T) *T { return &v }

func main() {
	flag.Parse()
	if *workDir == "" {
		panic("-dir is required")
	}
	must(0, os.MkdirAll(*workDir, 0o700))

	bot := openNode("bot")
	usr := openNode("user")
	defer usr.close()

	logf("== 0. accounts")
	bAcc := bot.account("VPN bot (test)", *relay, true)
	uAcc := usr.account("user", *relay, false)
	u2Acc := usr.account("user2", *relay, false)
	logf("bot=%s user=%s user2=%s", redact(addr(bot, bAcc)), redact(addr(usr, uAcc)), redact(addr(usr, u2Acc)))
	time.Sleep(5 * time.Second)
	bot.drain()
	usr.drain()

	logf("== 1. SecureJoin by the USER's invite link, user online")
	link := must(usr.rpc.GetChatSecurejoinQrCode(uAcc, nil))
	logf("user link prefix: %s… (len %d)", link[:30], len(link))
	qr := must(bot.rpc.CheckQr(bAcc, link))
	ask, _ := qr.(*dc.QrAskVerifyContact)
	logf("CheckQr kind=%s fingerprint(from link)=%q v3=%v", qr.GetKind(), ask.Fingerprint, ask.IsV3)
	t0 := time.Now()
	bChat := must(bot.rpc.SecureJoin(bAcc, link))
	cs := must(bot.rpc.CanSend(bAcc, bChat))
	early, errEarly := bot.rpc.MiscSendTextMessage(bAcc, bChat, "early hello (sent right after SecureJoin)")
	logf("SecureJoin returned chat=%d immediately (%.2fs); CanSend=%v; early send msg=%d err=%v", bChat, time.Since(t0).Seconds(), cs, early, errEarly)
	_, ok := bot.waitFor(bAcc, 5*time.Minute, joinerDone)
	logf("bot: joiner progress 1000 = %v after %.1fs", ok, time.Since(t0).Seconds())
	ev, ok := usr.waitFor(uAcc, time.Minute, func(e dc.EventType) bool {
		p, ok := e.(*dc.EventTypeSecurejoinInviterProgress)
		return ok && p.Progress == 1000
	})
	uChat, botOnUser := uint32(0), uint32(0)
	if ok {
		p := ev.(*dc.EventTypeSecurejoinInviterProgress)
		uChat, botOnUser = p.ChatId, p.ContactId
	}
	logf("user: inviter progress 1000 = %v chat=%d", ok, uChat)
	if errEarly == nil {
		m := must(bot.rpc.GetMessage(bAcc, early))
		logf("early msg state=%d error=%v", m.State, m.Error)
	}
	t1 := time.Now()
	text := "Привет! Команды: /help /status /1 /u1 /user_2 /12"
	must(bot.rpc.MiscSendTextMessage(bAcc, bChat, text))
	for i := 0; i < 3; i++ {
		m, ok := nextIncoming(usr, uAcc, 2*time.Minute)
		if !ok {
			logf("user: no incoming message")
			break
		}
		logf("user got %q chat=%d after %.1fs", m.Text, m.ChatId, time.Since(t1).Seconds())
		if strings.HasPrefix(m.Text, "Привет") {
			break
		}
	}
	if uChat != 0 {
		bc := must(usr.rpc.GetBasicChatInfo(uAcc, uChat))
		c := must(usr.rpc.GetContact(uAcc, botOnUser))
		logf("user view: chatType=%s isContactRequest=%v; bot contact isBot=%v isVerified=%v", bc.ChatType, bc.IsContactRequest, c.IsBot, c.IsVerified)
	}

	logf("== 2. identity")
	uOnBot := must(bot.rpc.GetChatContacts(bAcc, bChat))[0]
	c := must(bot.rpc.GetContact(bAcc, uOnBot))
	fpr := fingerprintOf(bot, bAcc, uOnBot)
	logf("bot view of user: contactId=%d isKeyContact=%v isVerified=%v e2ee=%v", uOnBot, c.IsKeyContact, c.IsVerified, c.E2eeAvail)
	linkFpr := strings.NewReplacer(" ", "", "\n", "").Replace(ask.Fingerprint) // CheckQr gives it human-readable
	logf("fingerprint via GetContactEncryptionInfo = %s; equals link fingerprint: %v", fpr, fpr == linkFpr)

	logf("== 3. SecureJoin when the user is OFFLINE (user2, IO stopped)")
	link2 := must(usr.rpc.GetChatSecurejoinQrCode(u2Acc, nil))
	must(0, usr.rpc.StopIo(u2Acc))
	t2 := time.Now()
	bChat2 := must(bot.rpc.SecureJoin(bAcc, link2))
	early2, errEarly2 := bot.rpc.MiscSendTextMessage(bAcc, bChat2, "queued while user2 offline")
	logf("SecureJoin(link2) chat=%d; early send msg=%d err=%v", bChat2, early2, errEarly2)
	// Concurrency: while user2's handshake is pending, join user3 (online).
	u3Acc := usr.account("user3", *relay, false)
	time.Sleep(3 * time.Second)
	link3 := must(usr.rpc.GetChatSecurejoinQrCode(u3Acc, nil))
	must(bot.rpc.SecureJoin(bAcc, link3))
	_, ok = bot.waitFor(bAcc, 2*time.Minute, joinerDone)
	logf("user3 joined while user2 pending: %v", ok)
	_, ok = bot.waitFor(bAcc, 75*time.Second, joinerDone)
	logf("while user2 offline ~90s: handshake finished = %v", ok)
	must(0, usr.rpc.StartIo(u2Acc))
	t3 := time.Now()
	if !ok {
		_, ok = bot.waitFor(bAcc, 10*time.Minute, joinerDone)
	}
	logf("after user2 went online: finished=%v in %.1fs (total %.1fs)", ok, time.Since(t3).Seconds(), time.Since(t2).Seconds())
	if errEarly2 == nil {
		time.Sleep(10 * time.Second)
		m := must(bot.rpc.GetMessage(bAcc, early2))
		logf("queued msg state=%d error=%v", m.State, m.Error)
	}
	u2Chats := must(usr.rpc.GetChatlistEntries(u2Acc, nil, nil, nil))
	logf("user2 chatlist entries: %v", u2Chats)

	logf("== 4. files: .conf and PNG")
	confPath := filepath.Join(*workDir, "acc22-awg.conf")
	must(0, os.WriteFile(confPath, []byte("[Interface]\nPrivateKey = TESTKEYTESTKEYTESTKEYTESTKEYTESTKEYTESTKE=\nAddress = 10.8.0.2/32\n"), 0o600))
	pngPath := filepath.Join(*workDir, "qr.png")
	writePNG(pngPath)
	must(bot.rpc.SendMsg(bAcc, bChat, dc.MessageData{File: &confPath, Filename: ptr("acc22-awg.conf"), Text: ptr("Ваш конфиг AWG")}))
	must(bot.rpc.SendMsg(bAcc, bChat, dc.MessageData{File: &pngPath, Filename: ptr("acc22-qr.png"), Viewtype: ptr(dc.ViewtypeImage), Text: ptr("QR для импорта")}))
	must(bot.rpc.SendMsg(bAcc, bChat, dc.MessageData{File: &pngPath, Filename: ptr("acc22-qr-as-file.png"), Viewtype: ptr(dc.ViewtypeFile)}))
	for i := 0; i < 3; i++ {
		m, ok := nextIncoming(usr, uAcc, 2*time.Minute)
		if !ok {
			logf("user: file not received")
			break
		}
		logf("user got viewType=%s fileName=%v mime=%v bytes=%d text=%q", m.ViewType, deref(m.FileName), deref(m.FileMime), m.FileBytes, m.Text)
	}

	logf("== 5. broadcast channel")
	bc := must(bot.rpc.CreateBroadcast(bAcc, "platform status (test)"))
	bLink := must(bot.rpc.GetChatSecurejoinQrCode(bAcc, &bc))
	bqr := must(usr.rpc.CheckQr(uAcc, bLink))
	logf("channel chat=%d link prefix %s… kind(at user)=%s", bc, bLink[:30], bqr.GetKind())
	logf("AddContactToChat(channel, user) without user action: err=%v", bot.rpc.AddContactToChat(bAcc, bc, uOnBot))
	for i := 1; i <= 3; i++ {
		must(bot.rpc.MiscSendTextMessage(bAcc, bc, fmt.Sprintf("post #%d before anyone joined", i)))
	}
	time.Sleep(5 * time.Second)
	bot.takeSmtp()
	for _, a := range []uint32{uAcc, u2Acc} {
		tj := time.Now()
		ch := must(usr.rpc.SecureJoin(a, bLink))
		got := 0
		for got < 3 {
			m, ok := nextIncoming(usr, a, 3*time.Minute)
			if !ok {
				break
			}
			if strings.HasPrefix(m.Text, "post #") {
				got++
			}
			logf("  acc%d got %q in chat %d (info=%v)", a, m.Text, m.ChatId, m.IsInfo)
		}
		full := must(usr.rpc.GetFullChatById(a, ch))
		logf("acc%d joined channel in %.1fs: chatType=%s canSend=%v contactIds=%v old posts received=%d", a, time.Since(tj).Seconds(), full.ChatType, full.CanSend, full.ContactIds, got)
	}
	time.Sleep(10 * time.Second)
	sent, _ := bot.takeSmtp()
	logf("bot SMTP sends caused by 2 joins: %d", len(sent))
	for _, s := range sent {
		logf("  %s", s)
	}
	logf("bot sees channel members: %v", must(bot.rpc.GetChatContacts(bAcc, bc)))
	must(bot.rpc.MiscSendTextMessage(bAcc, bc, "Работоспособность сервиса восстановлена (test post)"))
	tp := time.Now()
	seen := map[uint32]bool{}
	for len(seen) < 2 {
		_, ok := usr.waitFor(0, 2*time.Minute, func(e dc.EventType) bool { return true })
		if !ok {
			break
		}
		for _, a := range []uint32{uAcc, u2Acc} {
			for _, id := range must(usr.rpc.GetFreshMsgs(a)) {
				m := must(usr.rpc.GetMessage(a, id))
				if !seen[a] && strings.HasPrefix(m.Text, "Работоспособность") {
					seen[a] = true
					logf("acc%d received post after %.1fs: %q", a, time.Since(tp).Seconds(), m.Text)
				}
			}
		}
	}
	time.Sleep(3 * time.Second)
	sent, _ = bot.takeSmtp()
	logf("bot SMTP sends for ONE channel post to 2 subscribers: %d", len(sent))
	for _, s := range sent {
		logf("  %s", s)
	}
	uCh := must(usr.rpc.GetChatlistEntries(uAcc, nil, nil, nil))
	for _, id := range uCh {
		ci := must(usr.rpc.GetBasicChatInfo(uAcc, id))
		if ci.ChatType == dc.ChatTypeInBroadcast {
			_, err := usr.rpc.MiscSendTextMessage(uAcc, id, "subscriber tries to write")
			logf("subscriber send into channel: err=%v", err)
		}
	}

	logf("== 6. burst: 25 messages to one user as fast as possible")
	bot.takeSmtp()
	usr.drain()
	tb := time.Now()
	for i := 1; i <= 25; i++ {
		must(bot.rpc.MiscSendTextMessage(bAcc, bChat, fmt.Sprintf("burst %02d", i)))
	}
	got := 0
	var last time.Duration
	for got < 25 {
		m, ok := nextIncoming(usr, uAcc, 5*time.Minute)
		if !ok {
			break
		}
		if strings.HasPrefix(m.Text, "burst") {
			got++
			last = time.Since(tb)
			if got == 1 || got == 10 || got == 11 || got == 25 {
				logf("  user got %s at %.1fs", m.Text, last.Seconds())
			}
		}
	}
	sent, warns := bot.takeSmtp()
	logf("burst: user received %d/25, last at %.1fs; bot SMTP sends=%d; 4xx warnings=%d", got, last.Seconds(), len(sent), len(warns))
	for i, w := range warns {
		if i < 5 {
			logf("  %s", redact(w))
		}
	}

	logf("== 7. resources: idle 60s")
	time.Sleep(5 * time.Second)
	s1 := rpcServerStats()
	time.Sleep(60 * time.Second)
	s2 := rpcServerStats()
	for pid, v := range s2 {
		logf("rpc-server pid=%d RSS=%d kB cpu ticks in 60s=%d (ticks are 1/100 s)", pid, v[0], v[1]-s1[pid][1])
	}

	logf("== 8. restart of the bot process with the same accounts dir")
	bot.close()
	tr := time.Now()
	bot = openNode("bot")
	defer bot.close()
	ids := must(bot.rpc.GetAllAccountIds())
	logf("after restart: accounts=%v configured=%v", ids, must(bot.rpc.IsConfigured(bAcc)))
	must(0, bot.rpc.StartIo(bAcc))
	bi := must(bot.rpc.GetBasicChatInfo(bAcc, bChat))
	logf("chat %d still there: type=%s name=%q; fingerprint unchanged: %v", bChat, bi.ChatType, redact(bi.Name), fingerprintOf(bot, bAcc, uOnBot) == fpr)
	must(bot.rpc.MiscSendTextMessage(bAcc, bChat, "after restart"))
	for {
		m, ok := nextIncoming(usr, uAcc, 2*time.Minute)
		if !ok || m.Text == "after restart" {
			logf("user got message after bot restart: %v (%.1fs after reopen)", ok, time.Since(tr).Seconds())
			break
		}
	}

	if *relay2 != "" {
		logf("== 9. user moves to another relay (same key)")
		must(0, usr.rpc.AddTransportFromQr(uAcc, "dcaccount:"+*relay2))
		var newAddr string
		for _, t := range must(usr.rpc.ListTransports(uAcc)) {
			if t.Addr != addr(usr, uAcc) {
				newAddr = t.Addr
			}
		}
		must(0, usr.rpc.SetConfig(uAcc, "configured_addr", &newAddr))
		logf("user primary address now on %s", *relay2)
		must(usr.rpc.MiscSendTextMessage(uAcc, uChat, "/status from new relay"))
		m, ok := nextIncoming(bot, bAcc, 3*time.Minute)
		if ok {
			c2 := must(bot.rpc.GetContact(bAcc, m.FromId))
			logf("bot got %q from contactId=%d (old %d) addr on new relay=%v fingerprint same=%v",
				m.Text, m.FromId, uOnBot, strings.HasSuffix(c2.Address, "@"+*relay2), fingerprintOf(bot, bAcc, m.FromId) == fpr)
		} else {
			logf("bot: no message from moved user")
		}
	}
	logf("done")
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
