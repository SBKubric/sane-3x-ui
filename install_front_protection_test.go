package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeXUI stands in for the panel binary in a temporary xui_folder: it
// records every call and answers `setting -show true` and `nginx mode` the
// way the real one does.
func fakeXUI(t *testing.T, mode string) (folder, calls string) {
	t.Helper()
	folder = t.TempDir()
	calls = filepath.Join(folder, "calls")
	script := `#!/bin/sh
echo "$*" >>"` + calls + `"
case "$*" in
"setting -show true") printf 'port: 2053\nwebBasePath: /abc/\n' ;;
"nginx mode") echo "` + mode + `" ;;
esac
`
	if err := os.WriteFile(filepath.Join(folder, "x-ui"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return folder, calls
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(raw)
}

var webListenFunctions = []string{"config_web_listen", "prompt_web_listen", "is_ip", "is_ipv4", "is_ipv6", "panel_base_path"}

// TestInstallWebListenOption (#141): XUI_WEB_LISTEN sets where the panel
// listens (webListen), and on the loopback the installer says how to reach
// the panel's pages through an SSH tunnel. Nothing else changes it: without
// the option the panel keeps listening where it did.
func TestInstallWebListenOption(t *testing.T) {
	cases := []struct {
		name, listen string
		wantSet      string // the -listenIP call, "" for none
		wantTunnel   bool
	}{
		{name: "unset", listen: ""},
		{name: "loopback", listen: "127.0.0.1", wantSet: "setting -listenIP 127.0.0.1", wantTunnel: true},
		{name: "all addresses", listen: "0.0.0.0", wantSet: "setting -listenIP 0.0.0.0"},
		{name: "not an address", listen: "localhost; rm -rf /", wantSet: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			folder, calls := fakeXUI(t, "off")
			out, err := runInstallShell(t, webListenFunctions, "config_web_listen\n",
				"xui_folder="+folder, "XUI_WEB_LISTEN="+tc.listen)
			if err != nil {
				t.Fatalf("config_web_listen: %v\n%s", err, out)
			}
			got := readCalls(t, calls)
			if tc.wantSet == "" && strings.Contains(got, "-listenIP") {
				t.Errorf("the listen address was set: %q", got)
			}
			if tc.wantSet != "" && !strings.Contains(got, tc.wantSet+"\n") {
				t.Errorf("calls %q, want %q", got, tc.wantSet)
			}
			hint := strings.Contains(out, "ssh -L 2053:127.0.0.1:2053") && strings.Contains(out, "https://127.0.0.1:2053/abc/")
			if hint != tc.wantTunnel {
				t.Errorf("tunnel hint shown = %v, want %v:\n%s", hint, tc.wantTunnel, out)
			}
		})
	}

	// Asked for interactively where the installer asks for the port.
	out, err := runInstallShell(t, webListenFunctions, "prompt_web_listen <<<y\necho \"listen=${XUI_WEB_LISTEN}\"\n")
	if err != nil || !strings.Contains(out, "listen=127.0.0.1") {
		t.Errorf("answering yes: %v\n%s", err, out)
	}
	out, _ = runInstallShell(t, webListenFunctions, "prompt_web_listen <<<''\necho \"listen=${XUI_WEB_LISTEN}\"\n")
	if !strings.Contains(out, "listen=\n") {
		t.Errorf("the default changed where the panel listens:\n%s", out)
	}
	// An option given up front is not asked again.
	out, _ = runInstallShell(t, webListenFunctions, "prompt_web_listen </dev/null\necho \"listen=${XUI_WEB_LISTEN}\"\n", "XUI_WEB_LISTEN=0.0.0.0")
	if !strings.Contains(out, "listen=0.0.0.0") || strings.Contains(out, "SSH tunnel?") {
		t.Errorf("XUI_WEB_LISTEN was overridden or asked again:\n%s", out)
	}
}

var fail2banFunctions = []string{"front_wants_fail2ban", "install_front_firewall", "install_fail2ban", "fail2ban_sshd_journal"}

// TestInstallFail2banWithTheFront (#141): fail2ban comes onto a box together
// with only443 — on a hop from PROXY_FRONT or proxy.json, on the panel from
// its stored mode — and not otherwise; XUI_FAIL2BAN=1 asks for it outright.
func TestInstallFail2banWithTheFront(t *testing.T) {
	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			proxyJSON := filepath.Join(t.TempDir(), "proxy.json")
			cases := []struct {
				name string
				env  []string
				mode string // what `x-ui nginx mode` says
				json string // proxy.json's front, "" for no file
				want bool
			}{
				{name: "panel off", mode: "off"},
				{name: "panel shared", mode: "shared"},
				{name: "panel only443", mode: "only443", want: true},
				{name: "asked for", mode: "off", env: []string{"XUI_FAIL2BAN=1"}, want: true},
				{name: "hop off", env: []string{"XUI_PROXY_MODE=1", "PROXY_FRONT=off"}},
				{name: "hop only443", env: []string{"XUI_PROXY_MODE=1", "PROXY_FRONT=only443"}, want: true},
				{name: "hop from proxy.json", env: []string{"XUI_PROXY_MODE=1"}, json: "only443", want: true},
				{name: "hop proxy.json off", env: []string{"XUI_PROXY_MODE=1"}, json: "off"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					folder, _ := fakeXUI(t, tc.mode)
					_ = os.Remove(proxyJSON)
					if tc.json != "" {
						body := `{"version": 2, "front": {"mode": "` + tc.json + `"}}`
						if err := os.WriteFile(proxyJSON, []byte(body), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					env := append([]string{"xui_folder=" + folder, "PROXY_CONFIG=" + proxyJSON}, tc.env...)
					out, _ := runScriptShell(t, script, fail2banFunctions,
						"if front_wants_fail2ban; then echo WANTED; else echo NOT; fi\n", env...)
					if got := strings.Contains(out, "WANTED"); got != tc.want {
						t.Errorf("fail2ban wanted = %v, want %v:\n%s", got, tc.want, out)
					}
				})
			}
		})
	}
}

// TestFail2banSshdJournal: Debian 12's fail2ban enables an sshd jail that
// reads /var/log/auth.log; on a box without rsyslog that file is missing and
// fail2ban refuses to start at all, the front's jails with it. The installer
// points that jail at the journal then — and only then.
func TestFail2banSshdJournal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		authLog  bool
		existing string
		want     string
	}{
		{name: "no auth.log", want: "backend = systemd"},
		{name: "auth.log present", authLog: true},
		{name: "operator's own override", existing: "[sshd]\nenabled = false\n", want: "enabled = false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "jail.d"), 0o755); err != nil {
				t.Fatal(err)
			}
			authLog := filepath.Join(root, "auth.log")
			if tc.authLog {
				if err := os.WriteFile(authLog, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			override := filepath.Join(root, "jail.d", "3ax-ui-sshd.local")
			if tc.existing != "" {
				if err := os.WriteFile(override, []byte(tc.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			out, err := runScriptShell(t, "install.sh", fail2banFunctions,
				"PATH=\""+bin+":$PATH\"\nfail2ban_sshd_journal\n",
				"FAIL2BAN_ROOT="+root, "FAIL2BAN_AUTH_LOGS="+authLog)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			body, _ := os.ReadFile(override)
			if tc.want == "" && len(body) > 0 {
				t.Errorf("an override was written although sshd's log is there: %q", body)
			}
			if tc.want != "" && !strings.Contains(string(body), tc.want) {
				t.Errorf("override = %q, want %q", body, tc.want)
			}
		})
	}
}

// TestInstallFail2banBringsIptables: the front's firewall drives iptables, which
// Debian 13 does not ship; installing the jails for only443 installs it too
// (found on the stand, SBKubric/sane-3x-ui-orchestrator#22).
func TestInstallFail2banBringsIptables(t *testing.T) {
	for _, script := range []string{"install.sh", "update.sh"} {
		t.Run(script, func(t *testing.T) {
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "apt.log")
			stub := "#!/bin/sh\necho \"$*\" >> " + log + "\n"
			if err := os.WriteFile(filepath.Join(bin, "apt-get"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			// fail2ban-client is present, so only the firewall is missing.
			if err := os.WriteFile(filepath.Join(bin, "fail2ban-client"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			out, _ := runScriptShell(t, script, fail2banFunctions,
				"fail2ban_sshd_journal() { :; }\ninstall_fail2ban\n",
				"PATH="+bin+":/usr/bin:/bin", "release=debian")
			got, _ := os.ReadFile(log)
			if !strings.Contains(string(got), "install -y -q iptables") {
				t.Errorf("iptables was not installed, apt-get saw %q:\n%s", got, out)
			}
		})
	}
}
