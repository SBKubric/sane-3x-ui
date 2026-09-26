package nginx

import (
	"errors"
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
func (f *fakeFail2ban) Reload() error { f.calls = append(f.calls, "reload"); return nil }

// useFail2ban points the package at a temporary /etc/fail2ban and a fake
// fail2ban.
func useFail2ban(t *testing.T, installed bool) (*fakeFail2ban, string) {
	t.Helper()
	root := t.TempDir()
	fake := &fakeFail2ban{}
	prevRoot, prevCtl, prevInstalled, prevRetry := Fail2banRoot, fail2banCtl, fail2banInstalled, jailsRetryAfter
	Fail2banRoot, fail2banCtl = root, fake
	fail2banInstalled = func() bool { return installed }
	jailsRetryAfter = time.Time{}
	t.Cleanup(func() {
		Fail2banRoot, fail2banCtl, fail2banInstalled, jailsRetryAfter = prevRoot, prevCtl, prevInstalled, prevRetry
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
