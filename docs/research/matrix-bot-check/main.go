// Command matrixbotcheck is a throwaway live check for research ticket #275
// (Matrix bot for the subscriber cabinet and the platform status room).
//
// It talks to two local homeservers (hs1.test, hs2.test) that federate with
// each other through an nginx front on :443, and checks:
//   - DM started by the bot (invite) and by the user (link flow), federation;
//   - invite blocking (m.invite_permission_config);
//   - HTML formatted_body, m.file (.conf), m.image (PNG), authenticated media;
//   - read-only status room: power levels, alias, member visibility, peeking;
//   - send rate limits;
//   - E2EE with mautrix-go (goolm): encrypted DM created by the user,
//     cross-signing, restart, lost crypto store, restore with the recovery key,
//     identity reset; a bot without E2EE in an encrypted DM.
//
// Build with: go run -tags goolm . (CGO is needed only for the sqlite store).
// Secrets (passwords, tokens, recovery key, crypto stores) live in -state and
// must never be committed.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var (
	hs1URL   = flag.String("hs1", "http://127.0.0.1:18008", "client URL of hs1 (bot's homeserver)")
	hs2URL   = flag.String("hs2", "http://127.0.0.1:28008", "client URL of hs2 (remote user's homeserver)")
	dom1     = flag.String("dom1", "hs1.test", "server_name of hs1")
	dom2     = flag.String("dom2", "hs2.test", "server_name of hs2")
	stateDir = flag.String("state", "./state", "directory for credentials and crypto stores (never commit)")
	burstN   = flag.Int("burst", 200, "messages in the rate-limit burst")
	verbose  = flag.Bool("v", false, "mautrix debug logs")
)

// ---------- output ----------

func res(tag, id, format string, args ...any) {
	fmt.Printf("[%s] %s: %s\n", tag, id, fmt.Sprintf(format, args...))
}
func pass(id, f string, a ...any) { res("PASS", id, f, a...) }
func fail(id, f string, a ...any) { res("FAIL", id, f, a...) }
func info(id, f string, a ...any) { res("INFO", id, f, a...) }

func check(id string, err error, f string, a ...any) bool {
	if err != nil {
		fail(id, "%s: %v", fmt.Sprintf(f, a...), err)
		return false
	}
	pass(id, f, a...)
	return true
}

func nonce() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- accounts ----------

type account struct {
	URL      string      `json:"url"`
	User     id.UserID   `json:"user"`
	Password string      `json:"password"`
	Token    string      `json:"token"`
	Device   id.DeviceID `json:"device"`
}

type accounts map[string]*account

func loadAccounts() accounts {
	a := accounts{}
	b, err := os.ReadFile(filepath.Join(*stateDir, "accounts.json"))
	if err == nil {
		_ = json.Unmarshal(b, &a)
	}
	return a
}

func (a accounts) save() {
	b, _ := json.MarshalIndent(a, "", "  ")
	must(os.WriteFile(filepath.Join(*stateDir, "accounts.json"), b, 0o600))
}

func register(ctx context.Context, url, local, regToken string) (*account, error) {
	cli, _ := mautrix.NewClient(url, "", "")
	pw := nonce() + nonce() + nonce()
	req := &mautrix.ReqRegister[any]{Username: local, Password: pw, InitialDeviceDisplayName: "check"}
	resp, uia, err := cli.Register(ctx, req)
	if err != nil && uia == nil {
		return nil, err
	}
	if resp == nil {
		req.Auth = map[string]any{"type": "m.login.registration_token", "token": regToken, "session": uia.Session}
		resp, _, err = cli.Register(ctx, req)
		if err != nil {
			return nil, err
		}
	}
	return &account{URL: url, User: resp.UserID, Password: pw, Token: resp.AccessToken, Device: resp.DeviceID}, nil
}

func ensureAccounts(ctx context.Context) accounts {
	accs := loadAccounts()
	type spec struct{ name, url, tokEnv string }
	specs := []spec{
		{"admin1", *hs1URL, "HS1_FIRST_TOKEN"},
		{"bot", *hs1URL, "HS1_TOKEN"},
		{"botx", *hs1URL, "HS1_TOKEN"},
		{"u1", *hs1URL, "HS1_TOKEN"},
		{"u2", *hs1URL, "HS1_TOKEN"},
		{"u3", *hs1URL, "HS1_TOKEN"},
		{"admin2", *hs2URL, "HS2_FIRST_TOKEN"},
		{"r1", *hs2URL, "HS2_TOKEN"},
	}
	for _, s := range specs {
		if accs[s.name] != nil {
			continue
		}
		t0 := time.Now()
		acc, err := register(ctx, s.url, s.name, os.Getenv(s.tokEnv))
		if !check("S1.register", err, "register %s with registration token (%s)", s.name, time.Since(t0).Round(time.Millisecond)) {
			os.Exit(1)
		}
		accs[s.name] = acc
		accs.save()
	}
	return accs
}

// ---------- peers (a client with a sync loop and an event log) ----------

type peer struct {
	name   string
	acc    *account
	cli    *mautrix.Client
	ch     *cryptohelper.CryptoHelper
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	events   []*event.Event
	invites  []*event.Event
	utd      int
	oldUTD   int // UTDs on events older than this client's start (history)
	started  time.Time
	autoJoin bool
}

func logger() zerolog.Logger {
	if *verbose {
		return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	}
	return zerolog.Nop()
}

// newPeer starts a client. If cryptoDB is set, it runs E2EE through
// cryptohelper; with login=true it logs in with the password (reusing the
// device ID stored in cryptoDB, if any), otherwise it reuses the access token.
func newPeer(ctx context.Context, name string, acc *account, cryptoDB string, login bool, autoJoin bool) (*peer, error) {
	p := &peer{name: name, acc: acc, done: make(chan struct{}), autoJoin: autoJoin, started: time.Now()}
	var err error
	if login {
		p.cli, err = mautrix.NewClient(acc.URL, acc.User, "")
	} else {
		p.cli, err = mautrix.NewClient(acc.URL, acc.User, acc.Token)
		p.cli.DeviceID = acc.Device
	}
	if err != nil {
		return nil, err
	}
	p.cli.Log = logger()
	p.cli.DefaultHTTPRetries = 3
	syncer := p.cli.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, p.onEvent)
	syncer.OnEventType(event.EventEncrypted, func(ctx context.Context, evt *event.Event) {
		if p.ch == nil { // no E2EE: keep the ciphertext to show what the bot sees
			p.onEvent(ctx, evt)
		}
	})
	syncer.OnEventType(event.StateMember, p.onMember)
	if cryptoDB != "" {
		p.ch, err = cryptohelper.NewCryptoHelper(p.cli, []byte("research-275-pickle"), cryptoDB)
		if err != nil {
			return nil, err
		}
		if login {
			p.ch.LoginAs = &mautrix.ReqLogin{
				Type:                     mautrix.AuthTypePassword,
				Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: acc.User.String()},
				Password:                 acc.Password,
				InitialDeviceDisplayName: name,
			}
		}
		p.ch.DecryptErrorCallback = func(evt *event.Event, err error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			if time.UnixMilli(evt.Timestamp).Before(p.started) {
				p.oldUTD++ // history from before this device existed: expected, a bot must ignore it
				return
			}
			p.utd++
			info("UTD", "%s could not decrypt live event %s from %s: %v", p.name, evt.ID, evt.Sender, err)
		}
		if err = p.ch.Init(ctx); err != nil {
			return nil, err
		}
		p.cli.Crypto = p.ch
	} else {
		p.cli.StateStore = mautrix.NewMemoryStateStore()
		syncer.OnEvent(p.cli.StateStoreSyncHandler)
	}
	sctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	go func() {
		defer close(p.done)
		err := p.cli.SyncWithContext(sctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			info("sync", "%s sync stopped: %v", p.name, err)
		}
	}()
	return p, nil
}

func (p *peer) onEvent(_ context.Context, evt *event.Event) {
	p.mu.Lock()
	p.events = append(p.events, evt)
	p.mu.Unlock()
}

func (p *peer) onMember(ctx context.Context, evt *event.Event) {
	if evt.GetStateKey() != p.cli.UserID.String() || evt.Content.AsMember().Membership != event.MembershipInvite {
		return
	}
	p.mu.Lock()
	p.invites = append(p.invites, evt)
	p.mu.Unlock()
	if p.autoJoin {
		go func() { _, _ = p.cli.JoinRoomByID(ctx, evt.RoomID) }()
	}
}

func (p *peer) stop() {
	p.cancel()
	<-p.done
	if p.ch != nil {
		_ = p.ch.Close()
	}
}

func waitIn(list func() []*event.Event, timeout time.Duration, pred func(*event.Event) bool) (*event.Event, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range list() {
			if pred(e) {
				return e, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("timeout after %s", timeout)
}

func (p *peer) waitEvent(timeout time.Duration, pred func(*event.Event) bool) (*event.Event, error) {
	return waitIn(func() []*event.Event {
		p.mu.Lock()
		defer p.mu.Unlock()
		return append([]*event.Event(nil), p.events...)
	}, timeout, pred)
}

func (p *peer) waitInvite(timeout time.Duration, room id.RoomID) (*event.Event, error) {
	return waitIn(func() []*event.Event {
		p.mu.Lock()
		defer p.mu.Unlock()
		return append([]*event.Event(nil), p.invites...)
	}, timeout, func(e *event.Event) bool { return room == "" || e.RoomID == room })
}

func bodyHas(room id.RoomID, s string) func(*event.Event) bool {
	return func(e *event.Event) bool {
		if e.RoomID != room || e.Type != event.EventMessage {
			return false
		}
		m := e.Content.AsMessage()
		return m != nil && strings.Contains(m.Body, s)
	}
}

func waitJoined(ctx context.Context, cli *mautrix.Client, room id.RoomID, user id.UserID, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := cli.JoinedMembers(ctx, room)
		if err == nil {
			if _, ok := r.Joined[user]; ok {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s not joined %s after %s", user, room, timeout)
}

func errCode(err error) string {
	var he mautrix.HTTPError
	if errors.As(err, &he) && he.RespError != nil {
		return fmt.Sprintf("%d %s", he.RespError.StatusCode, he.RespError.ErrCode)
	}
	var re mautrix.RespError
	if errors.As(err, &re) {
		return fmt.Sprintf("%d %s", re.StatusCode, re.ErrCode)
	}
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// ---------- content ----------

const cabinetHTML = `<b>Моя подписка</b><br>` +
	`Пользователь: <code>alice</code><br>` +
	`Срок: <i>до 2026-11-20</i><br>` +
	`<a href="https://sub.example.invalid/s/abc">Страница подписки</a><br>` +
	`<pre><code>/configs — мои конфиги
/u2 — выбрать пользователя 2</code></pre>` +
	`Команды: /help /configs /u2`

func pngQR() []byte {
	img := image.NewGray(image.Rect(0, 0, 210, 210))
	for y := 0; y < 210; y++ {
		for x := 0; x < 210; x++ {
			v := uint8(255)
			if (x/10+y/10)%2 == 0 || (x < 70 && y < 70 && (x/10 == 0 || y/10 == 0)) {
				v = 0
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

const confText = "[Interface]\nPrivateKey = <redacted-test-key>\nAddress = 10.8.0.2/32\n\n[Peer]\nPublicKey = <test>\nEndpoint = vpn.example.invalid:443\n"

// ---------- scenarios ----------

func main() {
	flag.Parse()
	_ = os.MkdirAll(*stateDir, 0o700)
	ctx := context.Background()
	accs := ensureAccounts(ctx)

	bot, err := newPeer(ctx, "bot", accs["bot"], "", false, true)
	must(err)
	u1, err := newPeer(ctx, "u1", accs["u1"], "", false, false)
	must(err)
	u2, err := newPeer(ctx, "u2", accs["u2"], "", false, false)
	must(err)
	u3, err := newPeer(ctx, "u3", accs["u3"], "", false, false)
	must(err)
	r1, err := newPeer(ctx, "r1", accs["r1"], "", false, false)
	must(err)
	time.Sleep(time.Second)

	dm1 := scenarioBotInvite(ctx, bot, u1)
	dmR := scenarioFederatedDM(ctx, bot, r1)
	scenarioUserInitiated(ctx, bot, u2)
	scenarioInviteBlocking(ctx, bot, u3)
	scenarioFiles(ctx, bot, u1, r1, dm1, dmR)
	scenarioStatusRoom(ctx, bot, u1, u2, u3, r1)
	scenarioRateLimit(ctx, bot, u1, dm1)
	scenarioE2EE(ctx, accs, bot)

	for _, p := range []*peer{bot, u1, u2, u3, r1} {
		p.stop()
	}
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "VmHWM") || strings.HasPrefix(l, "VmRSS") {
				info("R1.mem", "whole check process (6 clients incl. 2 E2EE): %s", strings.Join(strings.Fields(l), " "))
			}
		}
	}
}

func must(err error) {
	if err != nil {
		fmt.Println("fatal:", err)
		os.Exit(1)
	}
}

// S2: the bot creates a DM and invites a local user (bot-initiated flow).
func scenarioBotInvite(ctx context.Context, bot, u1 *peer) id.RoomID {
	t0 := time.Now()
	room, err := bot.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{Preset: "trusted_private_chat", IsDirect: true, Invite: []id.UserID{u1.cli.UserID}})
	if !check("S2.create", err, "bot creates unencrypted DM and invites %s", u1.cli.UserID) {
		return ""
	}
	inv, err := u1.waitInvite(10*time.Second, room.RoomID)
	if check("S2.invite", err, "u1 sees the invite in /sync after %s", time.Since(t0).Round(time.Millisecond)) {
		info("S2.invite", "invite sender=%s is_direct=%v", inv.Sender, inv.Content.AsMember().IsDirect)
	}
	_, err = u1.cli.JoinRoomByID(ctx, room.RoomID)
	check("S2.join", err, "u1 accepts the invite")
	n := nonce()
	content := &event.MessageEventContent{MsgType: event.MsgText, Body: "Моя подписка " + n, Format: event.FormatHTML, FormattedBody: cabinetHTML + " " + n}
	_, err = bot.cli.SendMessageEvent(ctx, room.RoomID, event.EventMessage, content)
	check("S2.send", err, "bot sends the cabinet as HTML formatted_body")
	evt, err := u1.waitEvent(10*time.Second, bodyHas(room.RoomID, n))
	if check("S2.recv", err, "u1 receives it") {
		m := evt.Content.AsMessage()
		if m.FormattedBody == content.FormattedBody && m.Format == event.FormatHTML {
			pass("S2.html", "formatted_body arrives byte-identical (server does not sanitise; rendering is up to the client)")
		} else {
			fail("S2.html", "formatted_body changed: %q", m.FormattedBody)
		}
	}
	return room.RoomID
}

// S3: DM with a user on another homeserver (federation through :443 + .well-known).
func scenarioFederatedDM(ctx context.Context, bot, r1 *peer) id.RoomID {
	t0 := time.Now()
	room, err := bot.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{Preset: "trusted_private_chat", IsDirect: true, Invite: []id.UserID{r1.cli.UserID}})
	if !check("S3.create", err, "bot invites remote %s over federation (%s)", r1.cli.UserID, time.Since(t0).Round(time.Millisecond)) {
		return ""
	}
	_, err = r1.waitInvite(30*time.Second, room.RoomID)
	check("S3.invite", err, "remote user sees the invite after %s", time.Since(t0).Round(time.Millisecond))
	t1 := time.Now()
	_, err = r1.cli.JoinRoomByID(ctx, room.RoomID)
	check("S3.join", err, "remote user joins via federation (%s)", time.Since(t1).Round(time.Millisecond))
	n := nonce()
	t2 := time.Now()
	_, err = bot.cli.SendMessageEvent(ctx, room.RoomID, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgNotice, Body: "Подписка истекает через 3 дня " + n})
	check("S3.send", err, "bot sends m.notice")
	_, err = r1.waitEvent(30*time.Second, bodyHas(room.RoomID, n))
	check("S3.recv", err, "remote user receives it after %s", time.Since(t2).Round(time.Millisecond))
	return room.RoomID
}

// S4: the user starts the DM and sends a one-time code (link flow without bot invites).
func scenarioUserInitiated(ctx context.Context, bot, u2 *peer) {
	t0 := time.Now()
	room, err := u2.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{Preset: "trusted_private_chat", IsDirect: true, Invite: []id.UserID{bot.cli.UserID}})
	if !check("S4.create", err, "u2 creates a DM with the bot") {
		return
	}
	if err = waitJoined(ctx, u2.cli, room.RoomID, bot.cli.UserID, 10*time.Second); !check("S4.autojoin", err, "bot auto-accepts the invite (%s)", time.Since(t0).Round(time.Millisecond)) {
		return
	}
	code := nonce()
	_, err = u2.cli.SendText(ctx, room.RoomID, "/link "+code)
	check("S4.send", err, "u2 sends /link <code>")
	evt, err := bot.waitEvent(10*time.Second, bodyHas(room.RoomID, code))
	if check("S4.recv", err, "bot receives the code") {
		info("S4.identity", "event sender=%s (asserted by the homeserver of the sender; that is the proof of MXID ownership)", evt.Sender)
	}
}

// S5: invite blocking (spec v1.18 m.invite_permission_config / MSC4155).
func scenarioInviteBlocking(ctx context.Context, bot, u3 *peer) {
	try := func(label string, cfg map[string]any) {
		must(u3.cli.SetAccountData(ctx, "m.invite_permission_config", cfg))
		time.Sleep(300 * time.Millisecond)
		room, err := bot.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{Preset: "trusted_private_chat", IsDirect: true})
		must(err)
		_, err = bot.cli.InviteUser(ctx, room.RoomID, &mautrix.ReqInviteUser{UserID: u3.cli.UserID})
		_, seen := u3.waitInvite(3*time.Second, room.RoomID)
		info("S5."+label, "config=%v -> invite: %s, u3 sees invite: %v", cfg, errCode(err), seen == nil)
	}
	try("block-all", map[string]any{"default_action": "block"})
	try("block-all+allow-bot", map[string]any{"default_action": "block", "allowed_users": []string{bot.cli.UserID.String()}})
	try("block-servers+allow-bot", map[string]any{"blocked_servers": []string{"*"}, "allowed_users": []string{bot.cli.UserID.String()}})
	try("ignore-bot", map[string]any{"ignored_users": []string{bot.cli.UserID.String()}})
	try("reset", map[string]any{})
}

// S6: .conf as m.file, PNG as m.image, authenticated media download, legacy endpoint, federation.
func scenarioFiles(ctx context.Context, bot, u1, r1 *peer, dm1, dmR id.RoomID) {
	conf := []byte(confText)
	up, err := bot.cli.UploadBytesWithName(ctx, conf, "application/octet-stream", "alice-awg.conf")
	if !check("S6.upload", err, "upload .conf (%d B) -> %s", len(conf), up.ContentURI) {
		return
	}
	n := nonce()
	_, err = bot.cli.SendMessageEvent(ctx, dm1, event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgFile, Body: "alice-awg.conf", FileName: "alice-awg.conf", URL: up.ContentURI.CUString(),
		Info: &event.FileInfo{MimeType: "application/octet-stream", Size: len(conf)},
	})
	check("S6.file", err, "send m.file")
	evt, err := u1.waitEvent(10*time.Second, func(e *event.Event) bool {
		return e.RoomID == dm1 && e.Type == event.EventMessage && e.Content.AsMessage().MsgType == event.MsgFile
	})
	if check("S6.file.recv", err, "u1 receives m.file") {
		mxc, _ := evt.Content.AsMessage().URL.Parse()
		data, err := u1.cli.DownloadBytes(ctx, mxc)
		if check("S6.file.dl", err, "u1 downloads via /_matrix/client/v1/media (authenticated)") && !bytes.Equal(data, conf) {
			fail("S6.file.dl", "content differs")
		}
		legacy := fmt.Sprintf("%s/_matrix/media/v3/download/%s/%s", *hs1URL, mxc.Homeserver, mxc.FileID)
		if r, err := http.Get(legacy); err == nil {
			info("S6.legacy", "unauthenticated GET /_matrix/media/v3/download -> HTTP %d", r.StatusCode)
			r.Body.Close()
		}
		v1noauth := fmt.Sprintf("%s/_matrix/client/v1/media/download/%s/%s", *hs1URL, mxc.Homeserver, mxc.FileID)
		if r, err := http.Get(v1noauth); err == nil {
			info("S6.noauth", "GET /_matrix/client/v1/media/download without token -> HTTP %d (a link to the file is useless outside Matrix)", r.StatusCode)
			r.Body.Close()
		}
	}
	img := pngQR()
	upi, err := bot.cli.UploadBytesWithName(ctx, img, "image/png", "qr.png")
	must(err)
	for _, room := range []id.RoomID{dm1, dmR} {
		_, err = bot.cli.SendMessageEvent(ctx, room, event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgImage, Body: "qr.png " + n, FileName: "qr.png", URL: upi.ContentURI.CUString(),
			Info: &event.FileInfo{MimeType: "image/png", Size: len(img), Width: 210, Height: 210},
		})
		check("S6.image", err, "send m.image to %s", room)
	}
	t0 := time.Now()
	evt, err = r1.waitEvent(30*time.Second, bodyHas(dmR, n))
	if check("S6.image.fed", err, "remote user receives m.image") {
		mxc, _ := evt.Content.AsMessage().URL.Parse()
		data, err := r1.cli.DownloadBytes(ctx, mxc)
		if check("S6.image.fed.dl", err, "remote user downloads it through its own homeserver (federated authenticated media, %s)", time.Since(t0).Round(time.Millisecond)) && !bytes.Equal(data, img) {
			fail("S6.image.fed.dl", "content differs")
		}
	}
}

// S7: read-only public status room.
func scenarioStatusRoom(ctx context.Context, bot, u1, u2, u3, r1 *peer) {
	alias := "status" + nonce()
	pl := &event.PowerLevelsEventContent{
		Users:         map[id.UserID]int{bot.cli.UserID: 100},
		UsersDefault:  0,
		EventsDefault: 50,
		Events:        map[string]int{"m.reaction": 0},
	}
	room, err := bot.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Preset: "public_chat", Visibility: "public", RoomAliasName: alias, Name: "Platform status",
		Topic: "Объявления о доступности сервиса", PowerLevelOverride: pl,
		InitialState: []*event.Event{
			{Type: event.StateHistoryVisibility, StateKey: ptr(""), Content: event.Content{Parsed: &event.HistoryVisibilityEventContent{HistoryVisibility: event.HistoryVisibilityWorldReadable}}},
			{Type: event.StateGuestAccess, StateKey: ptr(""), Content: event.Content{Parsed: &event.GuestAccessEventContent{GuestAccess: event.GuestAccessForbidden}}},
		},
	})
	if !check("S7.create", err, "bot creates public room #%s:%s (events_default=50, bot=100, reactions=0)", alias, *dom1) {
		return
	}
	full := id.RoomAlias("#" + alias + ":" + *dom1)
	info("S7.link", "matrix.to link: https://matrix.to/#/%s", strings.ReplaceAll(string(full), "#", "%23"))
	for _, p := range []*peer{u1, u2} {
		_, err = p.cli.JoinRoom(ctx, string(full), nil)
		check("S7.join", err, "%s joins by alias", p.name)
	}
	t0 := time.Now()
	_, err = r1.cli.JoinRoom(ctx, string(full), nil)
	check("S7.join.fed", err, "remote user joins by alias over federation (%s)", time.Since(t0).Round(time.Millisecond))
	_, err = u1.cli.SendText(ctx, room.RoomID, "hello")
	info("S7.readonly", "subscriber posts a message -> %s", errCode(err))
	n := nonce()
	_, err = bot.cli.SendMessageEvent(ctx, room.RoomID, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: "⚠️ Наблюдаются нарушения связности сети " + n})
	check("S7.post", err, "bot posts an announcement (one event, regardless of N)")
	_, err = r1.waitEvent(30*time.Second, bodyHas(room.RoomID, n))
	check("S7.post.fed", err, "remote member receives it")
	jm, err := u2.cli.JoinedMembers(ctx, room.RoomID)
	if check("S7.members", err, "subscriber u2 asks /joined_members") {
		var names []string
		for u := range jm.Joined {
			names = append(names, u.String())
		}
		info("S7.members", "u2 sees %d members: %s", len(names), strings.Join(names, ", "))
	}
	// Peeking: a non-member reading a world_readable room.
	msgs, err := u3.cli.Messages(ctx, room.RoomID, "", "", mautrix.DirectionBackward, nil, 20)
	info("S7.peek.messages", "non-member u3 /messages on world_readable room -> %s (%d events)", errCode(err), lenChunk(msgs))
	mem, err := u3.cli.Members(ctx, room.RoomID)
	cnt := 0
	if mem != nil {
		cnt = len(mem.Chunk)
	}
	info("S7.peek.members", "non-member u3 /members -> %s (%d member events)", errCode(err), cnt)
	// A hidden-membership alternative: world_readable but nobody joins.
	_, err = r1.cli.Messages(ctx, room.RoomID, "", "", mautrix.DirectionBackward, nil, 5)
	info("S7.peek.fed", "remote member /messages -> %s", errCode(err))
}

func lenChunk(m *mautrix.RespMessages) int {
	if m == nil {
		return 0
	}
	return len(m.Chunk)
}

func ptr[T any](v T) *T { return &v }

// S8: how fast can the bot send before the homeserver says 429.
func scenarioRateLimit(ctx context.Context, bot, u1 *peer, room id.RoomID) {
	bot.cli.DefaultHTTPRetries = 0
	defer func() { bot.cli.DefaultHTTPRetries = 3 }()
	n := nonce()
	t0 := time.Now()
	limited, ok := 0, 0
	for i := 0; i < *burstN; i++ {
		_, err := bot.cli.SendMessageEvent(ctx, room, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgNotice, Body: fmt.Sprintf("burst %s %d", n, i)})
		switch {
		case err == nil:
			ok++
		case errors.Is(err, mautrix.MLimitExceeded):
			limited++
		default:
			info("S8.err", "%v", err)
		}
	}
	info("S8.burst", "%d messages back-to-back in %s: ok=%d, 429=%d", *burstN, time.Since(t0).Round(time.Millisecond), ok, limited)
	_, err := u1.waitEvent(30*time.Second, bodyHas(room, fmt.Sprintf("burst %s %d", n, *burstN-1)))
	check("S8.recv", err, "u1 received the last burst message")
	t1 := time.Now()
	lim := 0
	for i := 0; i < 30; i++ {
		r, err := bot.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{Preset: "trusted_private_chat", IsDirect: true})
		if err != nil {
			if errors.Is(err, mautrix.MLimitExceeded) {
				lim++
			}
			continue
		}
		_, err = bot.cli.InviteUser(ctx, r.RoomID, &mautrix.ReqInviteUser{UserID: u1.cli.UserID})
		if errors.Is(err, mautrix.MLimitExceeded) {
			lim++
		}
	}
	info("S8.invites", "30 createRoom+invite in %s: 429=%d", time.Since(t1).Round(time.Millisecond), lim)
}

// S9: E2EE.
func scenarioE2EE(ctx context.Context, accs accounts, plainBot *peer) {
	dir := *stateDir
	botDB := filepath.Join(dir, "botx-crypto.db")
	userDB := filepath.Join(dir, "u2-crypto.db")
	for _, f := range []string{botDB, userDB} {
		_ = os.Remove(f)
		_ = os.Remove(f + "-wal")
		_ = os.Remove(f + "-shm")
	}

	botx, err := newPeer(ctx, "botx", accs["botx"], botDB, true, true)
	if !check("E1.bot", err, "bot with E2EE (goolm + sqlite crypto store) logs in, device %s", devOf(botx)) {
		return
	}
	user, err := newPeer(ctx, "u2e", accs["u2"], userDB, true, false)
	if !check("E1.user", err, "test user with E2EE logs in, device %s", devOf(user)) {
		return
	}

	// The user creates an encrypted DM, as Element X does by default.
	room, err := user.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Preset: "trusted_private_chat", IsDirect: true, Invite: []id.UserID{botx.cli.UserID},
		InitialState: []*event.Event{{Type: event.StateEncryption, StateKey: ptr(""), Content: event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}}}},
	})
	if !check("E1.room", err, "user creates an encrypted DM with the bot") {
		return
	}
	must(waitJoined(ctx, user.cli, room.RoomID, botx.cli.UserID, 10*time.Second))
	waitEncrypted(ctx, user, room.RoomID)
	waitEncrypted(ctx, botx, room.RoomID)

	roundTrip := func(tag string, from, to *peer) bool {
		n := nonce()
		_, err := from.cli.SendMessageEvent(ctx, room.RoomID, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: tag + " " + n})
		if err != nil {
			fail(tag, "%s send: %v", from.name, err)
			return false
		}
		evt, err := to.waitEvent(15*time.Second, bodyHas(room.RoomID, n))
		if err != nil {
			fail(tag, "%s -> %s: not decrypted: %v", from.name, to.name, err)
			return false
		}
		pass(tag, "%s -> %s decrypted (source=%v)", from.name, to.name, evt.Mautrix.EventSource&event.SourceDecrypted != 0)
		return true
	}
	trust := func(tag string, p *peer) {
		devs, err := user.ch.Machine().FetchKeys(ctx, []id.UserID{p.cli.UserID}, true)
		if err != nil {
			fail(tag, "fetch keys: %v", err)
			return
		}
		d := devs[p.cli.UserID][p.cli.DeviceID]
		if d == nil {
			d, err = user.ch.Machine().GetOrFetchDevice(ctx, p.cli.UserID, p.cli.DeviceID)
			if err != nil {
				fail(tag, "device: %v", err)
				return
			}
		}
		ts, _ := user.ch.Machine().ResolveTrustContext(ctx, d)
		info(tag, "user's view of bot device %s: trust=%s (bot has %d devices with keys)", p.cli.DeviceID, ts, len(devs[p.cli.UserID]))
	}

	roundTrip("E2.user->bot", user, botx)
	roundTrip("E2.bot->user", botx, user)
	trust("E2.trust.nocs", botx)

	// Encrypted attachment (.conf) into the encrypted DM.
	ef := attachment.NewEncryptedFile()
	cipher := ef.Encrypt([]byte(confText))
	up, err := botx.cli.UploadBytes(ctx, cipher, "application/octet-stream")
	if check("E2.file.upload", err, "upload encrypted .conf") {
		n := nonce()
		_, err = botx.cli.SendMessageEvent(ctx, room.RoomID, event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgFile, Body: "alice-awg.conf " + n, FileName: "alice-awg.conf",
			File: &event.EncryptedFileInfo{EncryptedFile: *ef, URL: up.ContentURI.CUString()},
			Info: &event.FileInfo{MimeType: "application/octet-stream", Size: len(confText)},
		})
		check("E2.file.send", err, "send encrypted m.file")
		evt, err := user.waitEvent(15*time.Second, bodyHas(room.RoomID, n))
		if check("E2.file.recv", err, "user receives encrypted m.file") {
			m := evt.Content.AsMessage()
			mxc, _ := m.File.URL.Parse()
			data, err := user.cli.DownloadBytes(ctx, mxc)
			if err == nil {
				err = m.File.DecryptInPlace(data)
			}
			if check("E2.file.dec", err, "user downloads and decrypts the attachment") && string(data) != confText {
				fail("E2.file.dec", "content differs")
			}
		}
	}

	// Cross-signing for the bot (so clients don't show "unverified device").
	rk, err := botx.ch.Machine().GenerateAndVerifyWithRecoveryKey(ctx)
	if err != nil {
		info("E3.xsign", "without UIA: %v; retrying with password", err)
		rk, _, err = botx.ch.Machine().GenerateAndUploadCrossSigningKeys(ctx, func(ui *mautrix.RespUserInteractive) any {
			return pwAuth(accs["botx"], ui.Session)
		}, "")
		if err == nil {
			err = botx.ch.Machine().SignOwnDevice(ctx, botx.ch.Machine().OwnIdentity())
		}
		if err == nil {
			err = botx.ch.Machine().SignOwnMasterKey(ctx)
		}
	}
	if check("E3.xsign", err, "bot generates cross-signing keys + SSSS recovery key and signs its device") {
		_ = os.WriteFile(filepath.Join(dir, "botx-recovery-key.txt"), []byte(rk), 0o600)
	}
	trust("E3.trust.cs", botx)
	roundTrip("E3.bot->user", botx, user)

	// Restart with the same store.
	oldDev := botx.cli.DeviceID
	botx.stop()
	t0 := time.Now()
	botx, err = newPeer(ctx, "botx", accs["botx"], botDB, true, true)
	if check("E4.restart", err, "bot restarts from the same crypto store (%s), device %s (same=%v)", time.Since(t0).Round(time.Millisecond), devOf(botx), botx != nil && botx.cli.DeviceID == oldDev) {
		time.Sleep(time.Second)
		roundTrip("E4.bot->user", botx, user)
		roundTrip("E4.user->bot", user, botx)
		trust("E4.trust", botx)
	}

	// Lost store, same device/token: what cryptohelper does.
	tok := botx.cli.AccessToken
	botx.stop()
	lostDB := filepath.Join(dir, "botx-lost.db")
	_ = os.Remove(lostDB)
	lostAcc := *accs["botx"]
	lostAcc.Token, lostAcc.Device = tok, oldDev
	_, err = newPeer(ctx, "botx-lost", &lostAcc, lostDB, false, true)
	info("E5.lost-same-device", "empty store + old token/device -> %v", err)

	// Restore on a "new machine": empty store, new login, recovery key.
	newDB := filepath.Join(dir, "botx-new.db")
	_ = os.Remove(newDB)
	botx, err = newPeer(ctx, "botx", accs["botx"], newDB, true, true)
	if check("E6.newdevice", err, "bot logs in with password into an empty store -> new device %s", devOf(botx)) {
		err = botx.ch.Machine().VerifyWithRecoveryKey(ctx, rk)
		check("E6.recover", err, "bot restores cross-signing from SSSS with the recovery key and signs the new device")
		deleteOtherDevices(ctx, botx)
		time.Sleep(time.Second)
		trust("E6.trust", botx)
		roundTrip("E6.bot->user", botx, user)
		roundTrip("E6.user->bot", user, botx)
	}

	// Identity reset: no recovery key, new cross-signing keys.
	botx.stop()
	resetDB := filepath.Join(dir, "botx-reset.db")
	_ = os.Remove(resetDB)
	botx, err = newPeer(ctx, "botx", accs["botx"], resetDB, true, true)
	if check("E7.newdevice", err, "bot loses store AND recovery key: new device %s", devOf(botx)) {
		_, _, err = botx.ch.Machine().GenerateAndUploadCrossSigningKeysWithPassword(ctx, accs["botx"].Password, "")
		info("E7.reset.helper", "mautrix GenerateAndUploadCrossSigningKeysWithPassword (legacy UIA \"user\" field) -> %s", errCode(err))
		_, _, err = botx.ch.Machine().GenerateAndUploadCrossSigningKeys(ctx, func(ui *mautrix.RespUserInteractive) any {
			return pwAuth(accs["botx"], ui.Session)
		}, "")
		if err == nil {
			err = botx.ch.Machine().SignOwnDevice(ctx, botx.ch.Machine().OwnIdentity())
		}
		check("E7.reset", err, "bot uploads NEW cross-signing keys (identity reset)")
		time.Sleep(time.Second)
		trust("E7.trust", botx)
		roundTrip("E7.bot->user", botx, user)
	}

	// A bot without E2EE in the encrypted DM.
	room2, err := user.cli.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Preset: "trusted_private_chat", IsDirect: true, Invite: []id.UserID{plainBot.cli.UserID},
		InitialState: []*event.Event{{Type: event.StateEncryption, StateKey: ptr(""), Content: event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}}}},
	})
	if check("E8.room", err, "user creates an encrypted DM with the bot that has no E2EE") {
		must(waitJoined(ctx, user.cli, room2.RoomID, plainBot.cli.UserID, 10*time.Second))
		waitEncrypted(ctx, user, room2.RoomID)
		n := nonce()
		_, err = user.cli.SendMessageEvent(ctx, room2.RoomID, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: "/start " + n})
		must(err)
		evt, err := plainBot.waitEvent(10*time.Second, func(e *event.Event) bool { return e.RoomID == room2.RoomID })
		if err == nil {
			info("E8.bot-sees", "plain bot receives event type %s (cannot read the command)", evt.Type.Type)
		} else {
			info("E8.bot-sees", "plain bot got nothing: %v", err)
		}
		n2 := nonce()
		_, err = plainBot.cli.SendMessageEvent(ctx, room2.RoomID, event.EventMessage, &event.MessageEventContent{MsgType: event.MsgText, Body: "plaintext reply " + n2})
		info("E8.plain-send", "plain bot sends unencrypted into the encrypted room -> %s", errCode(err))
		evt, err = user.waitEvent(10*time.Second, bodyHas(room2.RoomID, n2))
		if err == nil {
			info("E8.user-sees", "user receives plaintext event (decrypted flag=%v) — clients mark it as not encrypted", evt.Mautrix.EventSource&event.SourceDecrypted != 0)
		} else {
			info("E8.user-sees", "user did not receive: %v", err)
		}
	}

	botx.mu.Lock()
	bu, bo := botx.utd, botx.oldUTD
	botx.mu.Unlock()
	user.mu.Lock()
	uu := user.utd
	user.mu.Unlock()
	info("E9.utd", "live decryption failures: bot(last device)=%d user=%d; history UTDs on the bot's fresh device=%d", bu, uu, bo)
	botx.stop()
	user.stop()
}

// pwAuth is m.login.password UIA with the spec "identifier" field.
func pwAuth(acc *account, session string) map[string]any {
	return map[string]any{
		"type":       "m.login.password",
		"identifier": map[string]any{"type": "m.id.user", "user": acc.User.String()},
		"password":   acc.Password,
		"session":    session,
	}
}

// deleteOtherDevices removes the bot's stale devices (left by a lost store),
// so clients stop encrypting to them.
func deleteOtherDevices(ctx context.Context, p *peer) {
	devs, err := p.cli.GetDevicesInfo(ctx)
	if err != nil {
		fail("devices", "list: %v", err)
		return
	}
	var ids []id.DeviceID
	for _, d := range devs.Devices {
		if d.DeviceID != p.cli.DeviceID {
			ids = append(ids, d.DeviceID)
		}
	}
	// First call without auth to get the UIA session (401), then with the password.
	post := func(body any) (int, []byte) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.acc.URL+"/_matrix/client/v3/delete_devices", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+p.cli.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, []byte(err.Error())
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, out
	}
	code, out := post(map[string]any{"devices": ids})
	if code == http.StatusUnauthorized {
		var ui mautrix.RespUserInteractive
		_ = json.Unmarshal(out, &ui)
		code, out = post(map[string]any{"devices": ids, "auth": pwAuth(p.acc, ui.Session)})
	}
	if code == http.StatusOK {
		pass("devices", "bot deletes %d stale devices (UIA password)", len(ids))
	} else {
		fail("devices", "delete_devices -> HTTP %d %s", code, out)
	}
}

func devOf(p *peer) id.DeviceID {
	if p == nil {
		return ""
	}
	return p.cli.DeviceID
}

func waitEncrypted(ctx context.Context, p *peer, room id.RoomID) {
	for i := 0; i < 100; i++ {
		if enc, _ := p.cli.StateStore.IsEncrypted(ctx, room); enc {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	info("wait", "%s: room %s not known as encrypted", p.name, room)
}
