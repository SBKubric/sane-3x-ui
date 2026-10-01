package sub

import (
	"encoding/json"
	"html"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/subpage"
)

// The subscription page (#235) as the panel's sub server renders it: the
// template of package subpage, filled from the database and the settings.

// pageCard is one configuration card of a rendered page.
type pageCard struct {
	Name  string
	Label string
	Link  string
	JSON  string
	Conf  string
}

// pageCards are the page's cards with the given data-testid, in page order.
func pageCards(body, testID string) []pageCard {
	var cards []pageCard
	parts := strings.Split(body, `data-testid="`+testID+`"`)[1:]
	for _, part := range parts {
		part = part[:strings.Index(part, "</article>")]
		cards = append(cards, pageCard{
			Name:  pageField(part, `<span class="name">(.*?)</span>`),
			Label: pageField(part, `data-testid="sub-label">(.*?)<`),
			Link:  pageField(part, `data-role="link">(.*?)</code>`),
			JSON:  pageField(part, `data-role="json">(.*?)</code>`),
			Conf:  pageField(part, `data-role="conf">(.*?)</code>`),
		})
	}
	return cards
}

func pageField(block, re string) string {
	m := regexp.MustCompile(`(?s)` + re).FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

// pageApps are the app cards of a rendered page: name, URL and labels.
func pageApps(body string) []subpage.App {
	var apps []subpage.App
	for _, part := range strings.Split(body, `data-testid="sub-app"`)[1:] {
		part = part[:strings.Index(part, "</a>")]
		app := subpage.App{
			Name:      pageField(part, `<strong>(.*?)</strong>`),
			Platform:  pageField(part, `<small>(.*?)</small>`),
			URL:       pageField(part, `href="(.*?)"`),
			Protocols: []string{},
		}
		for _, m := range regexp.MustCompile(`data-testid="sub-app-label">(.*?)<`).FindAllStringSubmatch(part, -1) {
			app.Protocols = append(app.Protocols, html.UnescapeString(m[1]))
		}
		apps = append(apps, app)
	}
	return apps
}

// storeXhttpClient writes an enabled VLESS + XHTTP inbound with one client
// of subId.
func storeXhttpClient(t *testing.T, id int, email, subId string) {
	t.Helper()
	ib := &model.Inbound{Id: id, Port: 20000 + id, Protocol: model.VLESS, Tag: "in-xhttp-" + email, Remark: "nl", Enable: true,
		Settings:       `{"clients":[{"id":"bbbbbbbb-0000-0000-0000-00000000000` + strconv.Itoa(id) + `","email":"` + email + `","enable":true,"subId":"` + subId + `"}],"decryption":"none"}`,
		StreamSettings: `{"network":"xhttp","security":"none","xhttpSettings":{"path":"/api","mode":"auto"}}`, Sniffing: "{}"}
	if err := database.GetDB().Create(ib).Error; err != nil {
		t.Fatal(err)
	}
}

const ownersApps = `[
  {"name": "Owner App", "platform": "Android", "url": "https://example.com/owner-app", "protocols": ["VLESS + XHTTP"]},
  {"name": "Tunnel App", "platform": "Windows", "url": "https://example.net/tunnel-app", "protocols": ["AWG 2", "awg 3"]}
]`

func TestSubPageShowsTheDefaultApps(t *testing.T) {
	engine := newTunTestServer(t, nil)
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")

	body := getTun(t, engine, "/sub/page-ivan", browser).Body.String()
	if got, want := pageApps(body), subpage.DefaultApps(); !reflect.DeepEqual(got, want) {
		t.Errorf("page apps =\n%+v\nwant the default list\n%+v", got, want)
	}
}

func TestSubPageShowsTheOwnersApps(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subPageApps": ownersApps})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")

	body := getTun(t, engine, "/sub/page-ivan", browser).Body.String()
	want := []subpage.App{
		{Name: "Owner App", Platform: "Android", URL: "https://example.com/owner-app", Protocols: []string{"VLESS + XHTTP"}},
		{Name: "Tunnel App", Platform: "Windows", URL: "https://example.net/tunnel-app", Protocols: []string{"AWG 2", "AWG 3"}},
	}
	if got := pageApps(body); !reflect.DeepEqual(got, want) {
		t.Errorf("page apps =\n%+v\nwant\n%+v", got, want)
	}
}

// A list written past the form that does not parse leaves the page with the
// default list rather than without a page.
func TestSubPageWithABrokenAppList(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subPageApps": `[{"name": "x"}]`})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")

	rec := getTun(t, engine, "/sub/page-ivan", browser)
	if rec.Code != http.StatusOK {
		t.Fatalf("page: %d", rec.Code)
	}
	if got := pageApps(rec.Body.String()); !reflect.DeepEqual(got, subpage.DefaultApps()) {
		t.Errorf("page apps = %+v, want the default list", got)
	}
}

func TestSubPageLabelsEveryConfiguration(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subJsonEnable": "false"})
	storeXhttpClient(t, 1, "ivan-xhttp", "page-ivan")
	storeVlessClient(t, 2, "ivan-tcp", "page-ivan")
	addTunnelPeer(t, model.TunnelKindAwg, "ivan-phone", "page-ivan")

	body := getTun(t, engine, "/sub/page-ivan", browser).Body.String()
	links := pageCards(body, "sub-link")
	if len(links) != 2 {
		t.Fatalf("%d link cards, want 2:\n%s", len(links), body)
	}
	labels := map[string]string{}
	for _, card := range links {
		if card.Link == "" || card.JSON == "" {
			t.Errorf("card %+v lacks its link or its JSON", card)
		}
		labels[card.Label] = card.Link
	}
	if !strings.Contains(labels["VLESS + XHTTP"], "type=xhttp") || !strings.Contains(labels["VLESS + TCP"], "type=tcp") {
		t.Errorf("link labels = %v", labels)
	}
	tunnels := pageCards(body, "sub-tunnel")
	if len(tunnels) != 1 || !strings.HasPrefix(tunnels[0].Label, "AWG ") || !strings.HasPrefix(tunnels[0].Conf, "[Interface]") {
		t.Errorf("tunnel cards = %+v", tunnels)
	}
	if !strings.Contains(body, "Test VPN") {
		t.Error("the page does not carry the subscription title")
	}
}

// TestSubAppsListOnTheSubscriptionPath: a hop renders the same page and
// takes the owner's app list from its next hop, by the subscription path —
// for a subscription that exists, tunnels alone included.
func TestSubAppsListOnTheSubscriptionPath(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subPageApps": ownersApps, "subEncrypt": "true"})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")
	addTunnelPeer(t, model.TunnelKindAwg, "solo-phone", "page-solo")

	for _, subId := range []string{"page-ivan", "page-solo"} {
		rec := getTun(t, engine, "/sub/"+subId+"?format=apps", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s format=apps: %d %s", subId, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q", ct)
		}
		var apps []subpage.App
		if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil {
			t.Fatalf("not a list of apps: %v\n%s", err, rec.Body.String())
		}
		want, _ := subpage.ParseApps(ownersApps)
		if !reflect.DeepEqual(apps, want) {
			t.Errorf("%s apps = %+v, want %+v", subId, apps, want)
		}
	}
	if rec := getTun(t, engine, "/sub/nobody?format=apps", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown subscription: %d, want the 400 /sub answers", rec.Code)
	}
}
