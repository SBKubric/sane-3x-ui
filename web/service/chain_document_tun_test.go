package service

import (
	"testing"
)

// TestTheDocumentCarriesTheTunnelPath: the chain document names the tunnel
// subscription's path so every hop can serve /tun (docs/spec/tunnel-subscription.md
// §7), inner hops passing the panel's own on; with the route off it names none.
func TestTheDocumentCarriesTheTunnelPath(t *testing.T) {
	documents, _ := exampleChain(t)

	for _, name := range []string{"inner-1", "inner-2", "edge-a"} {
		document, err := documents.Build(name)
		if err != nil {
			t.Fatalf("Build(%s): %v", name, err)
		}
		if document.NextHop.TunPath != "/tun/" {
			t.Errorf("%s: tunPath = %q, want /tun/", name, document.NextHop.TunPath)
		}
	}

	if err := documents.settingService.setString("subTunEnable", "false"); err != nil {
		t.Fatal(err)
	}
	document, err := documents.Build("inner-1")
	if err != nil {
		t.Fatal(err)
	}
	if document.NextHop.TunPath != "" {
		t.Errorf("tunPath = %q with the tunnel subscription off, want none", document.NextHop.TunPath)
	}
}
