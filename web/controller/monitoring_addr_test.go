package controller_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// TestMonServerAddressIsLearnedFromItsCalls: the front exempts mon-server
// from its limits and bans (#141), and the panel learns where mon-server is
// from mon-server's own authorised calls — through the front, the address
// nginx names in X-Real-IP. A caller without the token teaches it nothing.
func TestMonServerAddressIsLearnedFromItsCalls(t *testing.T) {
	r := newMonRouter(t)
	enableMonitoring(t)
	var mon service.MonitoringService

	call := func(token, ip string) {
		req := httptest.NewRequest(http.MethodGet, "/mon/v1/state", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-Real-IP", ip)
		req.RemoteAddr = "127.0.0.1:40000"
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	call("wrong-token", "192.0.2.66")
	if got := mon.MonServerAddr(); got != "" {
		t.Errorf("an unauthorised caller was taken for mon-server: %q", got)
	}
	call(monTestToken, "203.0.113.40")
	if got := mon.MonServerAddr(); got != "203.0.113.40" {
		t.Errorf("mon-server's address = %q, want the one it called from", got)
	}
	// mon-server moved: the new address replaces the old one.
	call(monTestToken, "2001:db8::40")
	if got := mon.MonServerAddr(); got != "2001:db8::40" {
		t.Errorf("mon-server's address = %q after it moved", got)
	}
}
