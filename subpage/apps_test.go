package subpage

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseApps(t *testing.T) {
	apps, err := ParseApps(`[
	  {"name": "Some App", "platform": "Android", "url": "https://example.com/app", "protocols": ["VLESS + XHTTP", " awg 3 ", ""]},
	  {"name": "Other", "platform": "iOS", "url": "http://example.net/other"}
	]`)
	if err != nil {
		t.Fatalf("ParseApps: %v", err)
	}
	want := []App{
		{Name: "Some App", Platform: "Android", URL: "https://example.com/app", Protocols: []string{"VLESS + XHTTP", "AWG 3"}},
		{Name: "Other", Platform: "iOS", URL: "http://example.net/other", Protocols: []string{}},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Fatalf("ParseApps = %#v, want %#v", apps, want)
	}
}

func TestParseAppsRefusesWhatThePageCannotShow(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":           `{"name":`,
		"not a list":         `{"name": "x", "url": "https://example.com"}`,
		"no name":            `[{"url": "https://example.com"}]`,
		"no url":             `[{"name": "x"}]`,
		"javascript url":     `[{"name": "x", "url": "javascript:alert(1)"}]`,
		"relative url":       `[{"name": "x", "url": "/app"}]`,
		"url without a host": `[{"name": "x", "url": "https://"}]`,
		"unknown field":      `[{"name": "x", "url": "https://example.com", "protocol": ["AWG 3"]}]`,
		// A typo would be a label no configuration ever carries.
		"unknown label": `[{"name": "x", "url": "https://example.com", "protocols": ["VLESS+XHTTP"]}]`,
	} {
		if _, err := ParseApps(raw); err == nil {
			t.Errorf("%s: ParseApps(%s) accepted it", name, raw)
		}
	}
}

func TestParseAppsEmptyIsTheDefaultList(t *testing.T) {
	for _, raw := range []string{"", "  \n"} {
		apps, err := ParseApps(raw)
		if err != nil || !reflect.DeepEqual(apps, DefaultApps()) {
			t.Errorf("ParseApps(%q) = %v, %v; want the default list", raw, apps, err)
		}
	}
	// An explicit empty list is the owner's choice: no apps.
	if apps, err := ParseApps("[]"); err != nil || len(apps) != 0 {
		t.Errorf("ParseApps([]) = %v, %v", apps, err)
	}
}

func TestDefaultAppsAreShowable(t *testing.T) {
	apps := DefaultApps()
	if len(apps) == 0 {
		t.Fatal("no default apps")
	}
	// The default list goes through the same check as the owner's.
	again, err := ParseApps(AppsJSON(apps))
	if err != nil {
		t.Fatalf("the default list does not parse: %v", err)
	}
	if !reflect.DeepEqual(again, apps) {
		t.Fatalf("round trip changed the list: %#v", again)
	}
	// v2RayTun leads, as the owner chose (#217); V2rayNG is not recommended.
	if !strings.HasPrefix(apps[0].Name, "v2RayTun") {
		t.Errorf("first default app = %q, want v2RayTun", apps[0].Name)
	}
	// The owner checked DefaultVPN with both of our configurations.
	for _, app := range apps {
		if app.Name == "DefaultVPN" && (!slices.Contains(app.Protocols, "VLESS + XHTTP") || !slices.Contains(app.Protocols, LabelAWG3)) {
			t.Errorf("default DefaultVPN labels = %v, want VLESS + XHTTP and AWG 3", app.Protocols)
		}
	}
	// The owner checked v2RayTun with our VLESS + XHTTP links on Android and iOS.
	for _, app := range apps[:2] {
		if app.Name != "v2RayTun" || !slices.Contains(app.Protocols, "VLESS + XHTTP") {
			t.Errorf("default %s (%s) labels = %v, want VLESS + XHTTP", app.Name, app.Platform, app.Protocols)
		}
	}
	for _, app := range apps {
		if strings.Contains(strings.ToLower(app.Name+app.URL), "v2rayng") {
			t.Errorf("the default list recommends %+v", app)
		}
		for _, label := range app.Protocols {
			if !KnownLabel(label) {
				t.Errorf("%s: label %q is not one the page gives a configuration", app.Name, label)
			}
		}
	}
	// A GitHub app's card downloads one file: the latest release's, or a
	// pinned release's where the file name carries the version.
	for _, app := range apps {
		if !strings.HasPrefix(app.URL, "https://github.com/") {
			continue
		}
		if !strings.Contains(app.URL, "/releases/latest/download/") && !strings.Contains(app.URL, "/releases/download/") {
			t.Errorf("default %s (%s) links to %s, want a release file", app.Name, app.Platform, app.URL)
		}
		if strings.Contains(app.Platform, "/") {
			t.Errorf("default %s card %q names several platforms for one file", app.Name, app.Platform)
		}
	}
	// DefaultApps is a copy: a caller cannot change the next page's list.
	apps[0].Protocols = append(apps[0].Protocols, "changed")
	apps[0].Name = "changed"
	if fresh := DefaultApps(); fresh[0].Name == "changed" || len(fresh[0].Protocols) == len(apps[0].Protocols) {
		t.Fatal("DefaultApps shares its backing data")
	}
}

func TestNormalizeAppsSetting(t *testing.T) {
	// The default list, in any spacing, is stored as "": a panel that never
	// changed it follows the defaults of the next release.
	for _, raw := range []string{"", AppsJSON(DefaultApps()), strings.ReplaceAll(AppsJSON(DefaultApps()), "\n", "")} {
		got, err := NormalizeAppsSetting(raw)
		if err != nil || got != "" {
			t.Errorf("NormalizeAppsSetting(default) = %q, %v; want \"\"", got, err)
		}
	}
	got, err := NormalizeAppsSetting(`[{"name":"x","url":"https://example.com","platform":"Android","protocols":["AWG 3"]}]`)
	if err != nil {
		t.Fatalf("NormalizeAppsSetting: %v", err)
	}
	if got != AppsJSON([]App{{Name: "x", Platform: "Android", URL: "https://example.com", Protocols: []string{"AWG 3"}}}) {
		t.Errorf("NormalizeAppsSetting = %s", got)
	}
	if _, err := NormalizeAppsSetting(`[{"name":"x"}]`); err == nil {
		t.Error("NormalizeAppsSetting accepted an app without a url")
	}
}
