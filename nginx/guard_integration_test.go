package nginx

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// runningFront stages cfg in a temporary tree and starts a real nginx on it,
// stopping it when the test ends.
func runningFront(t *testing.T, cfg Config) {
	t.Helper()
	staged, err := Stage(cfg)
	if err != nil {
		t.Fatalf("nginx refused the config: %v", err)
	}
	if out, err := exec.Command(Binary, "-c", MainConfPath()).CombinedOutput(); err != nil {
		t.Fatalf("start nginx: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command(Binary, "-c", MainConfPath(), "-s", "stop").Run()
		staged.tx.rollback()
	})
	_, port, _ := net.SplitHostPort(cfg.Site.Listen)
	n, _ := strconv.Atoi(port)
	for range 50 {
		if accepting(n) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nginx does not answer on %s", cfg.Site.Listen)
}

// answer is what a client got back.
type answer struct {
	status int
	body   string
}

// ask sends one request to the HTTP side the way the stream block hands it
// over: a PROXY header naming client, then TLS.
func ask(t *testing.T, listen, client, method, path string) answer {
	t.Helper()
	conn, err := net.DialTimeout("tcp", listen, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(conn, "PROXY TCP4 %s 127.0.0.1 40000 443\r\n", client); err != nil {
		t.Fatal(err)
	}
	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "panel.example.net"})
	fmt.Fprintf(tlsConn, "%s %s HTTP/1.1\r\nHost: panel.example.net\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", method, path)
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return answer{resp.StatusCode, string(body)}
}

// TestGuardedHTTPSideInPractice runs the guarded HTTP side in a real nginx
// (#141) and looks at it from outside, the way a prober and a client would.
func TestGuardedHTTPSideInPractice(t *testing.T) {
	root := tempNginxTree(t)
	certFile, keyFile := writeSelfSignedCert(t, root, "panel.example.net")
	if err := os.WriteFile(filepath.Join(root, "www", IndexFile), []byte("<html>stub</html>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The panel and the sub server, as one upstream that remembers who it
	// was told the client is.
	var mu sync.Mutex
	var seenIPs []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenIPs = append(seenIPs, r.Header.Get("X-Real-IP"))
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/unknown") {
			http.Error(w, "Error!", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "upstream %s", r.URL.Path)
	}))
	defer upstream.Close()
	target := strings.TrimPrefix(upstream.URL, "http://")

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
	cfg := Config{Mode: ModeOnly443, Port: publicPort, Site: &Site{
		Domain: "panel.example.net", CertFile: certFile, KeyFile: keyFile,
		Listen: listen, Root: filepath.Join(root, "www"),
		Panel: &Proxy{Name: "panel API", Paths: []string{"/base/panel/api/"}, Target: target},
		Login: &Proxy{Name: "panel login", Paths: []string{"/base/login"}, Target: target},
		Sub:   &Proxy{Name: "subscriptions", Paths: []string{"/sub/"}, Target: target},
		Decoy: []string{"/base/"},
		Guard: &Guard{Exempt: []string{"198.51.100.99"}, MissLog: missLog},
	}}
	runningFront(t, cfg)

	const prober, client, neighbour = "198.51.100.7", "198.51.100.8", "198.51.100.99"
	stub := ask(t, listen, prober, "GET", "/nothing-here")
	if stub.status != http.StatusOK || !strings.Contains(stub.body, "stub") {
		t.Fatalf("the stub itself: %+v", stub)
	}

	// The panel's UI is not published: its pages are the stub.
	if got := ask(t, listen, prober, "GET", "/base/panel/"); got != stub {
		t.Errorf("GET /base/panel/ = %+v, want the stub %+v", got, stub)
	}
	// The API and the login are.
	if got := ask(t, listen, client, "GET", "/base/panel/api/inbounds/list"); got.body != "upstream /base/panel/api/inbounds/list" {
		t.Errorf("the API is not published: %+v", got)
	}
	if got := ask(t, listen, client, "POST", "/base/login"); got.body != "upstream /base/login" {
		t.Errorf("the login is not published: %+v", got)
	}
	// Exactly the login: a path next to it is the stub.
	if got := ask(t, listen, prober, "GET", "/base/login.html"); got != stub {
		t.Errorf("GET /base/login.html = %+v, want the stub", got)
	}
	// The upstream's own refusal stays what it is: the hops read a 404 as
	// «revoked», and a subscription client its error.
	if got := ask(t, listen, prober, "GET", "/sub/unknown"); got.status != http.StatusBadRequest {
		t.Errorf("an unknown subscription = %+v, want the sub server's own 400", got)
	}

	// Over the limit a client gets the stub's answer to that request, not 429.
	postStub := ask(t, listen, prober, "POST", "/nothing-here")
	limited := false
	for range 10 {
		got := ask(t, listen, client, "POST", "/base/login")
		if got.status == http.StatusTooManyRequests {
			t.Fatalf("a limit answered 429: %+v", got)
		}
		if got == postStub {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("ten logins in a row were never limited")
	}
	for range 10 {
		if got := ask(t, listen, neighbour, "POST", "/base/login"); got.body != "upstream /base/login" {
			t.Fatalf("an exempt neighbour was limited: %+v", got)
		}
	}

	mu.Lock()
	for _, ip := range seenIPs {
		if ip != client && ip != prober && ip != neighbour {
			t.Errorf("the upstream was told the client is %q", ip)
		}
	}
	mu.Unlock()

	// The misses, written down for the probe jail: with the client's own
	// address, whatever the status.
	raw, err := os.ReadFile(missLog)
	if err != nil {
		t.Fatalf("no miss log: %v", err)
	}
	log := string(raw)
	for _, want := range []string{
		prober + ` [`, `] miss "GET /base/panel/" 200`,
		`] miss "GET /base/login.html" 200`,
		`] miss "GET /sub/unknown" 400`,
		client + ` [`, `] limit "POST /base/login" `,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the miss log lacks %q:\n%s", want, log)
		}
	}
	for _, unwanted := range []string{`"GET /nothing-here"`, `"GET /base/panel/api/inbounds/list"`, neighbour} {
		if strings.Contains(log, unwanted) {
			t.Errorf("the miss log has %q, which is no miss:\n%s", unwanted, log)
		}
	}
}

// TestNginxAcceptsTheGuardedFrontOfAHop hands a box's guarded front (#141) to
// nginx -t: the join page under the login limit beside the raw relays.
func TestNginxAcceptsTheGuardedFrontOfAHop(t *testing.T) {
	root := tempNginxTree(t)
	ipCertFile, ipKeyFile := writeSelfSignedCert(t, root, "198.51.100.20")
	cfg := goldenHopConfig()
	cfg.Port = 14443
	cfg.Routes[1].Upstream = "192.0.2.10:443"
	cfg.Site.IPCertFile, cfg.Site.IPKeyFile = ipCertFile, ipKeyFile
	cfg.Site.Root = filepath.Join(root, "www")
	cfg.Site.Login = &Proxy{Name: "join page", Paths: []string{"/join/"}, Target: "127.0.0.1:8083"}
	cfg.Site.Guard = &Guard{Exempt: []string{"203.0.113.9", "2001:db8::/64"}, MissLog: filepath.Join(root, "logs", "miss.log")}

	staged, err := Stage(cfg)
	if err != nil {
		t.Fatalf("nginx refused the guarded front of a hop: %v", err)
	}
	t.Cleanup(func() { staged.tx.rollback() })
}
