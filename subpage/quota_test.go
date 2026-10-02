package subpage

import (
	"html"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const gb = int64(1) << 30

// testWords word sizes in whole GB, as the bot does.
var testWords = QuotaWords{
	Size:      func(b int64) string { return strconv.FormatInt(b/gb, 10) + " GB" },
	Unlimited: "unlimited",
	Protocols: [3]string{"protocol", "protocols", "protocols!"},
}

// TestQuotaText (#247): one client its limit; one limit for all «N × k»;
// different limits «A + B», a protocol two share named by its inbound; any
// unlimited «unlimited».
func TestQuotaText(t *testing.T) {
	vless := func(remark string, limit int64) TrafficPart {
		return TrafficPart{Protocol: "vless", Remark: remark, Limit: limit * gb}
	}
	awg := TrafficPart{Protocol: "amneziawg", Remark: "awg", Limit: 20 * gb}
	for _, c := range []struct {
		name  string
		parts []TrafficPart
		want  string
	}{
		{"none", nil, ""},
		{"one", []TrafficPart{vless("nl", 50)}, "50 GB"},
		{"equal", []TrafficPart{vless("nl", 50), {Protocol: "amneziawg", Limit: 50 * gb}}, "100 GB (50 GB × 2 protocols)"},
		{"different", []TrafficPart{vless("nl", 50), awg}, "70 GB (VLESS 50 GB + AmneziaWG 20 GB)"},
		{"same protocol twice", []TrafficPart{vless("xhttp", 50), vless("tcp", 30), awg},
			"100 GB (VLESS (xhttp) 50 GB + VLESS (tcp) 30 GB + AmneziaWG 20 GB)"},
		{"unlimited", []TrafficPart{vless("nl", 50), {Protocol: "amneziawg"}}, "unlimited"},
		{"all unlimited", []TrafficPart{{Protocol: "vless"}, {Protocol: "trojan"}}, "unlimited"},
		{"five equal", []TrafficPart{vless("a", 10), vless("b", 10), vless("c", 10), vless("d", 10), vless("e", 10)},
			"50 GB (10 GB × 5 protocols!)"},
	} {
		if got := QuotaText(c.parts, testWords); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPartLabels(t *testing.T) {
	parts := []TrafficPart{{Protocol: "vless", Remark: "xhttp"}, {Protocol: "vless", Remark: "tcp"}, {Protocol: "nativewg", Remark: "wg"},
		{Protocol: "trojan"}, {Protocol: "trojan"}, {Protocol: "tuic"}}
	want := []string{"VLESS (xhttp)", "VLESS (tcp)", "WireGuard", "Trojan", "Trojan", "TUIC"}
	if got := PartLabels(parts); !reflect.DeepEqual(got, want) {
		t.Errorf("labels %q, want %q", got, want)
	}
}

func TestPluralForm(t *testing.T) {
	for n, want := range map[int]int{1: 0, 2: 1, 4: 1, 5: 2, 11: 2, 12: 2, 14: 2, 20: 2, 21: 0, 22: 1, 25: 2, 111: 2, 112: 2} {
		if got := PluralForm(n); got != want {
			t.Errorf("PluralForm(%d) = %d, want %d", n, got, want)
		}
	}
}

// TestTrafficPartsHeader: the breakdown crosses the chain as it left the
// panel; a long remark is cut, a breakdown past the bounds is not sent, and
// a value that does not parse is no breakdown.
func TestTrafficPartsHeader(t *testing.T) {
	parts := []TrafficPart{{Protocol: "vless", Remark: "Нидерланды", Used: 3 << 30, Limit: 50 << 30}, {Protocol: "amneziawg", Used: 1}}
	value := EncodeTrafficParts(parts)
	for _, r := range value {
		if r > 0x7e || r < 0x20 {
			t.Fatalf("the header carries %q: apps take ASCII", r)
		}
	}
	if got := ParseTrafficParts(value); !reflect.DeepEqual(got, parts) {
		t.Errorf("round trip: %+v, want %+v", got, parts)
	}

	long := []TrafficPart{{Protocol: "vless", Remark: strings.Repeat("я", 100)}}
	if got := ParseTrafficParts(EncodeTrafficParts(long)); len(got) != 1 || got[0].Remark != strings.Repeat("я", maxRemarkRunes) {
		t.Errorf("long remark: %+v", got)
	}

	many := make([]TrafficPart, maxTrafficParts+1)
	for i := range many {
		many[i] = TrafficPart{Protocol: "vless"}
	}
	if v := EncodeTrafficParts(many); v != "" {
		t.Errorf("%d parts sent: %d bytes", len(many), len(v))
	}
	wide := make([]TrafficPart, maxTrafficParts)
	for i := range wide {
		wide[i] = TrafficPart{Protocol: "vless", Remark: strings.Repeat("r", maxRemarkRunes), Used: 1 << 50, Limit: 1 << 50}
	}
	if v := EncodeTrafficParts(wide); v != "" {
		t.Errorf("a breakdown of %d bytes was sent", len(v))
	}
	if v := EncodeTrafficParts(wide[:8]); v == "" || len(v) > MaxTrafficPartsHeader {
		t.Errorf("eight parts: %d bytes", len(v))
	}

	for _, bad := range []string{"", "not base64!", "e30=" /* {} */, "W10=" /* [] */, "W3sidSI6MX1d" /* [{"u":1}] */, strings.Repeat("A", MaxTrafficPartsHeader+4)} {
		if got := ParseTrafficParts(bad); got != nil {
			t.Errorf("ParseTrafficParts(%.20q) = %+v", bad, got)
		}
	}
}

// TestUsageViewWithParts: with the breakdown, the used traffic and the quota
// are the parts', and the page words the total limit and lists each.
func TestUsageViewWithParts(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	u := Usage{Known: true, Up: 1, Total: 50 * gb, Parts: []TrafficPart{
		{Protocol: "vless", Used: 2 * gb, Limit: 50 * gb}, {Protocol: "amneziawg", Used: gb, Limit: 20 * gb}}}
	v := u.view(now)
	if v.Used != "3.00GB" || v.Total != "70.00GB" || v.Remained != "67.00GB" || !v.Active {
		t.Errorf("view = %+v", v)
	}
	u.quota(v, translator{})
	if v.Quota != "70.00GB (VLESS 50.00GB + AmneziaWG 20.00GB)" {
		t.Errorf("quota %q", v.Quota)
	}
	want := []partView{{"VLESS", "2.00GB", "50.00GB"}, {"AmneziaWG", "1.00GB", "20.00GB"}}
	if !reflect.DeepEqual(v.Parts, want) {
		t.Errorf("parts %+v", v.Parts)
	}

	u.Parts[1].Limit = 0
	v = u.view(now)
	if v.Total != "∞" || v.Remained != "" {
		t.Errorf("unlimited part: %+v", v)
	}
}

// TestThePageShowsTheTotalLimit: the page words the total limit and lists
// the parts in the visitor's language; without parts it is as before.
func TestThePageShowsTheTotalLimit(t *testing.T) {
	p := testPage()
	if page := render(t, p, nil); strings.Contains(page, `data-testid="sub-quota"`) {
		t.Error("a page without the breakdown shows the total limit")
	}

	p.Usage.Parts = []TrafficPart{{Protocol: "vless", Remark: "xhttp", Used: gb, Limit: 50 * gb},
		{Protocol: "vless", Remark: "tcp", Limit: 50 * gb}}
	for _, c := range []struct {
		lang string
		want []string
	}{
		{"en-US", []string{"Total limit: 100.00GB (50.00GB × 2 protocols)", "<span>VLESS (xhttp)</span><span>1.00GB / 50.00GB</span>",
			"<span>VLESS (tcp)</span><span>0.00B / 50.00GB</span>"}},
		{"ru-RU", []string{"Общий лимит: 100.00GB (50.00GB × 2 протокола)"}},
	} {
		page := html.UnescapeString(render(t, p, map[string]string{"Accept-Language": c.lang}))
		quota := blocks(page, "sub-quota")
		if len(quota) != 1 {
			t.Fatalf("%s: %d total limit blocks", c.lang, len(quota))
		}
		for _, want := range c.want {
			if !strings.Contains(page, want) {
				t.Errorf("%s: the page lacks %q", c.lang, want)
			}
		}
	}

	p.Usage.Parts[1].Limit = 0
	if page := html.UnescapeString(render(t, p, map[string]string{"Accept-Language": "ru-RU"})); !strings.Contains(page, "Общий лимит: без ограничения") ||
		!strings.Contains(page, "0.00B / ∞") {
		t.Error("an unlimited part does not make the total limit unlimited")
	}
}
