package clientrouting

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// issueRouting is the rule set of #263, the one dinosub hands its apps.
const issueRouting = `{"name": "RU", "domainStrategy": "IPIfNonMatch",
 "directSites": ["geosite:category-ru", "domain:ozon.ru", "domain:ozone.ru", "domain:o3t.ru", "domain:push.apple.com", "domain:ess.apple.com"],
 "directIp": ["geoip:ru", "geoip:private", "17.0.0.0/8", "185.73.192.0/22"],
 "directProtocols": ["bittorrent"],
 "proxySites": ["domain:acomics.ru"], "proxyIp": [], "blockSites": [], "blockIp": [],
 "geositeUrl": "", "geoipUrl": ""}`

func mustParse(t *testing.T, value string) *Routing {
	t.Helper()
	r, err := Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParse_Empty(t *testing.T) {
	for _, v := range []string{"", "  \n"} {
		r, err := Parse(v)
		if r != nil || err != nil {
			t.Errorf("%q: %v, %v", v, r, err)
		}
	}
}

func TestParse_IssueRuleSet(t *testing.T) {
	r := mustParse(t, issueRouting)
	if r.Name != "RU" || r.DomainStrategy != "IPIfNonMatch" || len(r.DirectSites) != 6 || len(r.DirectIP) != 4 ||
		!reflect.DeepEqual(r.DirectProtocols, []string{"bittorrent"}) || !reflect.DeepEqual(r.ProxySites, []string{"domain:acomics.ru"}) {
		t.Errorf("parsed %+v", r)
	}
}

func TestParse_Refusals(t *testing.T) {
	cases := map[string]string{
		"not JSON":          `{"name":`,
		"unknown field":     `{"name":"RU","domainStrategy":"AsIs","directSite":["geosite:category-ru"]}`,
		"two objects":       `{"name":"RU","domainStrategy":"AsIs"} {}`,
		"no name":           `{"name":" ","domainStrategy":"AsIs"}`,
		"long name":         `{"name":"` + strings.Repeat("x", 65) + `","domainStrategy":"AsIs"}`,
		"no strategy":       `{"name":"RU"}`,
		"strategy":          `{"name":"RU","domainStrategy":"IPIfNonMatched"}`,
		"site prefix":       `{"name":"RU","domainStrategy":"AsIs","directSites":["geo:ru"]}`,
		"empty site":        `{"name":"RU","domainStrategy":"AsIs","proxySites":[""]}`,
		"site with a space": `{"name":"RU","domainStrategy":"AsIs","blockSites":["domain:a b"]}`,
		"site prefix only":  `{"name":"RU","domainStrategy":"AsIs","directSites":["geosite:"]}`,
		"ip":                `{"name":"RU","domainStrategy":"AsIs","directIp":["17.0.0.0/33"]}`,
		"ip word":           `{"name":"RU","domainStrategy":"AsIs","proxyIp":["ru"]}`,
		"protocol":          `{"name":"RU","domainStrategy":"AsIs","directProtocols":["torrent"]}`,
		"geosite url":       `{"name":"RU","domainStrategy":"AsIs","geositeUrl":"ftp://example.com/geosite.dat"}`,
		"geoip url":         `{"name":"RU","domainStrategy":"AsIs","geoipUrl":"geoip.dat"}`,
	}
	for name, value := range cases {
		if r, err := Parse(value); err == nil {
			t.Errorf("%s: accepted %+v", name, r)
		}
	}
}

func TestParse_AcceptsXrayForms(t *testing.T) {
	r := mustParse(t, `{"name":"x","domainStrategy":"AsIs",
		"directSites":["keyword:ya","full:ya.ru","regexp:^ya\\.ru$","ext:h2y.dat:gfw","yandex"],
		"directIp":["geoip:ru","ext:geoip.dat:ru","10.0.0.1","2001:db8::/32"],
		"geositeUrl":"https://example.com/geosite.dat","geoipUrl":"http://example.com/geoip.dat"}`)
	if len(r.DirectSites) != 5 || len(r.DirectIP) != 4 {
		t.Errorf("parsed %+v", r)
	}
}

func TestNormalizeSetting(t *testing.T) {
	got, err := NormalizeSetting(`{"name":" RU ","domainStrategy":"AsIs","directSites":[" geosite:category-ru "]}`)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatal(err)
	}
	if back["name"] != "RU" || back["proxyIp"] == nil || !strings.Contains(got, "\n  \"directSites\": [\n    \"geosite:category-ru\"") {
		t.Errorf("normalized:\n%s", got)
	}
	// Normalizing the stored value again gives the same value.
	again, err := NormalizeSetting(got)
	if err != nil || again != got {
		t.Errorf("again: %v\n%s", err, again)
	}
	if empty, err := NormalizeSetting(" "); empty != "" || err != nil {
		t.Errorf("empty: %q, %v", empty, err)
	}
}

func TestClientOf(t *testing.T) {
	cases := map[string]Client{
		"Happ/3.13.0":                        Happ,
		"Happ/3.26.1/Android/0123456789abcd": Happ,
		"happ/2.0 (iPhone)":                  Happ,
		"v2raytun/android":                   V2RayTun,
		"v2raytun/5.21.68 (Android 14)":      V2RayTun,
		"v2RayTun/2.0":                       V2RayTun,
		"INCY/3.8.8/ios":                     Incy,
		"incy/1.0/android":                   Incy,
		"v2rayNG/1.9.30":                     None,
		"v2rayN/7.0":                         None,
		"Streisand/1.6":                      None,
		"clash-verge/v2.0":                   None,
		"Mozilla/5.0 (happening)":            None,
		"":                                   None,
	}
	for ua, want := range cases {
		if got := ClientOf(ua); got != want {
			t.Errorf("%q: %v, want %v", ua, got, want)
		}
	}
}

func decodeHappHeader(t *testing.T, header string) map[string]any {
	t.Helper()
	encoded, ok := strings.CutPrefix(header, "happ://routing/onadd/")
	if !ok {
		t.Fatalf("not a Happ onadd link: %q", header)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHeader_Happ(t *testing.T) {
	r := mustParse(t, issueRouting)
	header := Header("Happ/3.13.0", r, 1760745600)
	p := decodeHappHeader(t, header)
	want := map[string]any{
		"Name": "RU", "GlobalProxy": "true", "RouteOrder": "proxy-block-direct", "DomainStrategy": "IPIfNonMatch",
		"Geositeurl":  happDefaultGeosite,
		"Geoipurl":    happDefaultGeoip,
		"LastUpdated": "1760745600", "UseChunkFiles": "true",
		"DirectSites": []any{"geosite:category-ru", "domain:ozon.ru", "domain:ozone.ru", "domain:o3t.ru", "domain:push.apple.com", "domain:ess.apple.com"},
		"DirectIp":    []any{"geoip:ru", "geoip:private", "17.0.0.0/8", "185.73.192.0/22"},
		"ProxySites":  []any{"domain:acomics.ru"},
		"ProxyIp":     []any{}, "BlockSites": []any{}, "BlockIp": []any{},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("profile\n got %v\nwant %v", p, want)
	}
	// The same routing, the same header: Happ must not download its geo
	// files on every update of the subscription.
	if again := Header("Happ/3.13.0", r, 1760745600); again != header {
		t.Errorf("a second fetch differs:\n%s\n%s", header, again)
	}
}

func TestHeader_HappOwnGeoFiles(t *testing.T) {
	r := mustParse(t, `{"name":"RU","domainStrategy":"AsIs","geositeUrl":"https://example.com/s.dat","geoipUrl":"https://example.com/i.dat"}`)
	p := decodeHappHeader(t, Header("Happ/3.13.0", r, 0))
	if p["Geositeurl"] != "https://example.com/s.dat" || p["Geoipurl"] != "https://example.com/i.dat" || p["LastUpdated"] != "" {
		t.Errorf("profile %v", p)
	}
}

func decodeV2RayTunHeader(t *testing.T, header string) v2rayTunRouting {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("not base64: %q", header)
	}
	var o v2rayTunRouting
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestHeader_V2RayTun(t *testing.T) {
	r := mustParse(t, issueRouting)
	header := Header("v2raytun/android", r, 1760745600)
	o := decodeV2RayTunHeader(t, header)
	if o.Name != "RU" || o.DomainStrategy != "IPIfNonMatch" || o.DomainMatcher != "hybrid" || o.Balancers == nil || o.ID == "" {
		t.Errorf("object %+v", o)
	}
	type rule struct {
		tag    string
		values []string
	}
	var got []rule
	for _, r := range o.Rules {
		if r.Type != "field" || r.ID == "" || r.ID != r.IDExport || r.Name == "" {
			t.Errorf("rule %+v", r)
		}
		if r.Domain != nil && r.DomainMatcher != "hybrid" {
			t.Errorf("domain rule without the matcher: %+v", r)
		}
		// One condition per rule: an Xray rule needs all of its conditions.
		n := 0
		var values []string
		for _, v := range [][]string{r.Domain, r.IP, r.Protocol} {
			if v != nil {
				n++
				values = v
			}
		}
		if n != 1 {
			t.Errorf("rule with %d conditions: %+v", n, r)
		}
		got = append(got, rule{r.OutboundTag, values})
	}
	want := []rule{
		{"proxy", []string{"domain:acomics.ru"}},
		{"direct", []string{"geosite:category-ru", "domain:ozon.ru", "domain:ozone.ru", "domain:o3t.ru", "domain:push.apple.com", "domain:ess.apple.com"}},
		{"direct", []string{"geoip:ru", "geoip:private", "17.0.0.0/8", "185.73.192.0/22"}},
		{"direct", []string{"bittorrent"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rules\n got %v\nwant %v", got, want)
	}
	if again := Header("v2raytun/android", r, 1760745601); again != header {
		t.Error("the object moves between fetches of the same routing")
	}
}

func TestV2RayTunObject_BlockBetweenProxyAndDirect(t *testing.T) {
	r := mustParse(t, `{"name":"x","domainStrategy":"AsIs","directIp":["geoip:ru"],"blockSites":["geosite:category-ads-all"],"proxyIp":["1.1.1.1"]}`)
	o := decodeV2RayTunHeader(t, Header("v2RayTun/2.0", r, 0))
	var tags []string
	for _, r := range o.Rules {
		tags = append(tags, r.OutboundTag)
	}
	if strings.Join(tags, ",") != "proxy,block,direct" {
		t.Errorf("order %v", tags)
	}
}

func TestV2RayTunObject_StableIDs(t *testing.T) {
	a := decodeV2RayTunHeader(t, Header("v2raytun/android", mustParse(t, issueRouting), 0))
	changed := mustParse(t, strings.Replace(issueRouting, `"domain:acomics.ru"`, `"domain:acomics.ru", "domain:example.ru"`, 1))
	b := decodeV2RayTunHeader(t, Header("v2raytun/android", changed, 0))
	// The routing stays the same routing; only the rule that changed has a
	// new id.
	if a.ID != b.ID {
		t.Errorf("routing id moved: %s %s", a.ID, b.ID)
	}
	if a.Rules[0].ID == b.Rules[0].ID || a.Rules[1].ID != b.Rules[1].ID {
		t.Errorf("rule ids %s/%s %s/%s", a.Rules[0].ID, b.Rules[0].ID, a.Rules[1].ID, b.Rules[1].ID)
	}
	if strings.ToUpper(a.ID) != a.ID || len(a.ID) != 36 {
		t.Errorf("id %q is not an upper-case UUID, as the app's", a.ID)
	}
}

func TestHeader_OtherApps(t *testing.T) {
	r := mustParse(t, issueRouting)
	for _, ua := range []string{"v2rayNG/1.9.30", "", "curl/8.0", "Streisand/1.6"} {
		if h := Header(ua, r, 1); h != "" {
			t.Errorf("%q got %q", ua, h)
		}
	}
	if h := Header("Happ/3.13.0", nil, 1); h != "" {
		t.Errorf("no routing: %q", h)
	}
}

// TestHappProfile_RegexpKeepsBrackets: the profile is JSON as Happ reads
// it, not HTML-escaped.
func TestHappProfile_RegexpKeepsBrackets(t *testing.T) {
	r := mustParse(t, `{"name":"x","domainStrategy":"AsIs","directSites":["regexp:^a<b>&$"]}`)
	if !strings.Contains(string(HappProfile(r, 0)), `"regexp:^a<b>&$"`) {
		t.Errorf("profile %s", HappProfile(r, 0))
	}
}

// TestParse_RefusesAHeaderNginxWouldNotPass: a routing whose header would
// overflow the nginx response header buffer is refused, naming the app.
func TestParse_RefusesAHeaderNginxWouldNotPass(t *testing.T) {
	var sites []string
	for i := 0; i < 120; i++ {
		sites = append(sites, `"domain:site-number-`+strings.Repeat("x", 4)+string(rune('a'+i%26))+`.example.ru"`)
	}
	_, err := Parse(`{"name":"RU","domainStrategy":"AsIs","directSites":[` + strings.Join(sites, ",") + `]}`)
	if err == nil || !strings.Contains(err.Error(), "bytes, more than") {
		t.Errorf("err = %v", err)
	}
	if _, err := Parse(issueRouting); err != nil {
		t.Errorf("the rule set of #263: %v", err)
	}
}

// TestHeader_IncyGetsHappsLink: Incy reads Happ's profile from the same
// header (docs.incy.cc/en/routing) and gets exactly Happ's value.
func TestHeader_IncyGetsHappsLink(t *testing.T) {
	r := mustParse(t, issueRouting)
	if got, want := Header("INCY/3.8.8/ios", r, 1760745600), Header("Happ/3.13.0", r, 1760745600); got != want || got == "" {
		t.Errorf("Incy got %q, Happ %q", got, want)
	}
}
