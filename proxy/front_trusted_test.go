package proxy

import (
	"slices"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// The front's trusted addresses (#228): the subscription showcase calls an
// edge's HTTP side from one address for all its clients, so the edge must
// neither limit nor ban it.

// TestFrontExemptsTheTrustedAddrs: the document's trusted addresses join the
// chain neighbours in the guard — the limits' empty key and the probe jail's
// ignoreip — on an edge and on an inner alike. An entry the guard could not
// render (a name, past a panel that checks) is left out rather than breaking
// nginx.
func TestFrontExemptsTheTrustedAddrs(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  *chain.Document
		want []string
	}{
		{"edge", edgeFrontDocument(), []string{"198.51.100.0/28", "2001:db8:5::/48", "203.0.113.5", "203.0.113.9"}},
		{"inner", innerFrontDocument(), []string{"192.0.2.1", "198.51.100.0/28", "198.51.100.20", "198.51.100.30", "2001:db8:5::/48", "203.0.113.5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.doc
			doc.FrontTrustedAddrs = []string{"203.0.113.5", "showcase.example.com", "2001:db8:5::/48", "198.51.100.0/28"}
			cfg, _ := buildFrontT(t, doc)
			if got := cfg.Site.Guard.Exempt; !slices.Equal(got, tc.want) {
				t.Errorf("exempt = %v, want %v", got, tc.want)
			}
		})
	}

	// Without the list the exemptions are the neighbours, as before.
	cfg, _ := buildFrontT(t, edgeFrontDocument())
	if got := cfg.Site.Guard.Exempt; !slices.Equal(got, []string{"203.0.113.9"}) {
		t.Errorf("exempt without trusted addresses = %v", got)
	}
}

// TestTheJailsIgnoreTheTrustedAddrs: a new list reaches a running front with
// the next document, and fail2ban's probe jail ignores the showcase from then
// on; cleared, it is a client like any other again.
func TestTheJailsIgnoreTheTrustedAddrs(t *testing.T) {
	rig := newFrontRig(t, chain.FrontOnly443, "")
	doc := edgeFrontDocument()
	if err := rig.apply(t, doc); err != nil {
		t.Fatal(err)
	}
	if !rig.log.has("jails.apply miss=" + nginx.MissLogPath + " login= ignore=203.0.113.9") {
		t.Fatalf("jails before the list: %v", rig.log.events)
	}

	trusted := edgeFrontDocument()
	trusted.Revision = 43
	trusted.FrontTrustedAddrs = []string{"198.51.100.77"}
	rig.log.events = nil
	if err := rig.apply(t, trusted); err != nil {
		t.Fatal(err)
	}
	if !rig.log.has("jails.apply miss=" + nginx.MissLogPath + " login= ignore=198.51.100.77,203.0.113.9") {
		t.Errorf("the showcase is not ignored: %v", rig.log.events)
	}

	cleared := edgeFrontDocument()
	cleared.Revision = 44
	rig.log.events = nil
	if err := rig.apply(t, cleared); err != nil {
		t.Fatal(err)
	}
	if !rig.log.has("jails.apply miss=" + nginx.MissLogPath + " login= ignore=203.0.113.9") {
		t.Errorf("the cleared list is still ignored: %v", rig.log.events)
	}
}

// TestTruncateDocumentKeepsTheFrontTrustedAddrs: an edge behind an inner
// learns the list from the inner's truncation, as it learns the public
// subscription address.
func TestTruncateDocumentKeepsTheFrontTrustedAddrs(t *testing.T) {
	cfg := &Config{Domain: "192.0.2.7", SubPort: 2096}
	doc := innerDocument()
	doc.FrontTrustedAddrs = []string{"203.0.113.5", "2001:db8::/48"}
	for _, hop := range doc.Hops[1:] {
		if out := TruncateDocument(doc, hop, cfg); !slices.Equal(out.FrontTrustedAddrs, doc.FrontTrustedAddrs) {
			t.Errorf("%s: frontTrustedAddrs %v", hop.Name, out.FrontTrustedAddrs)
		}
	}
}

// TestDocumentDiffersOnTheFrontTrustedAddrs: a document whose only change is
// the list is a new one for the hop, even under the same revision.
func TestDocumentDiffersOnTheFrontTrustedAddrs(t *testing.T) {
	a, b := testDocument(42), testDocument(42)
	b.FrontTrustedAddrs = []string{"203.0.113.5"}
	if !documentDiffers(a, b) {
		t.Error("a document with new trusted addresses is taken for the same one")
	}
	a.FrontTrustedAddrs = []string{"203.0.113.5"}
	if documentDiffers(a, b) {
		t.Error("the same trusted addresses differ")
	}
}
