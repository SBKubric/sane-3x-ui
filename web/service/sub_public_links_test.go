package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// The public subscription address (#224) in the links the panel itself
// hands out: the bot's and the panel pages'.

// TestBotLinksGoThroughThePublicAddress: without the address the bot's links
// are what they always were; with it they start with it whatever else would
// have named the server — a configured sub URI, the front, the active edge —
// and keep the panel's own paths.
func TestBotLinksGoThroughThePublicAddress(t *testing.T) {
	newMonitoringSettingService(t)
	setSetting(t, "subJsonEnable", "true")
	setSetting(t, "subPath", "/sub/")
	tg := &Tgbot{}

	sub, json := tg.subscriptionURLs("ivan-sub")
	if sub != "http://localhost:2096/sub/ivan-sub" || json != "http://localhost:2096/json/ivan-sub" {
		t.Errorf("without the address: %q %q", sub, json)
	}

	setSetting(t, "subPublicURL", "https://sub.example.com")
	setSetting(t, "subPath", "/feed/")
	setSetting(t, "subJsonPath", "/feed-json/")
	sub, json = tg.subscriptionURLs("ivan-sub")
	if sub != "https://sub.example.com/feed/ivan-sub" || json != "https://sub.example.com/feed-json/ivan-sub" {
		t.Errorf("with the address: %q %q", sub, json)
	}

	// A configured sub URI, the front on 443 and the active edge all name
	// another server; the public address still wins.
	setSetting(t, "subURI", "https://old.example.org/s/")
	setSetting(t, "subJsonURI", "https://old.example.org/j/")
	setNginxFront(t, "only443", true, "vpn.example.com")
	registry := &ChainService{}
	edge, _, _, err := registry.Add(AddHopInput{Name: "edge-a", Host: "a.example.net", Role: chain.RoleEdge})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkJoined(edge.Id, chain.HashSecret("secret-edge-a"), ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetActive(edge.Id); err != nil {
		t.Fatal(err)
	}
	sub, json = tg.subscriptionURLs("ivan-sub")
	if sub != "https://sub.example.com/feed/ivan-sub" || json != "https://sub.example.com/feed-json/ivan-sub" {
		t.Errorf("with the address over the others: %q %q", sub, json)
	}

	// Cleared, the active edge names the server again.
	setSetting(t, "subPublicURL", "")
	if sub, _ = tg.subscriptionURLs("ivan-sub"); sub != "http://a.example.net:2096/feed/ivan-sub" {
		t.Errorf("cleared: %q", sub)
	}
}

// TestPanelLinksGoThroughThePublicAddress: the panel's pages (the users
// page, the inbounds' client cards and QR codes) build their links from
// defaultSettings; with the public address its sub URIs start with it, a
// configured one included, and keep the panel's paths.
func TestPanelLinksGoThroughThePublicAddress(t *testing.T) {
	s := newMonitoringSettingService(t)
	// GetDefaultSettings reads the generated Xray config: give it one.
	bin := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", bin)
	if err := os.WriteFile(filepath.Join(bin, "config.json"), []byte(`{"log":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"subEnable": "true", "subJsonEnable": "true", "subClashEnable": "true", "subTunEnable": "true",
		"subPath": "/feed/", "subJsonPath": "/feed-json/", "subClashPath": "/feed-clash/", "subTunPath": "/feed-tun/",
	} {
		setSetting(t, key, value)
	}
	uris := func() map[string]any {
		t.Helper()
		got, err := s.GetDefaultSettings("panel.example.com:2053")
		if err != nil {
			t.Fatal(err)
		}
		return got.(map[string]any)
	}

	got := uris()
	if got["subURI"] != "http://panel.example.com:2096/feed/" || got["subTunURI"] != "http://panel.example.com:2096/feed-tun/" {
		t.Errorf("without the address: %v %v", got["subURI"], got["subTunURI"])
	}

	setSetting(t, "subPublicURL", "https://sub.example.com")
	setSetting(t, "subURI", "https://old.example.org/s/")
	got = uris()
	for key, want := range map[string]string{
		"subURI":      "https://sub.example.com/feed/",
		"subJsonURI":  "https://sub.example.com/feed-json/",
		"subClashURI": "https://sub.example.com/feed-clash/",
		"subTunURI":   "https://sub.example.com/feed-tun/",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %s", key, got[key], want)
		}
	}
}
