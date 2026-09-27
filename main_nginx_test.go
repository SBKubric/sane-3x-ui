package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/config"
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// `x-ui nginx acme-front` is what install.sh, update.sh and x-ui.sh run before
// every certificate issuance. What it does lives in package nginx; these
// tests cover the command line only.

func runNginx(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := nginxCommand(args, &out)
	return code, out.String()
}

func TestNginxCommandNeedsASubcommand(t *testing.T) {
	code, out := runNginx(t)
	if code == 0 {
		t.Error("bare `x-ui nginx` succeeded")
	}
	if !strings.Contains(out, "acme-front") {
		t.Errorf("the usage line does not name acme-front: %q", out)
	}
}

func TestNginxCommandRejectsAnUnknownSubcommand(t *testing.T) {
	code, out := runNginx(t, "acme-back")
	if code == 0 || !strings.Contains(out, "unknown subcommand") {
		t.Errorf("`x-ui nginx acme-back` → %d %q", code, out)
	}
}

// `x-ui nginx mode` is how update.sh learns whether the panel's front is in
// only443, and so whether fail2ban belongs on the machine (#141).
func TestNginxModeCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)

	// A box — no panel database at all — has no panel front.
	if code, out := runNginx(t, "mode"); code != 0 || out != "off\n" {
		t.Errorf("without a database: %d %q", code, out)
	}

	if err := database.InitDB(config.GetDBPath()); err != nil {
		t.Fatal(err)
	}
	svc := &service.NginxService{}
	if err := svc.SaveSettings(service.NginxSettings{Mode: "only443", RealityPort: 8443}); err != nil {
		t.Fatal(err)
	}
	database.CloseDB()
	if code, out := runNginx(t, "mode"); code != 0 || out != "only443\n" {
		t.Errorf("a panel in only443: %d %q", code, out)
	}
}
