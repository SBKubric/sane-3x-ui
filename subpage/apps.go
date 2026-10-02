package subpage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// App is one client app card on the page: where to get it and the protocol
// labels it works with (#235). The owner sets the list in the panel's
// subscription settings (subPageApps) as a JSON list of these; empty, the
// page shows DefaultApps.
type App struct {
	Name      string   `json:"name"`
	Platform  string   `json:"platform"`
	URL       string   `json:"url"`
	Protocols []string `json:"protocols"`
}

// defaultApps are the apps the page recommends unless the owner says
// otherwise. A label is here only where the app's source code or its own
// release notes say it handles that configuration (checked 2026-10-01; the
// README lists the sources): VLESS + XHTTP means the share link's xhttp
// parameters — path, host, mode and extra — are read, AWG 3 that a .conf
// with the AmneziaWG 3.x keys is. An app whose support could not be
// confirmed keeps its card without the label.
//
// v2RayTun leads (#217); its VLESS + XHTTP label rests on the owner's own
// check of our links on Android and iOS (2026-10-01), its release notes not
// being public. V2rayNG is left out, it does not read
// Profile-Update-Interval (#217). AmneziaVPN reads AWG 3 but not the xhttp
// parameters of a VLESS link — the example the page's warning gives. DefaultVPN's
// VLESS + XHTTP label rests on the owner's check (2026-10-01).
//
// An app downloaded from GitHub has a card per platform that downloads the
// file itself (owner, 2026-10-02): Happ and v2rayN name their files without
// the version, so /releases/latest/download/<file> stays the newest one;
// Amnezia's files carry the version, so they are pinned here and a newer
// release of the panel moves them (AmneziaVPN 5.0.3.0, AmneziaWG for Windows
// 3.1.0). AmneziaVPN has no iOS build in the App Store; AmneziaWG covers it.
var defaultApps = []App{
	{Name: "v2RayTun", Platform: "Android", URL: "https://play.google.com/store/apps/details?id=com.v2raytun.android", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "v2RayTun", Platform: "iPhone / iPad", URL: "https://apps.apple.com/us/app/v2ray-vpn-client/id6752994543", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "Happ", Platform: "Android", URL: "https://play.google.com/store/apps/details?id=com.happproxy", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "Happ", Platform: "Windows", URL: "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/setup-Happ.x64.exe", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "Happ", Platform: "macOS", URL: "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.macOS.universal.dmg", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "Happ", Platform: "Linux (.deb)", URL: "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/Happ.linux.x64.deb", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "Shadowrocket", Platform: "iPhone / iPad", URL: "https://apps.apple.com/app/id932747118", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "V2Box", Platform: "iPhone / iPad", URL: "https://apps.apple.com/app/id6446814690", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "v2rayN", Platform: "Windows", URL: "https://github.com/2dust/v2rayN/releases/latest/download/v2rayN-windows-64-desktop.zip", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "v2rayN", Platform: "macOS (Apple Silicon)", URL: "https://github.com/2dust/v2rayN/releases/latest/download/v2rayN-macos-arm64.dmg", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "v2rayN", Platform: "macOS (Intel)", URL: "https://github.com/2dust/v2rayN/releases/latest/download/v2rayN-macos-64.dmg", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "v2rayN", Platform: "Linux (.deb)", URL: "https://github.com/2dust/v2rayN/releases/latest/download/v2rayN-linux-64.deb", Protocols: []string{"VLESS + XHTTP"}},
	{Name: "AmneziaVPN", Platform: "Android (.apk)", URL: "https://github.com/amnezia-vpn/amnezia-client/releases/download/5.0.3.0/AmneziaVPN_5.0.3.0_android11%2B_arm64-v8a.apk", Protocols: []string{LabelAWG3}},
	{Name: "AmneziaVPN", Platform: "Windows", URL: "https://github.com/amnezia-vpn/amnezia-client/releases/download/5.0.3.0/AmneziaVPN_5.0.3.0_windows_x64.exe", Protocols: []string{LabelAWG3}},
	{Name: "AmneziaVPN", Platform: "macOS", URL: "https://github.com/amnezia-vpn/amnezia-client/releases/download/5.0.3.0/AmneziaVPN_5.0.3.0_macos_x64.pkg", Protocols: []string{LabelAWG3}},
	{Name: "AmneziaVPN", Platform: "Linux", URL: "https://github.com/amnezia-vpn/amnezia-client/releases/download/5.0.3.0/AmneziaVPN_5.0.3.0_linux_x64.run", Protocols: []string{LabelAWG3}},
	{Name: "AmneziaWG", Platform: "Android", URL: "https://play.google.com/store/apps/details?id=org.amnezia.awg", Protocols: []string{LabelAWG3}},
	{Name: "AmneziaWG", Platform: "iPhone / iPad / Mac", URL: "https://apps.apple.com/app/id6478942365", Protocols: []string{LabelAWG3}},
	{Name: "AmneziaWG", Platform: "Windows", URL: "https://github.com/amnezia-vpn/amneziawg-windows-client/releases/download/3.1.0/amneziawg-amd64-3.1.0.msi", Protocols: []string{LabelAWG3}},
	{Name: "DefaultVPN", Platform: "iPhone / iPad", URL: "https://apps.apple.com/app/id6744725017", Protocols: []string{"VLESS + XHTTP", LabelAWG3}},
}

// DefaultApps is a copy of the built-in list.
func DefaultApps() []App {
	apps := make([]App, len(defaultApps))
	for i, app := range defaultApps {
		app.Protocols = slices.Clone(app.Protocols)
		apps[i] = app
	}
	return apps
}

// ParseApps reads the owner's app list. Blank is the built-in list; anything
// else must be a JSON list of apps, each with a name, an http(s) URL and
// labels a configuration can carry (KnownLabel) — a misspelt label would
// match nothing on the page.
func ParseApps(raw string) ([]App, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultApps(), nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var apps []App
	if err := decoder.Decode(&apps); err != nil {
		return nil, fmt.Errorf("the app list is not a JSON list of {name, platform, url, protocols}: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("the app list has data after its closing bracket")
	}
	for i := range apps {
		if err := checkApp(&apps[i]); err != nil {
			return nil, fmt.Errorf("app %d: %w", i+1, err)
		}
	}
	if apps == nil {
		apps = []App{}
	}
	return apps, nil
}

func checkApp(app *App) error {
	app.Name = strings.TrimSpace(app.Name)
	app.Platform = strings.TrimSpace(app.Platform)
	app.URL = strings.TrimSpace(app.URL)
	if app.Name == "" {
		return fmt.Errorf("no name")
	}
	u, err := url.Parse(app.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%s: the url must be an http(s) address, got %q", app.Name, app.URL)
	}
	labels := make([]string, 0, len(app.Protocols))
	for _, label := range app.Protocols {
		if strings.TrimSpace(label) == "" {
			continue
		}
		canonical, ok := canonicalLabel(label)
		if !ok {
			return fmt.Errorf("%s: unknown protocol label %q (labels look like %q, %q, %q)", app.Name, label, "VLESS + XHTTP", LabelAWG3, LabelWireGuard)
		}
		labels = append(labels, canonical)
	}
	app.Protocols = labels
	return nil
}

// AppsJSON is the list as the settings form shows it: indented JSON.
func AppsJSON(apps []App) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(apps); err != nil {
		return "[]"
	}
	return strings.TrimSpace(out.String())
}

// NormalizeAppsSetting is the value to store for the subPageApps setting:
// the owner's list checked and re-indented, or "" for the built-in list —
// blank, or the built-in list itself as the form showed it — so a panel
// that never changed it follows the next release's defaults.
func NormalizeAppsSetting(raw string) (string, error) {
	apps, err := ParseApps(raw)
	if err != nil {
		return "", err
	}
	normalized := AppsJSON(apps)
	if normalized == AppsJSON(DefaultApps()) {
		return "", nil
	}
	return normalized, nil
}
