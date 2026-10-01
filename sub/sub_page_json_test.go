package sub

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The subscription page offers the client JSON config of every link beside
// the link itself (#231): the same JSON the JSON subscription answers with,
// embedded in the page, so the copy works with the JSON subscription off.

// pageJSONConfigs pulls the JSON configs the page was rendered with out of
// its bootstrap element; ok is false when the page carries none.
func pageJSONConfigs(t *testing.T, body string) (configs []string, ok bool) {
	t.Helper()
	const attr = `data-json-configs="`
	i := strings.Index(body, attr)
	if i < 0 {
		return nil, false
	}
	rest := body[i+len(attr):]
	raw := html.UnescapeString(rest[:strings.Index(rest, `"`)])
	if err := json.Unmarshal([]byte(raw), &configs); err != nil {
		t.Fatalf("data-json-configs is not a JSON list of configs: %v\n%s", err, raw)
	}
	return configs, true
}

// pageLinkList is the subscription's links as the page lists them.
func pageLinkList(t *testing.T, body string) []string {
	t.Helper()
	const open = `<textarea id="subscription-links" style="display:none">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("the page has no link list:\n%s", body)
	}
	rest := body[i+len(open):]
	var links []string
	for _, line := range strings.Split(html.UnescapeString(rest[:strings.Index(rest, "</textarea>")]), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			links = append(links, line)
		}
	}
	return links
}

func decodeJSON(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	return v
}

// vnextPort is the port the config's proxy outbound dials.
func vnextPort(t *testing.T, raw string) float64 {
	t.Helper()
	var cfg struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Vnext []struct {
					Port float64 `json:"port"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("not a client config: %v\n%s", err, raw)
	}
	for _, ob := range cfg.Outbounds {
		if ob.Tag == "proxy" && len(ob.Settings.Vnext) == 1 {
			return ob.Settings.Vnext[0].Port
		}
	}
	t.Fatalf("no proxy outbound in\n%s", raw)
	return 0
}

// TestSubPageCarriesTheJSONSubscriptionOfALink: one link, one config, and
// that config is the JSON subscription's answer to the letter.
func TestSubPageCarriesTheJSONSubscriptionOfALink(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subJsonEnable": "true", "subJsonPath": "/json/"})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")

	page := getTun(t, engine, "/sub/page-ivan", browser)
	if page.Code != http.StatusOK {
		t.Fatalf("page: %d %s", page.Code, page.Body.String())
	}
	configs, ok := pageJSONConfigs(t, page.Body.String())
	if !ok || len(configs) != 1 {
		t.Fatalf("page JSON configs = %v (present %v), want one", configs, ok)
	}
	sub := getTun(t, engine, "/json/page-ivan", nil)
	if sub.Code != http.StatusOK {
		t.Fatalf("/json: %d %s", sub.Code, sub.Body.String())
	}
	if configs[0] != sub.Body.String() {
		t.Errorf("page JSON differs from the JSON subscription:\npage:\n%s\n/json:\n%s", configs[0], sub.Body.String())
	}
}

// TestSubPageJSONFollowsTheLinks: a config per link, in the links' order,
// each one the JSON subscription's config of that link.
func TestSubPageJSONFollowsTheLinks(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subJsonEnable": "true", "subJsonPath": "/json/"})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")
	storeVlessClient(t, 2, "ivan-de", "page-ivan")

	body := getTun(t, engine, "/sub/page-ivan", browser).Body.String()
	links := pageLinkList(t, body)
	configs, _ := pageJSONConfigs(t, body)
	if len(links) != 2 || len(configs) != 2 {
		t.Fatalf("links %v, configs %d: want two of each", links, len(configs))
	}
	var subConfigs []json.RawMessage
	if err := json.Unmarshal(getTun(t, engine, "/json/page-ivan", nil).Body.Bytes(), &subConfigs); err != nil {
		t.Fatalf("/json is not a list: %v", err)
	}
	for i, link := range links {
		if !reflect.DeepEqual(decodeJSON(t, configs[i]), decodeJSON(t, string(subConfigs[i]))) {
			t.Errorf("config %d differs from the JSON subscription's", i)
		}
		if port := vnextPort(t, configs[i]); !strings.Contains(link, fmt.Sprintf(":%d", int(port))) {
			t.Errorf("config %d dials port %v, its link is %s", i, port, link)
		}
	}
}

// TestSubPageJSONWithTheJSONSubscriptionOff: on a fresh install the JSON
// subscription is off and its path leads nowhere (the showcase does not
// even pass it), so the page names no JSON link, yet still carries the JSON.
func TestSubPageJSONWithTheJSONSubscriptionOff(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subJsonEnable": "false", "subJsonPath": "/json/"})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")

	body := getTun(t, engine, "/sub/page-ivan", browser).Body.String()
	if strings.Contains(body, "/json/page-ivan") {
		t.Errorf("the page links the JSON path of a JSON subscription that is off")
	}
	configs, ok := pageJSONConfigs(t, body)
	if !ok || len(configs) != 1 || vnextPort(t, configs[0]) != 20001 {
		t.Fatalf("page JSON configs = %v (present %v), want the config of the one link", configs, ok)
	}
	if rec := getTun(t, engine, "/json/page-ivan", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/json with the JSON subscription off: %d", rec.Code)
	}
}

// TestSubJSONListOnTheSubscriptionPath: the hops of a chain fetch the
// configs for their own page from the subscription path, which every hop
// and the showcase serve, as a JSON list in the links' order.
func TestSubJSONListOnTheSubscriptionPath(t *testing.T) {
	engine := newTunTestServer(t, map[string]string{"subJsonEnable": "false", "subEncrypt": "true"})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")
	storeVlessClient(t, 2, "ivan-de", "page-ivan")

	rec := getTun(t, engine, "/sub/page-ivan?format=json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("format=json: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var configs []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &configs); err != nil {
		t.Fatalf("not a JSON list: %v\n%s", err, rec.Body.String())
	}
	if len(configs) != 2 || vnextPort(t, string(configs[0])) != 20001 || vnextPort(t, string(configs[1])) != 20002 {
		t.Fatalf("configs = %s", rec.Body.String())
	}
	if rec.Header().Get("Subscription-Userinfo") == "" {
		t.Error("the list carries no Subscription-Userinfo")
	}

	if rec := getTun(t, engine, "/sub/nobody?format=json", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown subscription: %d, want the 400 /sub answers", rec.Code)
	}
}

// TestSubJsonServiceKeepsSharedServiceImmutable: the page now builds the
// JSON configs on every visit, beside the links, from the one SubService all
// requests share; the JSON service used to write the host override onto it
// and raced every concurrent GetSubs, as GetSubs once did itself.
func TestSubJsonServiceKeepsSharedServiceImmutable(t *testing.T) {
	newTunTestServer(t, map[string]string{"proxyOverrideEnable": "true", "proxyOverrideHost": "edge.example.com"})
	storeVlessClient(t, 1, "ivan-nl", "page-ivan")
	sub := NewSubService(false, "-ieo", "")
	svc := NewSubJsonService("", "", "", "", sub)

	var wg sync.WaitGroup
	for _, host := range []string{"host-a.example.com", "host-b.example.com"} {
		wg.Add(2)
		go func(h string) {
			defer wg.Done()
			for range 20 {
				if configs, _, err := svc.GetConfigs("page-ivan", h); err != nil || len(configs) != 1 {
					t.Errorf("GetConfigs: %v %v", configs, err)
					return
				}
			}
		}(host)
		go func(h string) {
			defer wg.Done()
			for range 20 {
				_, _, _, _ = sub.GetSubs("page-ivan", h)
			}
		}(host)
	}
	wg.Wait()

	if sub.overrideOn || sub.overrideHost != "" || sub.linkHost != "" {
		t.Errorf("shared SubService carries a request's override: on=%v host=%q link=%q", sub.overrideOn, sub.overrideHost, sub.linkHost)
	}
}
