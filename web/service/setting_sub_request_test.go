package service

import (
	"slices"
	"strings"
	"testing"
)

// TestSubRequestDefaultsSetting (#221): the request defaults are every
// enabled inbound, 50 GB per protocol and 30 days on a fresh panel; the
// settings form stores chosen inbounds as a list of ids, the traffic and the
// expiry, and refuses a malformed list or a negative number without touching
// the stored values.
func TestSubRequestDefaultsSetting(t *testing.T) {
	s := newMonitoringSettingService(t)

	d, err := s.GetSubRequestDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if d.InboundIds != nil || d.TrafficGB != 50 || d.ExpiryDays != 30 {
		t.Fatalf("fresh defaults: %+v, want all enabled, 50 GB, 30 days", d)
	}

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	if all.SubRequestInbounds != "" || all.SubRequestTrafficGB != 50 || all.SubRequestExpiryDays != 30 {
		t.Errorf("the form loads %q %d %d", all.SubRequestInbounds, all.SubRequestTrafficGB, all.SubRequestExpiryDays)
	}
	all.SubRequestInbounds, all.SubRequestTrafficGB, all.SubRequestExpiryDays = " 5, 2 ,2", 0, 7
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	d, _ = s.GetSubRequestDefaults()
	if !slices.Equal(d.InboundIds, []int{5, 2}) || d.TrafficGB != 0 || d.ExpiryDays != 7 {
		t.Errorf("saved: %+v, want [5 2], unlimited, 7 days", d)
	}
	if all, _ := s.GetAllSetting(); all.SubRequestInbounds != "5,2" {
		t.Errorf("stored list %q, want it tidied to 5,2", all.SubRequestInbounds)
	}

	for name, bad := range map[string]func(){
		"a list with a word":    func() { all.SubRequestInbounds = "1,vless" },
		"a zero id":             func() { all.SubRequestInbounds = "0" },
		"negative traffic":      func() { all.SubRequestTrafficGB = -1 },
		"negative expiry":       func() { all.SubRequestExpiryDays = -30 },
		"traffic beyond 999999": func() { all.SubRequestTrafficGB = 1000000 },
	} {
		all, _ = s.GetAllSetting()
		bad()
		if err := s.UpdateAllSetting(all); err == nil || !strings.Contains(err.Error(), "request") {
			t.Errorf("%s: err = %v, want a request defaults error", name, err)
		}
	}
	if d, _ := s.GetSubRequestDefaults(); !slices.Equal(d.InboundIds, []int{5, 2}) || d.ExpiryDays != 7 {
		t.Errorf("after refused saves: %+v", d)
	}

	// An empty list is every enabled inbound again.
	all, _ = s.GetAllSetting()
	all.SubRequestInbounds = " "
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetSubRequestDefaults(); d.InboundIds != nil {
		t.Errorf("an empty list: %+v", d)
	}
}
