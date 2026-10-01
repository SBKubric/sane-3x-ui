package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/subpage"
)

// TestSubPageAppsSetting: the subscription page's app list (#235). A fresh
// panel shows the built-in list in the form and stores nothing; saved back
// unchanged it stays nothing, so the next release's defaults reach it; the
// owner's own list is stored checked and re-indented, and a list the page
// could not show is refused without touching the stored one.
func TestSubPageAppsSetting(t *testing.T) {
	s := newMonitoringSettingService(t)

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("GetAllSetting: %v", err)
	}
	if all.SubPageApps != subpage.AppsJSON(subpage.DefaultApps()) {
		t.Errorf("the form shows %q, want the built-in list", all.SubPageApps)
	}
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatalf("save the form as it came: %v", err)
	}
	if raw, _ := s.getString("subPageApps"); raw != "" {
		t.Errorf("the built-in list saved back is stored as %q, want \"\"", raw)
	}
	if apps, err := s.GetSubPageApps(); err != nil || !reflect.DeepEqual(apps, subpage.DefaultApps()) {
		t.Errorf("GetSubPageApps = %v, %v; want the built-in list", apps, err)
	}

	own := []subpage.App{{Name: "Own", Platform: "Android", URL: "https://example.com/own", Protocols: []string{"VLESS + XHTTP"}}}
	all.SubPageApps = `[{"name":"Own","platform":"Android","url":"https://example.com/own","protocols":["vless + xhttp"]}]`
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatalf("save the owner's list: %v", err)
	}
	if apps, err := s.GetSubPageApps(); err != nil || !reflect.DeepEqual(apps, own) {
		t.Errorf("GetSubPageApps = %+v, %v; want %+v", apps, err, own)
	}
	if all, _ := s.GetAllSetting(); all.SubPageApps != subpage.AppsJSON(own) {
		t.Errorf("the form shows %q", all.SubPageApps)
	}

	for _, bad := range []string{`[{"name":"x"}]`, `not json`, `[{"name":"x","url":"https://example.com","protocols":["VLESS+XHTTP"]}]`} {
		all.SubPageApps = bad
		if err := s.UpdateAllSetting(all); err == nil || !strings.Contains(err.Error(), "subscription page apps") {
			t.Errorf("save %q: err = %v, want a subscription page apps error", bad, err)
		}
	}
	if apps, _ := s.GetSubPageApps(); !reflect.DeepEqual(apps, own) {
		t.Errorf("after refused saves the list is %+v", apps)
	}

	// An explicit empty list is a choice too: no apps.
	all.SubPageApps = "[]"
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if apps, err := s.GetSubPageApps(); err != nil || len(apps) != 0 {
		t.Errorf("GetSubPageApps after [] = %v, %v", apps, err)
	}

	// A stored value the form never wrote reads as the built-in list.
	if err := s.setString("subPageApps", `[{"name":"x"}]`); err != nil {
		t.Fatal(err)
	}
	if apps, err := s.GetSubPageApps(); err == nil || !reflect.DeepEqual(apps, subpage.DefaultApps()) {
		t.Errorf("GetSubPageApps of a broken value = %v, %v; want the built-in list and the error", apps, err)
	}
}
