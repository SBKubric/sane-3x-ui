package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// Tests for `x-ui setting -frontTrustedAddrs` (#228): what the orchestrator
// sets from the subscription showcase's address.

func TestRunFrontTrustedSetting(t *testing.T) {
	newMonTestDB(t)
	s := service.SettingService{}
	var out bytes.Buffer
	if err := runFrontTrustedSetting(&out, "203.0.113.5, 2001:DB8::/48"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "frontTrustedAddrs: 203.0.113.5,2001:db8::/48\n" {
		t.Errorf("output %q", out.String())
	}
	if got, _ := s.GetFrontTrustedAddrs(); !slices.Equal(got, []string{"203.0.113.5", "2001:db8::/48"}) {
		t.Errorf("stored %v", got)
	}

	// A refusal stores nothing, says why, and fails the command.
	out.Reset()
	err := runFrontTrustedSetting(&out, "showcase.example.com")
	if err == nil || !strings.Contains(err.Error(), "frontTrustedAddrs") || out.Len() != 0 {
		t.Errorf("a name: %v, output %q", err, out.String())
	}
	if got, _ := s.GetFrontTrustedAddrs(); len(got) != 2 {
		t.Errorf("after the refusal: %v", got)
	}

	// "" clears.
	out.Reset()
	if err := runFrontTrustedSetting(&out, ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "frontTrustedAddrs: \n" {
		t.Errorf("output %q", out.String())
	}
	if got, _ := s.GetFrontTrustedAddrs(); got != nil {
		t.Errorf("cleared: %v", got)
	}
}
