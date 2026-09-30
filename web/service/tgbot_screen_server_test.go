package service

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/xray"
)

// serverScreenFixture is usersBotFixture with the server's status already
// in the cache, so the screen never asks the machine.
func serverScreenFixture(t *testing.T) (*Tgbot, *screenTelegram) {
	t.Helper()
	tg := usersBotFixture(t)
	tg.setCachedServerStats("🖥 STATUS OF THE SERVER\r\n")
	t.Cleanup(func() { tg.setCachedServerStats("") })
	return tg, withScreenTelegram(t)
}

// TestScreenServer: «⚙️ Server» is the server's status (the old usage and
// /status) with its actions, as the prototype lays them out.
func TestScreenServer(t *testing.T) {
	tg, fake := serverScreenFixture(t)

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "⚙️ Server")
	if !strings.Contains(fake.messages[1].text, "STATUS OF THE SERVER") {
		t.Errorf("server: %q", fake.messages[1].text)
	}
	want := []string{"💾 DB backup", "🔄 Restart xray", "🔗 Chain", "🚫 Ban logs", "♻️ Reset all traffic", "🔄 Refresh",
		"⬅️ Back", "🏠 Menu"}
	if got := fake.messages[1].labels; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("server buttons:\n got %q\nwant %q", got, want)
	}
	fake.press(t, tg, 1, "🔗 Chain")
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(fake.messages[1].text, "STATUS OF THE SERVER") {
		t.Errorf("back from the chain: %q", fake.messages[1].text)
	}

	// /status is the shortcut to it.
	adminCommand(tg, "/status")
	if m := fake.messages[fake.next]; !strings.Contains(m.text, "STATUS OF THE SERVER") || len(fake.live()) != 1 {
		t.Errorf("/status: %q, the chat shows %v", m.text, fake.live())
	}
}

// TestScreenServerFiles: the backup and the ban logs go to the admin's chat
// as files below the screen, which stays as it is.
func TestScreenServerFiles(t *testing.T) {
	tg, fake := serverScreenFixture(t)
	dbDir, logDir := t.TempDir(), t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("XUI_LOG_FOLDER", logDir)
	t.Setenv("XUI_BIN_FOLDER", t.TempDir())
	for path, body := range map[string]string{
		config.GetDBPath():                 "db",
		xray.GetIPLimitBannedLogPath():     "banned 1.2.3.4",
		xray.GetIPLimitBannedPrevLogPath(): "banned 5.6.7.8",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	documents := func() int {
		n := 0
		for _, c := range fake.calls {
			if c == "sendDocument" {
				n++
			}
		}
		return n
	}

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "⚙️ Server")
	fake.press(t, tg, 1, "💾 DB backup")
	if n := documents(); n != 1 {
		t.Errorf("backup: %d files sent, want the database (calls %q)", n, fake.calls)
	}
	fake.press(t, tg, 1, "🚫 Ban logs")
	if n := documents(); n != 3 {
		t.Errorf("ban logs: %d files sent in all, want 3 (calls %q)", n, fake.calls)
	}
	if !strings.Contains(fake.messages[1].text, "STATUS OF THE SERVER") {
		t.Errorf("the screen changed: %q", fake.messages[1].text)
	}
}

// TestScreenServerRestartXray: «🔄 Restart xray» asks first; only the
// confirmation restarts it, and the server screen then says how it went.
func TestScreenServerRestartXray(t *testing.T) {
	tg, fake := serverScreenFixture(t)
	restarts := 0
	result := error(nil)
	prev := screenRestartXray
	screenRestartXray = func(*Tgbot) (bool, error) { restarts++; return true, result }
	t.Cleanup(func() { screenRestartXray = prev })

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "⚙️ Server")
	fake.press(t, tg, 1, "🔄 Restart xray")
	if m := fake.messages[1]; !strings.Contains(m.text, "Restart xray?") || strings.Join(m.labels, "|") != "✅ Confirm|⬅️ Back|🏠 Menu" {
		t.Fatalf("confirmation: %q %q", m.text, m.labels)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	if restarts != 0 || !strings.Contains(fake.messages[1].text, "STATUS OF THE SERVER") {
		t.Fatalf("back: %d restarts, %q", restarts, fake.messages[1].text)
	}

	fake.press(t, tg, 1, "🔄 Restart xray")
	fake.press(t, tg, 1, "✅ Confirm")
	if m := fake.messages[1]; restarts != 1 || !strings.Contains(m.text, "✅ Xray restarted.") || !strings.Contains(m.text, "STATUS OF THE SERVER") {
		t.Errorf("after the restart: %d restarts, %q", restarts, m.text)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	if !strings.Contains(fake.messages[1].text, "Main menu") {
		t.Errorf("back after the restart: %q", fake.messages[1].text)
	}

	result = errors.New("boom")
	fake.press(t, tg, 1, "⚙️ Server")
	fake.press(t, tg, 1, "🔄 Restart xray")
	fake.press(t, tg, 1, "✅ Confirm")
	if m := fake.messages[1]; !strings.Contains(m.text, "boom") {
		t.Errorf("a failed restart: %q", m.text)
	}

	screenRestartXray = prev // the real one: no xray in a test
	reply := screenPressData(t, tg, "s_rxc")
	if !strings.Contains(reply.text, "Xray Core is not running") {
		t.Errorf("xray not running: %q", reply.text)
	}

	// /restart is the shortcut to the confirmation.
	adminCommand(tg, "/restart")
	if m := fake.messages[fake.next]; !strings.Contains(m.text, "Restart xray?") || len(fake.live()) != 1 {
		t.Errorf("/restart: %q, the chat shows %v", m.text, fake.live())
	}
}

// TestScreenServerResetAllTraffic: «♻️ Reset all traffic» asks first; the
// confirmation zeroes the traffic of every user's clients, xray and tunnel,
// and leaves monitoring's probes alone.
func TestScreenServerResetAllTraffic(t *testing.T) {
	tg, fake := serverScreenFixture(t)
	const gb = int64(1) << 30
	ivan := mustCreateUser(t, SubUserCreate{Name: "ivan", InboundIds: []int{2}})
	peer := awgPeer(t, 90, "stray-awg", "")
	probe := awgPeer(t, 91, ProbeTunnelEmail("mc1", "direct"), "")
	useTraffic(t, "ivan-de", gb, 2*gb)
	db := database.GetDB()
	if err := db.Model(&model.TunnelClient{}).Where("id IN ?", []int{peer.Id, probe.Id}).
		Updates(map[string]any{"upload": gb, "download": gb}).Error; err != nil {
		t.Fatal(err)
	}

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "⚙️ Server")
	fake.press(t, tg, 1, "♻️ Reset all traffic")
	if m := fake.messages[1]; !strings.Contains(m.text, "Reset the traffic of all clients?") || strings.Join(m.labels, "|") != "✅ Confirm|⬅️ Back|🏠 Menu" {
		t.Fatalf("confirmation: %q %q", m.text, m.labels)
	}
	fake.press(t, tg, 1, "⬅️ Back")
	if v, _ := (&SubUserService{}).Get(ivan.SubId); v.Up+v.Down != 3*gb {
		t.Fatalf("back reset the traffic: %d", v.Up+v.Down)
	}

	fake.press(t, tg, 1, "♻️ Reset all traffic")
	fake.press(t, tg, 1, "✅ Confirm")
	if m := fake.messages[1]; !strings.Contains(m.text, "♻️ Traffic reset for") || !strings.Contains(m.text, "STATUS OF THE SERVER") {
		t.Errorf("after the reset: %q", m.text)
	}
	if v, _ := (&SubUserService{}).Get(ivan.SubId); v.Up+v.Down != 0 {
		t.Errorf("ivan still has %d bytes", v.Up+v.Down)
	}
	var left []model.TunnelClient
	db.Where("id IN ?", []int{peer.Id, probe.Id}).Order("id").Find(&left)
	if len(left) != 2 || left[0].Upload+left[0].Download != 0 || left[1].Upload+left[1].Download != 2*gb {
		t.Errorf("tunnel clients after the reset: %+v", left)
	}
}

// TestScreenAdminLink: «🔧 Admin panel» sends the panel's address as a
// message of its own, set to be deleted in five minutes; the screen stays.
func TestScreenAdminLink(t *testing.T) {
	tg, fake := serverScreenFixture(t)
	setSetting(t, "webDomain", "panel.example.com")
	setSetting(t, "webPort", "2053")
	setSetting(t, "webBasePath", "/secret/")
	setSetting(t, "webCertFile", "/etc/cert.pem")
	setSetting(t, "webKeyFile", "/etc/key.pem")
	var delays []time.Duration
	var deletes []func()
	prev := tgbotDeleteAfter
	tgbotDeleteAfter = func(d time.Duration, f func()) { delays, deletes = append(delays, d), append(deletes, f) }
	t.Cleanup(func() { tgbotDeleteAfter = prev })

	adminCommand(tg, "/start")
	fake.press(t, tg, 1, "🔧 Admin panel")
	if fake.next != 2 || !strings.Contains(fake.messages[2].text, "https://panel.example.com:2053/secret/") {
		t.Fatalf("the link: %+v (calls %q)", fake.messages[2], fake.calls)
	}
	if !strings.Contains(fake.messages[1].text, "Main menu") {
		t.Errorf("the screen changed: %q", fake.messages[1].text)
	}
	if !slices.Equal(delays, []time.Duration{5 * time.Minute}) {
		t.Fatalf("deletion timers: %v", delays)
	}
	deletes[0]()
	if !fake.messages[2].deleted || fake.messages[1].deleted {
		t.Errorf("after five minutes the chat shows %v", fake.live())
	}
}

// TestPanelURL: the address the panel knows itself by — its domain, else
// the address it listens on, else the server's public address — with its
// port (none when it is the scheme's own) and base path.
func TestPanelURL(t *testing.T) {
	tg := usersBotFixture(t)
	cases := []struct {
		name                           string
		domain, listen, port, cert, ip string
		want                           string
	}{
		{"domain with tls", "panel.example.com", "", "2053", "/c.pem", "", "https://panel.example.com:2053/b/"},
		{"tls on 443", "panel.example.com", "", "443", "/c.pem", "", "https://panel.example.com/b/"},
		{"listen address", "", "10.0.0.5", "80", "", "", "http://10.0.0.5/b/"},
		{"wildcard listen, public ip", "", "0.0.0.0", "2053", "", "203.0.113.7", "http://203.0.113.7:2053/b/"},
		{"ipv6 listen", "", "2001:db8::1", "2053", "", "", "http://[2001:db8::1]:2053/b/"},
	}
	setSetting(t, "webBasePath", "/b/")
	for _, tc := range cases {
		setSetting(t, "webDomain", tc.domain)
		setSetting(t, "webListen", tc.listen)
		setSetting(t, "webPort", tc.port)
		setSetting(t, "webCertFile", tc.cert)
		setSetting(t, "webKeyFile", tc.cert)
		tg.lastStatus = &Status{}
		tg.lastStatus.PublicIP.IPv4 = tc.ip
		if got := tg.panelURL(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
