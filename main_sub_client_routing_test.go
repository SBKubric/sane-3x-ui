package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// Tests for `x-ui setting -subClientRouting` (#263): the routing Happ and
// v2RayTun get, as the orchestrator sets it on every run.

func TestRunSubClientRoutingSetting(t *testing.T) {
	newMonTestDB(t)
	s := service.SettingService{}
	var out bytes.Buffer
	value := `{"name":"RU","domainStrategy":"IPIfNonMatch","directSites":["geosite:category-ru"],"proxySites":["domain:acomics.ru"]}`
	if err := runSubClientRoutingSetting(&out, value); err != nil {
		t.Fatal(err)
	}
	line := out.String()
	if !strings.HasPrefix(line, `subClientRouting: {"name":"RU","domainStrategy":"IPIfNonMatch","directSites":["geosite:category-ru"]`) ||
		strings.Count(line, "\n") != 1 {
		t.Errorf("output %q", line)
	}
	r, first, err := s.GetSubClientRouting()
	if err != nil || r == nil || r.ProxySites[0] != "domain:acomics.ru" || first == 0 {
		t.Fatalf("stored %+v, %d, %v", r, first, err)
	}

	// The same routing again: Happ's LastUpdated stays.
	out.Reset()
	if err := runSubClientRoutingSetting(&out, value); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.GetSubClientRouting(); again != first {
		t.Errorf("LastUpdated moved: %d -> %d", first, again)
	}

	// A refusal stores nothing, says why, and fails the command.
	out.Reset()
	err = runSubClientRoutingSetting(&out, `{"name":"RU","domainStrategy":"Always"}`)
	if err == nil || !strings.Contains(err.Error(), "subClientRouting") || out.Len() != 0 {
		t.Errorf("refusal: %v, output %q", err, out.String())
	}
	if r, _, _ := s.GetSubClientRouting(); r == nil || r.Name != "RU" {
		t.Errorf("after the refusal: %+v", r)
	}

	// "" turns it off.
	out.Reset()
	if err := runSubClientRoutingSetting(&out, ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "subClientRouting: \n" {
		t.Errorf("output %q", out.String())
	}
	if r, _, _ := s.GetSubClientRouting(); r != nil {
		t.Errorf("off: %+v", r)
	}
}
