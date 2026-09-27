package nginx

import (
	"strings"
	"testing"
)

// goldenGuard is the protection of an only443 HTTP side (#141): a hop of the
// chain by address, another by network, and mon-server over IPv6.
func goldenGuard() *Guard {
	return &Guard{
		Exempt:  []string{"203.0.113.9", "2001:db8::7", "198.51.100.0/24"},
		MissLog: "/var/log/nginx/3ax-ui-miss.log",
	}
}

// goldenGuardedSite is the panel's HTTP side in only443 (#141): of the panel
// only the login and the API are published, the rest of its base path is the
// stub, and every published path sits behind a per-client limit.
func goldenGuardedSite() *Site {
	s := goldenSite()
	goldenIPCert(s)
	s.Panel = &Proxy{Name: "panel API", Paths: []string{"/uS2J19TzcfZuEAPyNH/panel/api/"}, Target: "127.0.0.1:33757", TLS: true}
	s.Login = &Proxy{Name: "panel login", Paths: []string{"/uS2J19TzcfZuEAPyNH/login"}, Target: "127.0.0.1:33757", TLS: true}
	s.Sub = &Proxy{Name: "subscriptions", Paths: []string{"/sub-abc123/", "/json/", "/chain/v1/"}, Target: "127.0.0.1:2096"}
	s.Mon = &Proxy{Name: "monitoring", Paths: []string{"/uS2J19TzcfZuEAPyNH/mon/v1/"}, Target: "127.0.0.1:33757", TLS: true}
	s.Decoy = []string{"/uS2J19TzcfZuEAPyNH/"}
	s.Guard = goldenGuard()
	return s
}

// TestGeneratedGuardedConfig: the golden of the panel's guarded HTTP side,
// and the properties it has to keep, spelled out.
func TestGeneratedGuardedConfig(t *testing.T) {
	c := goldenConfig()
	c.Mode = ModeOnly443
	c.Site = goldenGuardedSite()
	http := must(t, c.HTTPConf)
	check(t, "http_only443_guard.conf", http)

	for _, want := range []string{
		// An exempt address has an empty key, which neither limit counts.
		"geo $threeax_exempt {\n    default 0;\n    127.0.0.0/8 1;\n    ::1 1;\n    198.51.100.0/24 1;\n    2001:db8::7 1;\n    203.0.113.9 1;\n}\n",
		"map $threeax_exempt $threeax_limit_key {\n    1       \"\";\n    default $binary_remote_addr;\n}\n",
		"limit_req_zone $threeax_limit_key zone=threeax_login:",
		"limit_conn_zone $threeax_limit_key zone=threeax_conn:",
		// Over the limit is the stub, not 429.
		"    limit_req_status 429;\n    limit_conn_status 429;\n    error_page 429 = @threeax_limited;\n",
		"    location @threeax_limited {\n        access_log /var/log/nginx/3ax-ui-miss.log threeax_limit;\n        try_files /index.html =404;\n    }\n",
		// Only the login and the API of the panel; the login exactly.
		"    location = /uS2J19TzcfZuEAPyNH/login {\n        limit_req zone=threeax_login ",
		"    location /uS2J19TzcfZuEAPyNH/panel/api/ {\n        limit_req zone=threeax_api ",
		"    location /sub-abc123/ {\n        limit_req zone=threeax_sub ",
		"    location /uS2J19TzcfZuEAPyNH/mon/v1/ {\n        limit_req zone=threeax_api ",
		// An upstream's refusal under a secret prefix is a miss.
		"        access_log /var/log/nginx/3ax-ui-miss.log threeax_miss if=$threeax_upstream_miss;\n",
		// The rest of the base path answers as the stub does, and counts.
		"    location /uS2J19TzcfZuEAPyNH/ {\n        access_log /var/log/nginx/3ax-ui-miss.log threeax_miss;\n        try_files /index.html =404;\n    }\n",
	} {
		if !strings.Contains(http, want) {
			t.Errorf("the guarded HTTP side lacks %q", want)
		}
	}
	// Both servers — the domain and the address — carry the same guard.
	if n := strings.Count(http, "error_page 429 = @threeax_limited;"); n != 2 {
		t.Errorf("%d servers send a limit to the stub, want 2", n)
	}
	if strings.Contains(http, "location /uS2J19TzcfZuEAPyNH/ {\n        proxy_pass") {
		t.Error("the whole base path of the panel is still published")
	}
}

// TestUnguardedSiteHasNoLimits: shared mode and a site without a guard render
// exactly as before (#141 changes only443 only).
func TestUnguardedSiteHasNoLimits(t *testing.T) {
	c := goldenConfig()
	c.Site = goldenSite()
	http := must(t, c.HTTPConf)
	for _, unwanted := range []string{"limit_req", "geo ", "threeax_miss", "@threeax_limited"} {
		if strings.Contains(http, unwanted) {
			t.Errorf("an unguarded site carries %q", unwanted)
		}
	}
}

// TestValidateGuardedSite: the mistakes a guarded site can carry that nginx
// would refuse, or that would quietly protect nothing.
func TestValidateGuardedSite(t *testing.T) {
	cases := []struct {
		name string
		want string // substring of the expected error, "" for valid
		mut  func(s *Site)
	}{
		{name: "valid"},
		{name: "exempt address with a port", want: "neither an IP nor a network",
			mut: func(s *Site) { s.Guard.Exempt = []string{"203.0.113.9:443"} }},
		{name: "exempt host name", want: "neither an IP nor a network",
			mut: func(s *Site) { s.Guard.Exempt = []string{"hop.example.net"} }},
		{name: "miss log without a path", want: "miss log",
			mut: func(s *Site) { s.Guard.MissLog = "" }},
		{name: "relative miss log", want: "miss log",
			mut: func(s *Site) { s.Guard.MissLog = "miss.log" }},
		// A decoy of the root would take the stub's own location.
		{name: "decoy of the root", want: "decoy prefix",
			mut: func(s *Site) { s.Decoy = []string{"/"} }},
		{name: "decoy that is not a prefix", want: "decoy prefix",
			mut: func(s *Site) { s.Decoy = []string{"/uS2J19TzcfZuEAPyNH"} }},
		{name: "published path without its leading slash", want: "does not start with /",
			mut: func(s *Site) { s.Login.Paths = []string{"uS2J19TzcfZuEAPyNH/login"} }},
		{name: "decoy without a guard is only the stub",
			mut: func(s *Site) { s.Guard = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := goldenConfig()
			c.Mode = ModeOnly443
			c.Site = goldenGuardedSite()
			if tc.mut != nil {
				tc.mut(c.Site)
			}
			err := c.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("expected an error containing %q, got none", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestExemptAddress: the chain stores addresses as the owner typed them or
// as a join arrived; the guard wants the bare IP, and no name.
func TestExemptAddress(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.9":          "203.0.113.9",
		"203.0.113.9:2096":     "203.0.113.9",
		"[2001:db8::7]:443":    "2001:db8::7",
		"2001:db8::7":          "2001:db8::7",
		" ::ffff:203.0.113.9 ": "203.0.113.9",
		"hop.example.net":      "",
		"hop.example.net:443":  "",
		"":                     "",
	} {
		got, ok := ExemptAddress(in)
		if got != want || ok != (want != "") {
			t.Errorf("ExemptAddress(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// TestGeneratedGuardedHopConfig: a box's HTTP side in only443 (#141) — the
// subscriptions and the wave under the subscription limit, the join page
// under the login limit, its neighbours exempt.
func TestGeneratedGuardedHopConfig(t *testing.T) {
	c := goldenHopConfig()
	c.Site.Sub.Paths = []string{"/sub/", "/chain/v1/"}
	c.Site.Login = &Proxy{Name: "join page of edge-a", Paths: []string{"/join/"}, Target: "127.0.0.1:8083"}
	c.Site.Guard = &Guard{Exempt: []string{"203.0.113.9"}, MissLog: "/var/log/nginx/3ax-ui-miss.log"}
	http := must(t, c.HTTPConf)
	check(t, "http_hop_guard.conf", http)
	if !strings.Contains(http, "    location /join/ {\n        limit_req zone=threeax_login ") {
		t.Error("the join page is not under the login limit")
	}
}
