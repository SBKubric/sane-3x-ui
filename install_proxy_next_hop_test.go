package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/proxy"
)

// A hop set up by hand used to be asked for its next hop's address only, and
// got sub port 2096 behind its owner's back. Every hop of production and of
// the stand runs the only443 front, whose firewall closes 2096, so the join
// timed out (#265). The installer now takes a port in the address, and
// otherwise finds the port the next hop answers on.

// nextHopFunctions leave out proxy_next_hop_is_chain: stubProbe stands in.
var nextHopFunctions = proxyFrontFunctions

// stubProbe stands in for proxy_next_hop_is_chain: it logs every port it is
// asked about and answers as a chain hop on the ports in $ANSWERING.
const stubProbe = `proxy_next_hop_is_chain() {
    echo "probed $1://$2:$3" >&2
    [[ " ${ANSWERING:-} " == *" $3 "* ]]
}
`

// nextHopEnv is the environment of a hop set up by hand: no sub port, the one
// value the orchestrator always passes.
func nextHopEnv(nextHop string, extra ...string) []string {
	env := []string{
		"XUI_PROXY_MODE=1", "PROXY_NEXT_HOP=" + nextHop, "PROXY_JOIN_TOKEN=0123456789012345678901234567890a",
		"PROXY_DOMAIN=192.0.2.9", "PROXY_TLS=none",
	}
	return append(env, extra...)
}

// loadProxyJSON reads what proxy_config_json printed the way `x-ui proxy` does.
func loadProxyJSON(t *testing.T, out string) *proxy.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxy.json")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := proxy.LoadConfig(path)
	if err != nil {
		t.Fatalf("proxy.json does not load: %v\n%s", err, out)
	}
	return cfg
}

// TestInstallProxyNextHopAddressCarriesThePort: host:port and [ipv6]:port name
// the next hop's sub port, a named port is never probed, and what lands in
// proxy.json is the URL the box will join through.
func TestInstallProxyNextHopAddressCarriesThePort(t *testing.T) {
	cases := []struct {
		name, nextHop string
		env           []string
		wantBase      string
	}{
		{name: "host:port", nextHop: "198.51.100.7:443", wantBase: "https://198.51.100.7:443"},
		{name: "name:port", nextHop: "bridge.example.net:443", wantBase: "https://bridge.example.net:443"},
		{name: "bracketed IPv6 with a port", nextHop: "[2001:db8::7]:443", wantBase: "https://[2001:db8::7]:443"},
		{name: "bracketed IPv6, port from the variable", nextHop: "[2001:db8::7]",
			env: []string{"PROXY_NEXT_HOP_SUB_PORT=2096"}, wantBase: "https://[2001:db8::7]:2096"},
		{name: "bare IPv6, port from the variable", nextHop: "2001:db8::7",
			env: []string{"PROXY_NEXT_HOP_SUB_PORT=443"}, wantBase: "https://[2001:db8::7]:443"},
		{name: "the variable wins over the probe", nextHop: "198.51.100.7",
			env: []string{"PROXY_NEXT_HOP_SUB_PORT=8443", "ANSWERING=443 2096"}, wantBase: "https://198.51.100.7:8443"},
		{name: "address and variable agree", nextHop: "198.51.100.7:443",
			env: []string{"PROXY_NEXT_HOP_SUB_PORT=443"}, wantBase: "https://198.51.100.7:443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runInstallShell(t, nextHopFunctions, stubProbe+"prompt_proxy_mode >/dev/null\nproxy_config_json\n",
				nextHopEnv(tc.nextHop, tc.env...)...)
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if strings.Contains(out, "probed") {
				t.Fatalf("a named port was probed anyway:\n%s", out)
			}
			if got := loadProxyJSON(t, out).NextHopBase(); got != tc.wantBase {
				t.Errorf("next hop = %s, want %s", got, tc.wantBase)
			}
		})
	}

	refused := []struct {
		name, nextHop string
		env           []string
	}{
		{name: "address and variable disagree", nextHop: "198.51.100.7:443", env: []string{"PROXY_NEXT_HOP_SUB_PORT=2096"}},
		{name: "a colon and no port", nextHop: "198.51.100.7:"},
		{name: "brackets, a colon and no port", nextHop: "[2001:db8::7]:"},
		{name: "a port and no host", nextHop: ":443"},
		{name: "a port that is not a number", nextHop: "198.51.100.7:https"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runInstallShell(t, nextHopFunctions, stubProbe+"prompt_proxy_mode\necho reached\n",
				nextHopEnv(tc.nextHop, tc.env...)...)
			if err == nil || strings.Contains(out, "reached") {
				t.Errorf("PROXY_NEXT_HOP=%q was accepted:\n%s", tc.nextHop, out)
			}
		})
	}
}

// TestInstallProxyNextHopPortIsFound: without a named port the next hop is
// asked on 443 first, then on 2096, and the answer is said out loud. Nothing
// answering keeps the old 2096 without a TTY — such a box always came up,
// next hop or not — and says which variable fixes it.
func TestInstallProxyNextHopPortIsFound(t *testing.T) {
	cases := []struct {
		name, answering, wantPort, wantSay string
		wantProbed                         []string
	}{
		{name: "behind only443", answering: "443 2096", wantPort: "443",
			wantSay: "next hop answers on 443 (front only443)", wantProbed: []string{"443"}},
		{name: "without a front", answering: "2096", wantPort: "2096",
			wantSay: "next hop answers on 2096 (no front)", wantProbed: []string{"443", "2096"}},
		{name: "nothing answers", answering: "", wantPort: "2096",
			wantSay: "PROXY_NEXT_HOP_SUB_PORT=443", wantProbed: []string{"443", "2096"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runInstallShell(t, nextHopFunctions,
				stubProbe+"prompt_proxy_mode\necho \"port=${PROXY_NEXT_HOP_SUB_PORT} front=${PROXY_FRONT}\"\n",
				nextHopEnv("198.51.100.7", "ANSWERING="+tc.answering)...)
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if !strings.Contains(out, "port="+tc.wantPort+" front=off") {
				t.Errorf("want port %s and the front left off without a TTY:\n%s", tc.wantPort, out)
			}
			if !strings.Contains(out, tc.wantSay) {
				t.Errorf("the installer does not say %q:\n%s", tc.wantSay, out)
			}
			var probed []string
			for _, line := range strings.Split(out, "\n") {
				if port, found := strings.CutPrefix(line, "probed https://198.51.100.7:"); found {
					probed = append(probed, port)
				}
			}
			if strings.Join(probed, " ") != strings.Join(tc.wantProbed, " ") {
				t.Errorf("probed %v, want %v", probed, tc.wantProbed)
			}
			if tc.answering == "" && (!strings.Contains(out, "443") || !strings.Contains(out, "2096")) {
				t.Errorf("the warning does not name both ports:\n%s", out)
			}
		})
	}

	// The scheme the owner asked for is the one probed.
	out, err := runInstallShell(t, nextHopFunctions, stubProbe+"prompt_proxy_mode >/dev/null\n",
		nextHopEnv("198.51.100.7", "PROXY_NEXT_HOP_SCHEME=http", "ANSWERING=2096")...)
	if err != nil || !strings.Contains(out, "probed http://198.51.100.7:443") {
		t.Errorf("PROXY_NEXT_HOP_SCHEME=http was not probed over http: %v\n%s", err, out)
	}
}

// TestInstallProxyNextHopProbeKnowsAChainHop runs the real probe, with curl,
// against the real hop handler: a bare 404 to an empty join token is the
// chain; a web server's 404 page, a closed port and a server that answers
// something else are not.
func TestInstallProxyNextHopProbeKnowsAChainHop(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not available")
	}
	hop := httptest.NewTLSServer(proxy.NewChainHandler(&proxy.Config{}, proxy.NewState(), nil).Handler())
	defer hop.Close()
	webServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><body><h1>404 Not Found</h1></body></html>"))
	}))
	defer webServer.Close()
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	closed := closedPort(t)

	port := func(server *httptest.Server) string {
		_, p, _ := net.SplitHostPort(server.Listener.Addr().String())
		return p
	}
	detect := func(t *testing.T, host string, ports ...string) (string, error) {
		t.Helper()
		out, err := runInstallShell(t, []string{"proxy_detect_next_hop_port", "proxy_next_hop_is_chain"},
			"proxy_detect_next_hop_port https "+host+" "+strings.Join(ports, " ")+"\n")
		return strings.TrimSpace(out), err
	}

	got, err := detect(t, "127.0.0.1", port(webServer), closed, port(other), port(hop))
	if err != nil || got != port(hop) {
		t.Errorf("detected %q (%v), want the hop on %s", got, err, port(hop))
	}
	if got, err := detect(t, "127.0.0.1", port(webServer), closed, port(other)); err == nil {
		t.Errorf("no chain hop among the ports, but %q was taken for one", got)
	}

	// An IPv6 next hop goes into the URL in brackets.
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback")
	}
	hop6 := httptest.NewUnstartedServer(proxy.NewChainHandler(&proxy.Config{}, proxy.NewState(), nil).Handler())
	hop6.Listener.Close()
	hop6.Listener = listener
	hop6.StartTLS()
	defer hop6.Close()
	if got, err := detect(t, "::1", port(hop6)); err != nil || got != port(hop6) {
		t.Errorf("IPv6 next hop: detected %q (%v), want %s", got, err, port(hop6))
	}
}

// closedPort is a local port nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return strconv.Itoa(port)
}

// runInstallShellTTY is runInstallShell with stdin on a terminal, typing input,
// for the questions install.sh asks only there. script(1) provides the pty.
func runInstallShellTTY(t *testing.T, functions []string, body, input string, env ...string) (string, error) {
	t.Helper()
	scriptBin, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is not available")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	file := filepath.Join(t.TempDir(), "run.sh")
	code := "red='' green='' yellow='' blue='' plain=''\n" + scriptFunctions(t, "install.sh", functions...) + body
	if err := os.WriteFile(file, []byte(code), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(scriptBin, "-qec", bash+" "+file, "/dev/null")
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "TERM=dumb"}, env...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	return strings.ReplaceAll(string(out), "\r", ""), err
}

// TestInstallProxyNextHopOnATerminal: an owner at a terminal is asked for the
// front, with only443 offered first when the next hop is behind one, and is
// stopped — not handed 2096 — when the next hop answers on neither port.
func TestInstallProxyNextHopOnATerminal(t *testing.T) {
	body := stubProbe + "prompt_proxy_mode\necho \"port=${PROXY_NEXT_HOP_SUB_PORT} front=${PROXY_FRONT}\"\n"
	cases := []struct {
		name, answering, input, want string
	}{
		{name: "behind only443, default", answering: "443", input: "\n", want: "port=443 front=only443"},
		{name: "behind only443, off typed", answering: "443", input: "off\n", want: "port=443 front=off"},
		{name: "without a front, default", answering: "2096", input: "\n", want: "port=2096 front=off"},
		{name: "without a front, only443 typed", answering: "2096", input: "ONLY443\n", want: "port=2096 front=only443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runInstallShellTTY(t, nextHopFunctions, body, tc.input,
				nextHopEnv("198.51.100.7", "ANSWERING="+tc.answering)...)
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q:\n%s", tc.want, out)
			}
			if !strings.Contains(out, "production and stand hops run it") {
				t.Errorf("the front question does not say what the chain runs:\n%s", out)
			}
		})
	}

	// PROXY_FRONT given: no question.
	out, err := runInstallShellTTY(t, nextHopFunctions, body, "",
		nextHopEnv("198.51.100.7", "ANSWERING=443", "PROXY_FRONT=off")...)
	if err != nil || !strings.Contains(out, "port=443 front=off") || strings.Contains(out, "Front [only443/off]") {
		t.Errorf("PROXY_FRONT=off was asked again or lost: %v\n%s", err, out)
	}

	out, err = runInstallShellTTY(t, nextHopFunctions, body, "\n", nextHopEnv("198.51.100.7", "ANSWERING=")...)
	if err == nil || strings.Contains(out, "port=") {
		t.Errorf("a next hop answering on neither port went through on a terminal:\n%s", out)
	}
	for _, want := range []string{"443", "2096", "PROXY_NEXT_HOP_SUB_PORT", "Nothing has been changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}
