package nginx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goldenJails is the panel's pair of jails: probes on the HTTP side and
// failed logins, with the guard's exemptions.
func goldenJails() Jails {
	return Jails{
		MissLog:   "/var/log/nginx/3ax-ui-miss.log",
		LoginLog:  "/var/log/x-ui/3xui.log",
		IgnoreIP:  []string{"203.0.113.9", "2001:db8::7", "198.51.100.0/24"},
		BanAction: "iptables-multiport",
	}
}

// TestGeneratedJails: the golden jail and filter files (#141).
func TestGeneratedJails(t *testing.T) {
	files := goldenJails().render()
	check(t, "fail2ban_jail_panel.conf", files[jailFile])
	check(t, "fail2ban_filter_probe.conf", files[probeFilterFile])
	check(t, "fail2ban_filter_login.conf", files[loginFilterFile])

	jail := files[jailFile]
	for _, want := range []string{
		"[3ax-ui-probe]\n", "[3ax-ui-login]\n",
		"maxretry          = 10\n", "maxretry          = 5\n",
		"findtime          = 10m\n", "bantime           = 1h\n",
		"bantime.increment = true\n", "bantime.maxtime   = 24h\n",
		"ignoreip          = 127.0.0.1/8 ::1 198.51.100.0/24 2001:db8::7 203.0.113.9\n",
		// A file log, whatever the distro made the default backend.
		"backend           = auto\n",
		// The operator's jail.local is read after this file and wins.
		"jail.local",
	} {
		if !strings.Contains(jail, want) {
			t.Errorf("the jail lacks %q", want)
		}
	}

	// A box has no login: one jail, no login filter.
	box := goldenJails()
	box.LoginLog = ""
	boxFiles := box.render()
	check(t, "fail2ban_jail_box.conf", boxFiles[jailFile])
	if _, ok := boxFiles[loginFilterFile]; ok || strings.Contains(boxFiles[jailFile], "3ax-ui-login") {
		t.Error("a box without a login got the login jail")
	}
}

// Sample lines as nginx (log_format threeax_miss/threeax_limit) and the
// panel's logger write them.
const (
	sampleMissLog = `198.51.100.7 [26/Sep/2026:21:54:29 +0000] miss "GET /base/panel/" 200
198.51.100.7 [26/Sep/2026:21:54:30 +0000] miss "GET /sub/unknown" 400
198.51.100.8 [26/Sep/2026:21:54:31 +0000] limit "POST /base/login" 405
2001:db8::1 [26/Sep/2026:21:54:32 +0000] miss "GET /x\x22 192.0.2.1 [" 200
`
	samplePanelLog = `2026/09/26 21:54:29 WARNING - wrong username: "admin", password: "x", IP: "198.51.100.9"
2026/09/26 21:54:30 WARNING - wrong username: "a&#34;, IP: &#34;192.0.2.66", password: "y", IP: "2001:db8::2"
2026/09/26 21:54:31 INFO - admin logged in successfully, Ip Address: 192.0.2.77
2026/09/26 21:54:32 WARNING - wrong username: "b", password: "z", IP: "unknown"
`
)

// TestFail2banFiltersMatchTheLogs runs fail2ban's own fail2ban-regex over the
// sample lines with the generated filters: every miss and every failed login
// names the client, and nothing a client typed can name another address.
func TestFail2banFiltersMatchTheLogs(t *testing.T) {
	bin, err := exec.LookPath("fail2ban-regex")
	if err != nil {
		t.Skip("fail2ban-regex is not installed on this machine")
	}
	dir := t.TempDir()
	files := goldenJails().render()
	for _, tc := range []struct {
		filter, log string
		want        []string
	}{
		{probeFilterFile, sampleMissLog, []string{"198.51.100.7", "198.51.100.7", "198.51.100.8", "2001:db8::1"}},
		{loginFilterFile, samplePanelLog, []string{"198.51.100.9", "2001:db8::2"}},
	} {
		filter := filepath.Join(dir, filepath.Base(tc.filter))
		logFile := filter + ".log"
		if err := os.WriteFile(filter, []byte(files[tc.filter]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(logFile, []byte(tc.log), 0o644); err != nil {
			t.Fatal(err)
		}
		// -o ip prints the address each matching line was blamed on.
		out, err := exec.Command(bin, "-o", "ip", logFile, filter).CombinedOutput()
		if err != nil {
			t.Fatalf("fail2ban-regex %s: %v\n%s", tc.filter, err, out)
		}
		if got := strings.Fields(string(out)); strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s blamed %v, want %v", tc.filter, got, tc.want)
		}
	}
}

// fakeFail2ban is fail2ban as the tests see it.
type fakeFail2ban struct {
	running  bool
	startErr error
	calls    []string

	// bare are the jails running without an action, the way fail2ban
	// leaves a jail whose action a reload renamed (#152). reloadStrips
	// makes the next reload do that to every jail; a restart brings the
	// actions back unless stuck. asked are the jails whose actions were
	// looked at; actionsErr is what that look answers, if not nil.
	bare         map[string]bool
	reloadStrips bool
	stuck        bool
	asked        []string
	actionsErr   error
}

func (f *fakeFail2ban) Running() bool { return f.running }
func (f *fakeFail2ban) Start() error {
	f.calls = append(f.calls, "start")
	if f.startErr != nil {
		return f.startErr
	}
	f.running = true
	return nil
}
func (f *fakeFail2ban) Reload() error {
	f.calls = append(f.calls, "reload")
	if f.reloadStrips {
		f.bare = map[string]bool{"3ax-ui-probe": true, "3ax-ui-login": true}
	}
	return nil
}
func (f *fakeFail2ban) Restart() error {
	f.calls = append(f.calls, "restart")
	if !f.stuck {
		f.bare = nil
	}
	return nil
}
func (f *fakeFail2ban) Actions(jail string) ([]string, error) {
	f.asked = append(f.asked, jail)
	if f.actionsErr != nil {
		return nil, f.actionsErr
	}
	if f.bare[jail] {
		return nil, nil
	}
	return []string{"iptables-multiport"}, nil
}

// useFail2ban points the package at a temporary /etc/fail2ban and a fake
// fail2ban.
func useFail2ban(t *testing.T, installed bool) (*fakeFail2ban, string) {
	t.Helper()
	root := t.TempDir()
	fake := &fakeFail2ban{}
	prevRoot, prevCtl, prevInstalled, prevRetry, prevChecked := Fail2banRoot, fail2banCtl, fail2banInstalled, jailsRetryAfter, jailsChecked
	Fail2banRoot, fail2banCtl = root, fake
	fail2banInstalled = func() bool { return installed }
	jailsRetryAfter, jailsChecked = time.Time{}, false
	t.Cleanup(func() {
		Fail2banRoot, fail2banCtl, fail2banInstalled, jailsRetryAfter, jailsChecked = prevRoot, prevCtl, prevInstalled, prevRetry, prevChecked
	})
	return fake, root
}

// TestApplyJails: the jails go in with the mode and out without it, fail2ban
// is started or reloaded only when something changed, and a file the
// operator wrote under our name is never overwritten (#141).
func TestApplyJails(t *testing.T) {
	fake, root := useFail2ban(t, true)
	jails := goldenJails()
	logs := t.TempDir()
	jails.MissLog = filepath.Join(logs, "nginx", "miss.log")
	jails.LoginLog = filepath.Join(logs, "x-ui", "3xui.log")

	if err := ApplyJails(jails); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, rel := range []string{jailFile, probeFilterFile, loginFilterFile} {
		if body, err := os.ReadFile(filepath.Join(root, rel)); err != nil || !strings.HasPrefix(string(body), "# Generated by 3AX-UI.") {
			t.Errorf("%s: %v %q", rel, err, body)
		}
	}
	// fail2ban refuses to start at all when a jail's log is missing.
	for _, log := range []string{jails.MissLog, jails.LoginLog} {
		if _, err := os.Stat(log); err != nil {
			t.Errorf("the log %s was not created: %v", log, err)
		}
	}
	if strings.Join(fake.calls, ",") != "start" {
		t.Errorf("first apply: calls %v, want a start", fake.calls)
	}

	// Nothing changed: fail2ban is left alone.
	fake.calls = nil
	if err := ApplyJails(jails); err != nil || len(fake.calls) != 0 {
		t.Errorf("an unchanged apply: %v, calls %v", err, fake.calls)
	}
	// A new exemption: reloaded.
	jails.IgnoreIP = append(jails.IgnoreIP, "192.0.2.10")
	if err := ApplyJails(jails); err != nil || strings.Join(fake.calls, ",") != "reload" {
		t.Errorf("a changed apply: %v, calls %v", err, fake.calls)
	}

	// The login jail goes when there is no login to guard.
	jails.LoginLog = ""
	if err := ApplyJails(jails); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, loginFilterFile)); !os.IsNotExist(err) {
		t.Errorf("the login filter stayed: %v", err)
	}

	// Off: our files go, fail2ban stays for whatever else it guards.
	fake.calls = nil
	if err := RemoveJails(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{jailFile, probeFilterFile} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s stayed after remove: %v", rel, err)
		}
	}
	if strings.Join(fake.calls, ",") != "reload" {
		t.Errorf("remove: calls %v, want a reload", fake.calls)
	}
	fake.calls = nil
	if err := RemoveJails(); err != nil || len(fake.calls) != 0 {
		t.Errorf("removing nothing: %v, calls %v", err, fake.calls)
	}
}

// TestApplyJails_ReloadThatDropsTheActionRestarts (#152): fail2ban (1.0.2 on
// Debian 12 and Ubuntu 24.04, 1.1.0 on Debian 13) drops a jail's action on a
// reload that changes the action's name — nftables-multiport written before
// the box had iptables, iptables-multiport after install_front_firewall put
// it there — and adds none back on any later reload. The jail keeps logging
// bans that block nothing. Only a restart brings the action back.
func TestApplyJails_ReloadThatDropsTheActionRestarts(t *testing.T) {
	fake, _ := useFail2ban(t, true)
	fake.running = true
	jails := goldenJails()
	jails.MissLog, jails.LoginLog = filepath.Join(t.TempDir(), "miss.log"), ""
	jails.BanAction = "nftables-multiport"
	if err := ApplyJails(jails); err != nil || strings.Join(fake.calls, ",") != "reload" {
		t.Fatalf("the first apply: %v, calls %v", err, fake.calls)
	}

	fake.calls, fake.reloadStrips = nil, true
	jails.BanAction = "iptables-multiport"
	if err := ApplyJails(jails); err != nil {
		t.Fatalf("the apply that renames the action: %v", err)
	}
	if strings.Join(fake.calls, ",") != "reload,restart" {
		t.Errorf("the apply that renames the action: calls %v, want a reload and then a restart", fake.calls)
	}
	if acts, _ := fake.Actions("3ax-ui-probe"); len(acts) == 0 {
		t.Error("the probe jail was left without an action")
	}
}

// TestApplyJails_HealsAJailFoundWithoutActions (#152): a box upgraded from a
// version that had already lost the action finds its files unchanged — no
// reload — and still has to restart fail2ban once, on the first apply. After
// that the running jails are not looked at again until the files change.
func TestApplyJails_HealsAJailFoundWithoutActions(t *testing.T) {
	fake, _ := useFail2ban(t, true)
	jails := goldenJails()
	jails.MissLog, jails.LoginLog = filepath.Join(t.TempDir(), "miss.log"), ""
	fake.running = true
	if err := ApplyJails(jails); err != nil {
		t.Fatal(err)
	}
	// A new process (the upgrade), the same files, a jail without actions.
	jailsChecked = false
	fake.calls, fake.asked = nil, nil
	fake.bare = map[string]bool{"3ax-ui-probe": true}
	if err := ApplyJails(jails); err != nil || strings.Join(fake.calls, ",") != "restart" {
		t.Errorf("the first apply after an upgrade: %v, calls %v, want a restart", err, fake.calls)
	}
	fake.calls, fake.asked = nil, nil
	if err := ApplyJails(jails); err != nil || len(fake.calls) != 0 || len(fake.asked) != 0 {
		t.Errorf("an unchanged apply after the check: %v, calls %v, asked %v", err, fake.calls, fake.asked)
	}
}

// TestApplyJails_ARestartThatDoesNotHelpIsReportedOnce: fail2ban that keeps
// the jail bare after a restart is an error for the Nginx page, not a
// restart on every tick of the reconcile.
func TestApplyJails_ARestartThatDoesNotHelpIsReportedOnce(t *testing.T) {
	fake, _ := useFail2ban(t, true)
	jails := goldenJails()
	jails.MissLog = filepath.Join(t.TempDir(), "miss.log")
	jails.LoginLog = filepath.Join(t.TempDir(), "3xui.log")
	fake.running, fake.stuck = true, true
	fake.bare = map[string]bool{"3ax-ui-login": true}
	if err := ApplyJails(jails); err == nil || !strings.Contains(err.Error(), "3ax-ui-login") {
		t.Errorf("a restart that did not help: %v", err)
	}
	fake.calls = nil
	if err := ApplyJails(jails); err != nil || len(fake.calls) != 0 {
		t.Errorf("the next apply: %v, calls %v, want fail2ban left alone", err, fake.calls)
	}
}

// TestApplyJails_AJailThatCannotBeAskedIsNotRestarted: a jail fail2ban does
// not know (a config it refused, say) says nothing about its actions.
func TestApplyJails_AJailThatCannotBeAskedIsNotRestarted(t *testing.T) {
	fake, _ := useFail2ban(t, true)
	jails := goldenJails()
	jails.MissLog, jails.LoginLog = filepath.Join(t.TempDir(), "miss.log"), ""
	fake.running = true
	fake.actionsErr = errors.New("Sorry but the jail '3ax-ui-probe' does not exist")
	if err := ApplyJails(jails); err != nil || strings.Join(fake.calls, ",") != "reload" {
		t.Errorf("apply: %v, calls %v, want only the reload", err, fake.calls)
	}
}

// TestParseJailActions reads fail2ban-client's answer to `get <jail> actions`
// as fail2ban 1.0.2 and 1.1.0 print it.
func TestParseJailActions(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want []string
	}{
		{"The jail 3ax-ui-probe has the following actions:\niptables-multiport\n", []string{"iptables-multiport"}},
		{"The jail sshd has the following actions:\nnftables, sendmail\n", []string{"nftables", "sendmail"}},
		{"No actions for jail 3ax-ui-probe\n", nil},
	} {
		if got := parseJailActions(tc.out); strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("parseJailActions(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}

// TestApplyJailsLeavesTheOperatorsFileAlone: a file of ours by name but not by
// header is somebody's hand-written config.
func TestApplyJailsLeavesTheOperatorsFileAlone(t *testing.T) {
	_, root := useFail2ban(t, true)
	own := filepath.Join(root, jailFile)
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("[mine]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jails := goldenJails()
	jails.MissLog, jails.LoginLog = filepath.Join(t.TempDir(), "miss.log"), ""
	if err := ApplyJails(jails); err == nil || !strings.Contains(err.Error(), "not ours") {
		t.Errorf("apply over the operator's file: %v", err)
	}
	if err := RemoveJails(); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(own); string(body) != "[mine]\nenabled = true\n" {
		t.Errorf("the operator's file was touched: %q", body)
	}
}

// TestApplyJailsWithoutFail2ban: the caller turns this into the status
// warning; nothing is written for a daemon that is not there.
func TestApplyJailsWithoutFail2ban(t *testing.T) {
	_, root := useFail2ban(t, false)
	jails := goldenJails()
	jails.MissLog, jails.LoginLog = filepath.Join(t.TempDir(), "miss.log"), ""
	if err := ApplyJails(jails); !errors.Is(err, ErrNoFail2ban) {
		t.Errorf("apply without fail2ban: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, jailFile)); !os.IsNotExist(err) {
		t.Errorf("a jail was written for a fail2ban that is not there: %v", err)
	}
}

// TestApplyJailsBacksOffAFailingStart: a fail2ban that cannot start is not
// restarted on every tick of the reconcile.
func TestApplyJailsBacksOffAFailingStart(t *testing.T) {
	fake, _ := useFail2ban(t, true)
	fake.startErr = errors.New("Have not found any log file for sshd jail")
	jails := goldenJails()
	jails.MissLog, jails.LoginLog = filepath.Join(t.TempDir(), "miss.log"), ""
	if err := ApplyJails(jails); err == nil || !strings.Contains(err.Error(), "sshd") {
		t.Errorf("a failing start: %v", err)
	}
	fake.calls = nil
	if err := ApplyJails(jails); err != nil || len(fake.calls) != 0 {
		t.Errorf("retried at once: %v, calls %v", err, fake.calls)
	}
}

// TestFail2banAcceptsTheJails hands the generated files to fail2ban itself,
// beside the distro's own config, and checks that jail.local still has the
// last word on every value (#141).
func TestFail2banAcceptsTheJails(t *testing.T) {
	client, err := exec.LookPath("fail2ban-client")
	if err != nil {
		t.Skip("fail2ban is not installed on this machine")
	}
	root := filepath.Join(t.TempDir(), "fail2ban")
	if out, err := exec.Command("cp", "-r", "/etc/fail2ban", root).CombinedOutput(); err != nil {
		t.Skipf("no distro config to test against: %v %s", err, out)
	}
	jails := goldenJails()
	logs := t.TempDir()
	jails.MissLog, jails.LoginLog = filepath.Join(logs, "miss.log"), filepath.Join(logs, "3xui.log")
	for rel, content := range jails.render() {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "jail.local"), []byte("[3ax-ui-probe]\nmaxretry = 20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(client, "-c", root, "-d").CombinedOutput()
	if err != nil {
		t.Fatalf("fail2ban refused the jails: %v\n%s", err, out)
	}
	for _, want := range []string{
		"['set', '3ax-ui-probe', 'maxretry', 20]",
		"['set', '3ax-ui-login', 'maxretry', 5]",
		"['set', '3ax-ui-probe', 'bantime.increment', True]",
		"['set', '3ax-ui-login', 'addignoreip', '127.0.0.1/8', '::1', '198.51.100.0/24', '2001:db8::7', '203.0.113.9']",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("fail2ban's view of the jails lacks %s", want)
		}
	}
}

// realFail2ban is a fail2ban server of the test's own: its config, socket,
// pid file and database all under root.
type realFail2ban struct{ root string }

func (f realFail2ban) client(args ...string) *exec.Cmd {
	return exec.Command("fail2ban-client", append([]string{"-c", f.root}, args...)...)
}

func (f realFail2ban) run(args ...string) error {
	if out, err := f.client(args...).CombinedOutput(); err != nil {
		return fmt.Errorf("fail2ban-client %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func (f realFail2ban) Running() bool  { return f.client("ping").Run() == nil }
func (f realFail2ban) Start() error   { return f.run("-x", "start") }
func (f realFail2ban) Reload() error  { return f.run("reload") }
func (f realFail2ban) Restart() error { return f.run("restart") }
func (f realFail2ban) Actions(jail string) ([]string, error) {
	return fail2banActions(f.client("get", jail, "actions"))
}

// TestFail2banBansBlockAfterTheActionChanges (#152) runs the stand's story
// against a real fail2ban: the probe jail is started with nftables-multiport
// (a box without iptables), rewritten with iptables-multiport (after
// install_front_firewall), and a ban must still land in iptables. It inserts
// a chain into INPUT and bans a TEST-NET address, so it runs only as root and
// only when asked: THREEAX_TEST_FAIL2BAN=1, on a throwaway machine or
// container with fail2ban, iptables and nft (see #152 for the container).
func TestFail2banBansBlockAfterTheActionChanges(t *testing.T) {
	if os.Getenv("THREEAX_TEST_FAIL2BAN") != "1" {
		t.Skip("set THREEAX_TEST_FAIL2BAN=1 to run fail2ban against this machine's firewall")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to touch netfilter")
	}
	for _, bin := range []string{"fail2ban-client", "fail2ban-server", "iptables", "nft"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed on this machine", bin)
		}
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "fail2ban")
	if out, err := exec.Command("cp", "-r", "/etc/fail2ban", root).CombinedOutput(); err != nil {
		t.Skipf("no distro config to test against: %v %s", err, out)
	}
	local := map[string]string{
		"fail2ban.local": fmt.Sprintf("[Definition]\nlogtarget = %[1]s/fail2ban.log\nsocket = %[1]s/fail2ban.sock\npidfile = %[1]s/fail2ban.pid\ndbfile = %[1]s/fail2ban.sqlite3\n", dir),
		// Debian's sshd jail wants a journal or an auth.log a container
		// does not have.
		"jail.local": "[sshd]\nenabled = false\n",
	}
	for name, content := range local {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	server := realFail2ban{root: root}
	prevRoot, prevCtl, prevInstalled, prevRetry, prevChecked := Fail2banRoot, fail2banCtl, fail2banInstalled, jailsRetryAfter, jailsChecked
	Fail2banRoot, fail2banCtl = root, server
	fail2banInstalled = func() bool { return true }
	jailsRetryAfter, jailsChecked = time.Time{}, false
	t.Cleanup(func() {
		// Stopping runs the actions' stop: the f2b chain and table go.
		_ = server.run("stop")
		Fail2banRoot, fail2banCtl, fail2banInstalled, jailsRetryAfter, jailsChecked = prevRoot, prevCtl, prevInstalled, prevRetry, prevChecked
	})

	jails := Jails{MissLog: filepath.Join(dir, "miss.log"), BanAction: "nftables-multiport"}
	if err := ApplyJails(jails); err != nil {
		t.Fatalf("start with nftables-multiport: %v", err)
	}
	jails.BanAction = "iptables-multiport"
	if err := ApplyJails(jails); err != nil {
		t.Fatalf("switch to iptables-multiport: %v", err)
	}
	if actions, err := server.Actions("3ax-ui-probe"); err != nil || strings.Join(actions, ",") != "iptables-multiport" {
		t.Fatalf("the probe jail's actions after the switch: %v %v", actions, err)
	}
	if err := server.run("set", "3ax-ui-probe", "banip", "192.0.2.55"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("iptables", "-S", "f2b-3ax-ui-probe").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "192.0.2.55") {
		t.Errorf("the ban is not in iptables: %v\n%s", err, out)
	}
}
