package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
)

// TestFrontTrustedAddrsSetting (#228): the front's trusted addresses are
// empty on a fresh panel, are stored normalised through the settings form,
// and a refused list leaves the stored one alone.
func TestFrontTrustedAddrsSetting(t *testing.T) {
	s := newMonitoringSettingService(t)

	all, err := s.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	if all.FrontTrustedAddrs != "" {
		t.Errorf("default = %q, want empty", all.FrontTrustedAddrs)
	}
	if got, _ := s.GetFrontTrustedAddrs(); got != nil {
		t.Errorf("GetFrontTrustedAddrs on a fresh panel = %v", got)
	}

	all.FrontTrustedAddrs = " 203.0.113.5\n2001:DB8::/48, 203.0.113.5 "
	if err := s.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetFrontTrustedAddrs(); !slices.Equal(got, []string{"203.0.113.5", "2001:db8::/48"}) {
		t.Errorf("stored %v", got)
	}
	reread, _ := s.GetAllSetting()
	if reread.FrontTrustedAddrs != "203.0.113.5,2001:db8::/48" {
		t.Errorf("the form reads back %q", reread.FrontTrustedAddrs)
	}

	for _, bad := range []string{"sub.example.com", "203.0.113.5:443", "0.0.0.0/0"} {
		all.FrontTrustedAddrs = bad
		if err := s.UpdateAllSetting(all); err == nil || !strings.Contains(err.Error(), "trusted address") {
			t.Errorf("save %q: err = %v, want a refusal", bad, err)
		}
	}
	if got, _ := s.GetFrontTrustedAddrs(); !slices.Equal(got, []string{"203.0.113.5", "2001:db8::/48"}) {
		t.Errorf("after refused saves: %v", got)
	}

	// The setter the CLI uses checks the same way.
	if err := s.SetFrontTrustedAddrs("198.51.100.0/24"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetFrontTrustedAddrs(); !slices.Equal(got, []string{"198.51.100.0/24"}) {
		t.Errorf("after SetFrontTrustedAddrs: %v", got)
	}
	if err := s.SetFrontTrustedAddrs("showcase.example.com"); err == nil {
		t.Error("SetFrontTrustedAddrs took a name")
	}
	if err := s.SetFrontTrustedAddrs(""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetFrontTrustedAddrs(); got != nil {
		t.Errorf("cleared: %v", got)
	}
}

// TestTheDocumentCarriesTheFrontTrustedAddrs (#228): every hop reads the
// trusted addresses, so the edge the showcase calls exempts it. A new list
// is a new revision — the hops poll by ETag — whether the form or the CLI
// stored it; the same list again is not.
func TestTheDocumentCarriesTheFrontTrustedAddrs(t *testing.T) {
	documents, _ := exampleChain(t)
	build := func() map[string]*chain.Document {
		t.Helper()
		all, err := documents.BuildAllWithPanelHost("")
		if err != nil {
			t.Fatal(err)
		}
		return all
	}
	for name, document := range build() {
		if document.FrontTrustedAddrs != nil {
			t.Errorf("%s: frontTrustedAddrs %v without the setting", name, document.FrontTrustedAddrs)
		}
	}

	settings := &documents.settingService
	revision := func() int64 {
		t.Helper()
		r, err := settings.GetChainRevision()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := revision()
	all, err := settings.GetAllSetting()
	if err != nil {
		t.Fatal(err)
	}
	all.FrontTrustedAddrs = "203.0.113.5, 2001:db8::/48"
	if err := settings.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	for name, document := range build() {
		if !slices.Equal(document.FrontTrustedAddrs, []string{"203.0.113.5", "2001:db8::/48"}) {
			t.Errorf("%s: frontTrustedAddrs %v", name, document.FrontTrustedAddrs)
		}
		if document.Revision <= before {
			t.Errorf("%s: revision %d, want more than %d", name, document.Revision, before)
		}
	}

	moved := revision()
	all, _ = settings.GetAllSetting()
	if err := settings.UpdateAllSetting(all); err != nil {
		t.Fatal(err)
	}
	if again := revision(); again != moved {
		t.Errorf("saving the same list moved the revision %d → %d", moved, again)
	}

	// The CLI's setter moves it too, and the same value again does not.
	if err := settings.SetFrontTrustedAddrs("198.51.100.7"); err != nil {
		t.Fatal(err)
	}
	cli := revision()
	if cli <= moved {
		t.Errorf("SetFrontTrustedAddrs left the revision at %d", cli)
	}
	if err := settings.SetFrontTrustedAddrs(" 198.51.100.7 "); err != nil {
		t.Fatal(err)
	}
	if again := revision(); again != cli {
		t.Errorf("setting the same list moved the revision %d → %d", cli, again)
	}
	for name, document := range build() {
		if !slices.Equal(document.FrontTrustedAddrs, []string{"198.51.100.7"}) {
			t.Errorf("%s: frontTrustedAddrs %v after the CLI", name, document.FrontTrustedAddrs)
		}
	}
}
