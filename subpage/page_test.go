package subpage

import (
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const (
	xhttpLink = "vless://11111111-2222-3333-4444-555555555555@vpn.example.com:443?type=xhttp&security=reality&path=%2Fapi&mode=auto#nl"
	tcpLink   = "vless://11111111-2222-3333-4444-555555555555@vpn.example.com:8443?type=tcp&security=reality#de"
	xhttpJSON = "{\n  \"remarks\": \"nl\",\n  \"outbounds\": [{\"protocol\": \"vless\", \"tag\": \"proxy\"}]\n}"
)

func testPage() Page {
	return Page{
		Title:   "Example VPN",
		SubURL:  "https://sub.example.com/sub/abc",
		SubURLs: []NamedURL{{Name: "JSON", URL: "https://sub.example.com/json/abc"}},
		Usage:   Usage{Known: true, Up: 1 << 20, Down: 1 << 20, Total: 10 << 30},
		Links:   []Link{{URL: xhttpLink, JSON: xhttpJSON}, {URL: tcpLink}},
		Tunnels: []Tunnel{{Kind: "awg", Name: "phone", Filename: "phone", Enable: true, Conf: awg3Conf}},
		Apps: []App{
			{Name: "First App", Platform: "Android", URL: "https://example.com/first", Protocols: []string{"VLESS + XHTTP", "AWG 3"}},
			{Name: "Second App", Platform: "iOS", URL: "https://example.net/second", Protocols: []string{}},
		},
	}
}

func render(t *testing.T, p Page, header map[string]string) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/abc", nil)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	Render(w, r, p)
	if w.Code != http.StatusOK {
		t.Fatalf("Render: status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Render: content type %q", ct)
	}
	return w.Body.String()
}

// blocks cuts the page into the elements carrying a data-testid, in page
// order, each up to the end of its element.
func blocks(page, testID string) []string {
	marker := `data-testid="` + testID + `"`
	parts := strings.Split(page, marker)[1:]
	for i, part := range parts {
		end := len(part)
		for _, closer := range []string{"</article>", "</a>", "</dl>", "</header>", `<div class="configs">`} {
			if j := strings.Index(part, closer); j >= 0 && j+len(closer) < end {
				end = j + len(closer)
			}
		}
		parts[i] = part[:end]
	}
	return parts
}

func TestThePageLeadsWithTheWarning(t *testing.T) {
	page := render(t, testPage(), nil)
	main := page[strings.Index(page, "<main"):]
	first := regexp.MustCompile(`<main[^>]*>\s*<([a-z]+)[^>]*data-testid="([^"]+)"`).FindStringSubmatch(main)
	if first == nil || first[2] != "sub-warning" {
		t.Fatalf("the first block of <main> is %v, want the warning", first)
	}
	warning := blocks(page, "sub-warning")[0]
	if i := strings.Index(warning, "<h1"); i < 0 || i > strings.Index(warning, "</header>") {
		t.Fatalf("the warning is not the page's heading:\n%s", warning[:min(len(warning), 600)])
	}
	if !strings.Contains(warning, "AmneziaVPN") {
		t.Error("the warning does not give its example")
	}
	for _, later := range []string{`data-testid="sub-app"`, `data-testid="sub-link"`, `data-testid="sub-tunnel"`, `data-testid="sub-steps"`} {
		if strings.Index(page, later) < strings.Index(page, `data-testid="sub-warning"`) {
			t.Errorf("%s comes before the warning", later)
		}
	}
}

func TestTheAppCardsCarryTheirLabels(t *testing.T) {
	page := render(t, testPage(), nil)
	apps := blocks(page, "sub-app")
	if len(apps) != 2 {
		t.Fatalf("%d app cards, want 2", len(apps))
	}
	if !strings.Contains(apps[0], `href="https://example.com/first"`) || !strings.Contains(apps[0], "First App") || !strings.Contains(apps[0], "Android") {
		t.Errorf("first card:\n%s", apps[0])
	}
	if got := labelsIn(apps[0], "sub-app-label"); strings.Join(got, "|") != "VLESS + XHTTP|AWG 3" {
		t.Errorf("first card labels = %q", got)
	}
	if got := labelsIn(apps[1], "sub-app-label"); len(got) != 0 {
		t.Errorf("second card labels = %q, want none", got)
	}
}

func labelsIn(block, testID string) []string {
	var out []string
	re := regexp.MustCompile(`data-testid="` + testID + `"[^>]*>([^<]*)<`)
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

func TestEveryConfigurationCarriesItsLabel(t *testing.T) {
	page := render(t, testPage(), nil)
	links := blocks(page, "sub-link")
	if len(links) != 2 {
		t.Fatalf("%d link cards, want 2", len(links))
	}
	if got := labelsIn(links[0], "sub-label"); len(got) != 1 || got[0] != "VLESS + XHTTP" {
		t.Errorf("xhttp card label = %q", got)
	}
	if got := labelsIn(links[1], "sub-label"); len(got) != 1 || got[0] != "VLESS + TCP" {
		t.Errorf("tcp card label = %q", got)
	}
	tunnels := blocks(page, "sub-tunnel")
	if len(tunnels) != 1 {
		t.Fatalf("%d tunnel cards, want 1", len(tunnels))
	}
	if got := labelsIn(tunnels[0], "sub-label"); len(got) != 1 || got[0] != "AWG 3" {
		t.Errorf("tunnel card label = %q", got)
	}
}

func TestTheLinkCardsOfferTheirCopies(t *testing.T) {
	page := render(t, testPage(), nil)
	links := blocks(page, "sub-link")
	if !strings.Contains(links[0], `data-testid="sub-copy-link"`) || !strings.Contains(links[0], `data-testid="sub-copy-json"`) {
		t.Errorf("the xhttp card lacks a copy button:\n%s", links[0])
	}
	if got := jsonIn(links[0]); got != xhttpJSON {
		t.Errorf("the xhttp card carries JSON %q, want %q", got, xhttpJSON)
	}
	if strings.Contains(links[1], `data-testid="sub-copy-json"`) {
		t.Error("a link without JSON offers to copy JSON")
	}
	if !strings.Contains(links[1], `data-testid="sub-copy-link"`) {
		t.Error("the tcp card cannot copy its link")
	}
	if !strings.Contains(html.UnescapeString(links[0]), xhttpLink) {
		t.Error("the xhttp card does not show its link")
	}
	if !strings.Contains(page, `data-testid="sub-do-not-share"`) {
		t.Error("the page does not warn against forwarding the configurations")
	}
}

// jsonIn is the JSON config a link card carries.
func jsonIn(block string) string {
	m := regexp.MustCompile(`(?s)data-testid="sub-json"[^>]*>(.*?)</code>`).FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

func TestTheTunnelCardOffersTheConfAndItsQR(t *testing.T) {
	page := render(t, testPage(), nil)
	tunnel := blocks(page, "sub-tunnel")[0]
	for _, want := range []string{`data-testid="sub-tunnel-conf"`, `data-download="phone.conf"`, "phone.conf", `src="data:image/png;base64,`, "HeaderProtectionKey"} {
		if !strings.Contains(tunnel, want) {
			t.Errorf("tunnel card lacks %q", want)
		}
	}
}

func TestThePageNeedsNothingFromElsewhere(t *testing.T) {
	page := render(t, testPage(), nil)
	for _, re := range []string{`<script[^>]+src=`, `<link[^>]+rel="stylesheet"`, `@import`, `url\(\s*['"]?https?:`, `fonts\.googleapis`} {
		if loc := regexp.MustCompile(re).FindStringIndex(page); loc != nil {
			t.Errorf("the page loads something from elsewhere: %q", page[loc[0]:min(len(page), loc[1]+60)])
		}
	}
	for _, want := range []string{"prefers-color-scheme: dark", `data-theme="dark"`, "--bg:", "viewport"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestThePageSpeaksTheVisitorsLanguage(t *testing.T) {
	ru := render(t, testPage(), map[string]string{"Accept-Language": "ru-RU,ru;q=0.9,en;q=0.5"})
	if !strings.Contains(ru, `<html lang="ru-RU"`) || !strings.Contains(ru, "Скопировать ссылку") {
		t.Errorf("Accept-Language ru gave another page:\n%s", ru[:min(len(ru), 300)])
	}
	en := render(t, testPage(), map[string]string{"Accept-Language": "ru-RU", "Cookie": "lang=en-US"})
	if !strings.Contains(en, `<html lang="en-US"`) || !strings.Contains(en, "Copy link") {
		t.Error("the lang cookie does not win over Accept-Language")
	}
	if page := render(t, testPage(), map[string]string{"Accept-Language": "xx"}); !strings.Contains(page, "Copy link") {
		t.Error("an unknown language does not fall back to English")
	}
	if fa := render(t, testPage(), map[string]string{"Accept-Language": "fa"}); !strings.Contains(fa, `dir="rtl"`) {
		t.Error("Persian is not right to left")
	}
}

func TestThePageEscapesWhatItShows(t *testing.T) {
	p := testPage()
	p.Links = []Link{{URL: `vless://x@vpn.example.com:443?type=tcp#<script>alert(1)</script>`, JSON: `{"remarks": "</code><script>alert(2)</script>"}`}}
	p.Apps = []App{{Name: `<b>App</b>`, URL: "https://example.com/?a=1&b=2", Protocols: []string{}}}
	page := render(t, p, nil)
	for _, bad := range []string{"<script>alert(1)", "<script>alert(2)", "<b>App</b>"} {
		if strings.Contains(page, bad) {
			t.Errorf("page carries %q unescaped", bad)
		}
	}
}

func TestAPageOfTunnelsAlone(t *testing.T) {
	p := testPage()
	p.SubURL, p.SubURLs, p.Links = "", nil, nil
	page := render(t, p, nil)
	if strings.Contains(page, `data-testid="sub-subscription"`) || strings.Contains(page, `data-testid="sub-link"`) {
		t.Error("a page of tunnels alone shows a subscription link")
	}
	if len(blocks(page, "sub-tunnel")) != 1 {
		t.Error("a page of tunnels alone lost its tunnel")
	}
}

func TestTheSubscriptionCard(t *testing.T) {
	page := render(t, testPage(), nil)
	sub := blocks(page, "sub-subscription")
	if len(sub) != 1 {
		t.Fatalf("%d subscription cards", len(sub))
	}
	for _, want := range []string{"https://sub.example.com/sub/abc", "https://sub.example.com/json/abc", `src="data:image/png;base64,`} {
		if !strings.Contains(sub[0], want) {
			t.Errorf("subscription card lacks %q", want)
		}
	}
	usage := blocks(page, "sub-usage")
	if len(usage) != 1 {
		t.Fatalf("%d usage blocks", len(usage))
	}
	for _, want := range []string{"2.00MB", "10.00GB", "Active"} {
		if !strings.Contains(usage[0], want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

func TestNoAppsSaysSo(t *testing.T) {
	p := testPage()
	p.Apps = nil
	page := render(t, p, nil)
	if len(blocks(page, "sub-app")) != 0 || !strings.Contains(page, `data-testid="sub-no-apps"`) {
		t.Error("a page without apps does not say so")
	}
}

func TestPlatformIcon(t *testing.T) {
	for platform, want := range map[string]string{
		"Android": "android", "iPhone / iPad": "apple", "iPhone / iPad / Mac": "apple", "Windows": "windows",
		"Windows / macOS / Linux": "", "Android / iOS": "", "Linux": "", "": "",
	} {
		if got := platformIcon(platform); got != want {
			t.Errorf("platformIcon(%q) = %q, want %q", platform, got, want)
		}
	}
}
