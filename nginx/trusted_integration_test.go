package nginx

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTheShowcaseIsNeitherLimitedNorBanned (#228) runs an edge's guarded
// HTTP side in a real nginx, and its probe jail in a real fail2ban, with the
// subscription showcase's network among the exemptions — where a hop's front
// puts the document's frontTrustedAddrs. The showcase asks for far more
// subscriptions than the limit lets one address have, many of them unknown,
// and gets every answer from the sub server; a client doing the same is
// limited, and banned.
func TestTheShowcaseIsNeitherLimitedNorBanned(t *testing.T) {
	root := tempNginxTree(t)
	certFile, keyFile := writeSelfSignedCert(t, root, "198.51.100.20")
	if err := os.WriteFile(filepath.Join(root, "www", IndexFile), []byte("<html>stub</html>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The edge's sub server: a known subscription, and a refusal for the
	// rest — the misses a showcase passes on from clients' typos.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub-abc123/known" {
			http.Error(w, "Error!", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "subscription")
	}))
	defer upstream.Close()

	listenPort, err := FreeLoopbackPort(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicPort, err := FreeLoopbackPort(0, map[int]bool{listenPort: true})
	if err != nil {
		t.Fatal(err)
	}
	listen := net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort))
	missLog := filepath.Join(root, "logs", "miss.log")
	// The neighbour next hop, and the showcase's network as the owner
	// entered it.
	guard := &Guard{Exempt: []string{"192.0.2.1", "203.0.113.0/29"}, MissLog: missLog}
	runningFront(t, Config{Mode: ModeOnly443, Port: publicPort, Site: &Site{
		IPCertFile: certFile, IPKeyFile: keyFile, Listen: listen, Root: filepath.Join(root, "www"),
		Sub:   &Proxy{Name: "sub server of edge-a", Paths: []string{"/sub-abc123/"}, Target: strings.TrimPrefix(upstream.URL, "http://")},
		Login: &Proxy{Name: "join page of edge-a", Paths: []string{"/join/"}, Target: strings.TrimPrefix(upstream.URL, "http://")},
		Guard: guard,
	}})

	const showcase, client = "203.0.113.5", "198.51.100.8"
	// Twice the subscription limit's burst and more, half of it unknown.
	const requests = 70
	for i := range requests {
		path := "/sub-abc123/known"
		if i%2 == 1 {
			path = "/sub-abc123/typo-" + strconv.Itoa(i)
		}
		got := ask(t, listen, showcase, "GET", path)
		if strings.Contains(got.body, "stub") {
			t.Fatalf("request %d of the showcase was limited: %+v", i, got)
		}
		if (path == "/sub-abc123/known") != (got.status == http.StatusOK) {
			t.Fatalf("request %d of the showcase (%s): %+v, want the sub server's own answer", i, path, got)
		}
	}
	limited := false
	for i := range requests {
		got := ask(t, listen, client, "GET", "/sub-abc123/typo-"+strconv.Itoa(i))
		if strings.Contains(got.body, "stub") {
			limited = true
			break
		}
	}
	if !limited {
		t.Errorf("%d requests of one client were never limited", requests)
	}

	raw, err := os.ReadFile(missLog)
	if err != nil {
		t.Fatalf("no miss log: %v", err)
	}
	if !strings.Contains(string(raw), client+` [`) {
		t.Fatalf("the client's misses are not in the miss log:\n%s", raw)
	}

	// The probe jail, as a box renders it, in a fail2ban of its own.
	banned := runProbeJail(t, Jails{MissLog: missLog, IgnoreIP: guard.Exempt})
	if !slices.Contains(banned, client) {
		t.Errorf("the client was not banned: banned %v", banned)
	}
	if slices.Contains(banned, showcase) {
		t.Errorf("the showcase was banned: banned %v", banned)
	}
}

// runProbeJail starts a fail2ban of its own on jails' files, in a temporary
// tree with an action that only writes the ban down, and returns the
// addresses its probe jail banned once it has read the miss log.
func runProbeJail(t *testing.T, jails Jails) []string {
	t.Helper()
	client, err := exec.LookPath("fail2ban-client")
	if err != nil {
		t.Skip("fail2ban is not installed on this machine")
	}
	dir := t.TempDir()
	socket, pid := filepath.Join(dir, "f2b.sock"), filepath.Join(dir, "f2b.pid")
	jails.BanAction = "threeax-test"
	files := jails.render()
	files["fail2ban.conf"] = "[Definition]\nloglevel = INFO\nlogtarget = " + filepath.Join(dir, "f2b.log") +
		"\nsocket = " + socket + "\npidfile = " + pid + "\ndbfile = :memory:\n"
	files["jail.conf"] = "[DEFAULT]\n"
	files["action.d/threeax-test.conf"] = "[Definition]\nactionstart =\nactionstop =\nactioncheck =\nactionban =\nactionunban =\n"
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f2b := func(args ...string) (string, error) {
		out, err := exec.Command(client, append([]string{"-c", dir, "-s", socket, "-p", pid}, args...)...).CombinedOutput()
		return string(out), err
	}
	if out, err := f2b("-x", "start"); err != nil {
		log, _ := os.ReadFile(filepath.Join(dir, "f2b.log"))
		t.Fatalf("start fail2ban: %v\n%s\n%s", err, out, log)
	}
	t.Cleanup(func() { _, _ = f2b("stop") })

	// The jail reads the whole log once it is up; wait until the bans stop
	// coming.
	var banned []string
	for range 50 {
		time.Sleep(200 * time.Millisecond)
		out, err := f2b("status", "3ax-ui-probe")
		if err != nil {
			continue
		}
		_, list, found := strings.Cut(out, "Banned IP list:")
		if !found {
			continue
		}
		now := strings.Fields(list)
		if len(now) > 0 && slices.Equal(now, banned) {
			return now
		}
		banned = now
	}
	log, _ := os.ReadFile(filepath.Join(dir, "f2b.log"))
	t.Logf("fail2ban's log:\n%s", log)
	return banned
}
