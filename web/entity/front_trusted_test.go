package entity

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The front's trusted addresses (#228): what the settings form, the API and
// `x-ui setting -frontTrustedAddrs` may store.

func TestNormalizeFrontTrustedAddrs(t *testing.T) {
	cases := []struct {
		raw, want string
		bad       bool
	}{
		{raw: "", want: ""},
		{raw: " \n ", want: ""},
		{raw: "198.51.100.7", want: "198.51.100.7"},
		// Commas, spaces, semicolons and new lines all separate; the order
		// the owner gave is kept, a repeat is dropped.
		{raw: "203.0.113.5, 198.51.100.7;\n2001:DB8::7  203.0.113.5", want: "203.0.113.5,198.51.100.7,2001:db8::7"},
		// A network is stored masked; a v4-mapped address as plain v4.
		{raw: "198.51.100.77/24", want: "198.51.100.0/24"},
		{raw: "2001:db8:1::5/48", want: "2001:db8:1::/48"},
		{raw: "::ffff:192.0.2.9", want: "192.0.2.9"},
		{raw: "192.0.2.0/16", want: "192.0.0.0/16"},
		{raw: "2001:db8::/32", want: "2001:db8::/32"},
		// Names are not resolved: the address behind one can change under
		// the config.
		{raw: "sub.example.com", bad: true},
		{raw: "198.51.100.7:443", bad: true},
		{raw: "198.51.100.7/33", bad: true},
		{raw: "198.51.100.300", bad: true},
		// Nothing that switches the protection off for everybody.
		{raw: "0.0.0.0/0", bad: true},
		{raw: "::/0", bad: true},
		{raw: "10.0.0.0/8", bad: true},
		{raw: "2001::/16", bad: true},
		{raw: "0.0.0.0", bad: true},
		{raw: "::", bad: true},
		{raw: manyAddrs(FrontTrustedMax + 1), bad: true},
	}
	for _, c := range cases {
		got, err := NormalizeFrontTrustedAddrs(c.raw)
		if c.bad {
			if err == nil || !strings.Contains(err.Error(), "trusted address") {
				t.Errorf("NormalizeFrontTrustedAddrs(%q) = %q, %v; want a refusal", c.raw, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("NormalizeFrontTrustedAddrs(%q) = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}

	if got, err := NormalizeFrontTrustedAddrs(manyAddrs(FrontTrustedMax)); err != nil || strings.Count(got, ",") != FrontTrustedMax-1 {
		t.Errorf("%d addresses: %q, %v", FrontTrustedMax, got, err)
	}
}

func TestParseFrontTrustedAddrs(t *testing.T) {
	got, err := ParseFrontTrustedAddrs("203.0.113.5 , 2001:db8::/48")
	if err != nil || !slices.Equal(got, []string{"203.0.113.5", "2001:db8::/48"}) {
		t.Errorf("ParseFrontTrustedAddrs = %v, %v", got, err)
	}
	if got, err := ParseFrontTrustedAddrs(""); err != nil || got != nil {
		t.Errorf("empty: %v, %v; want nil", got, err)
	}
}

// manyAddrs is n distinct addresses of 198.51.100.0/24 and 203.0.113.0/24.
func manyAddrs(n int) string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, []string{"198.51.100.", "203.0.113."}[i/250]+strconv.Itoa(i%250+1))
	}
	return strings.Join(out, ",")
}
