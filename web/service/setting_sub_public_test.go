package service

import (
	"strings"
	"testing"
)

// TestSubPublicURLSetting: the public subscription address (#224) is empty
// on a fresh panel, takes a scheme and a host (with a port) through the
// settings form, stored as an origin — no trailing slash, no default port,
// the host in lower case — and refuses anything else without touching the
// stored value.
func TestSubPublicURLSetting(t *testing.T) {
	s := newMonitoringSettingService(t)

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatalf("GetAllSetting: %v", err)
	}
	if all.SubPublicURL != "" {
		t.Errorf("default = %q, want empty", all.SubPublicURL)
	}

	for value, want := range map[string]string{
		"https://sub.example.com":      "https://sub.example.com",
		" https://Sub.Example.com/ ":   "https://sub.example.com",
		"http://sub.example.com:8080":  "http://sub.example.com:8080",
		"https://sub.example.com:443/": "https://sub.example.com",
		"http://sub.example.com:80":    "http://sub.example.com",
		"HTTPS://203.0.113.5:2096":     "https://203.0.113.5:2096",
		"https://[2001:db8::5]:8443":   "https://[2001:db8::5]:8443",
		"":                             "",
	} {
		all.SubPublicURL = value
		if err := s.UpdateAllSetting(all); err != nil {
			t.Fatalf("save %q: %v", value, err)
		}
		got, err := s.GetSubPublicURL()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("save %q: stored %q, want %q", value, got, want)
		}
	}

	all.SubPublicURL = "https://kept.example.com"
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"sub.example.com",                 // no scheme
		"ftp://sub.example.com",           // not a web scheme
		"https://",                        // no host
		"https://sub.example.com/sub/",    // a path: the paths are the panel's own
		"https://sub.example.com?x=1",     // a query
		"https://sub.example.com#top",     // a fragment
		"https://user:pw@sub.example.com", // credentials
		"https://sub.example.com:0",       // no such port
		"https://sub.example.com:70000",   // no such port
		"https://sub.example.com:http",    // not a port
		"https://sub example.com",         // not a host
	} {
		all.SubPublicURL = bad
		err := s.UpdateAllSetting(all)
		if err == nil || !strings.Contains(err.Error(), "public subscription address") {
			t.Errorf("save %q: err = %v, want a public subscription address error", bad, err)
		}
	}
	if got, _ := s.GetSubPublicURL(); got != "https://kept.example.com" {
		t.Errorf("after refused saves the address is %q", got)
	}
}
