package controller_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/web/controller"
	"github.com/gin-gonic/gin"
)

// failedLoginLine posts a wrong login the way nginx hands it over — from the
// loopback, with the client in X-Real-IP — and returns the panel's log line.
func failedLoginLine(t *testing.T, username string) string {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { database.CloseDB() })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	controller.NewIndexController(r.Group("/"))

	form := url.Values{"username": {username}, "password": {"guess"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Real-IP", "198.51.100.9")
	req.RemoteAddr = "127.0.0.1:40000"
	r.ServeHTTP(httptest.NewRecorder(), req)

	for _, line := range logger.GetLogs(100, "WARNING") {
		if strings.Contains(line, "wrong username") {
			return line
		}
	}
	t.Fatal("no failed login was logged")
	return ""
}

// TestFailedLoginLogsTheClientBehindTheFront: the login jail (#141) bans the
// address in the panel's "wrong username" line, so behind the front it has
// to be the client nginx names in X-Real-IP, not nginx's own 127.0.0.1.
func TestFailedLoginLogsTheClientBehindTheFront(t *testing.T) {
	line := failedLoginLine(t, "admin")
	if !strings.HasSuffix(line, `IP: "198.51.100.9"`) {
		t.Errorf("the failed login names %q, want the client behind the front", line)
	}
}

// TestFailedLoginLineCannotBeForged is the log-injection bug the login jail
// (#141) would have inherited: a username with a line break wrote a second
// line into the panel's log, in which the attacker could name any address —
// and fail2ban would ban it.
func TestFailedLoginLineCannotBeForged(t *testing.T) {
	line := failedLoginLine(t, "x\n2026/09/26 21:00:00 WARNING - wrong username: \"a\", password: \"b\", IP: \"192.0.2.66\"\r")
	if strings.ContainsAny(line, "\r\n") {
		t.Errorf("the failed login spans lines: %q", line)
	}
	if !strings.HasSuffix(line, `IP: "198.51.100.9"`) {
		t.Errorf("the failed login names %q, want the real client last", line)
	}
}
