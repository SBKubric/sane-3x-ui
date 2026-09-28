package service

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTunnelSubSettings: the three keys of docs/spec/tunnel-subscription.md
// §5 have their defaults, survive a settings save with the path's slashes
// normalised, and reach the info modal through GetDefaultSettings.
func TestTunnelSubSettings(t *testing.T) {
	s := newMonitoringSettingService(t)

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("GetAllSetting: %v", err)
	}
	if !all.SubTunEnable || all.SubTunPath != "/tun/" || all.SubTunURI != "" {
		t.Errorf("defaults: enable=%v path=%q uri=%q", all.SubTunEnable, all.SubTunPath, all.SubTunURI)
	}

	all.SubTunEnable = false
	all.SubTunPath = "tunnels"
	all.SubTunURI = "https://sub.example.com/tunnels/"
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatalf("UpdateAllSetting: %v", err)
	}
	enable, _ := s.GetSubTunEnable()
	path, _ := s.GetSubTunPath()
	uri, _ := s.GetSubTunURI()
	if enable || path != "/tunnels/" || uri != "https://sub.example.com/tunnels/" {
		t.Errorf("after save: enable=%v path=%q uri=%q", enable, path, uri)
	}

	// The info modal gets the configured URI as is, or one built like
	// subJsonURI when none is configured. GetDefaultSettings reads the
	// generated Xray config, so give it one.
	bin := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", bin)
	if err := os.WriteFile(filepath.Join(bin, "config.json"), []byte(`{"log":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	defaults := func() map[string]any {
		t.Helper()
		got, err := s.GetDefaultSettings("panel.example.com:2053")
		if err != nil {
			t.Fatal(err)
		}
		return got.(map[string]any)
	}
	if got := defaults(); got["subTunEnable"] != false || got["subTunURI"] != "https://sub.example.com/tunnels/" {
		t.Errorf("GetDefaultSettings: subTunEnable=%v subTunURI=%v", got["subTunEnable"], got["subTunURI"])
	}
	all.SubTunEnable = true
	all.SubTunURI = ""
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	got := defaults()
	if got["subTunEnable"] != true || got["subTunURI"] != "http://panel.example.com:2096/tunnels/" {
		t.Errorf("GetDefaultSettings built: subTunEnable=%v subTunURI=%v", got["subTunEnable"], got["subTunURI"])
	}
}
