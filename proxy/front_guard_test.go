package proxy

import (
	"slices"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/chain"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// TestFrontIsGuarded (#141): a box's HTTP side limits and logs like the
// panel's, and exempts its neighbours in the chain — the next hop and the
// hops outside it, which fetch subscriptions and the wave for every client
// behind them. The join page is under the login limit.
func TestFrontIsGuarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  *chain.Document
		want []string
	}{
		// The edge knows only its next hop; itself is no neighbour.
		{"edge", edgeFrontDocument(), []string{"203.0.113.9"}},
		// The inner knows its next hop and both edges outside it.
		{"inner", innerFrontDocument(), []string{"192.0.2.1", "198.51.100.20", "198.51.100.30"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := buildFrontT(t, tc.doc)
			site := cfg.Site
			if site.Guard == nil || site.Guard.MissLog != nginx.MissLogPath {
				t.Fatalf("guard = %+v", site.Guard)
			}
			if !slices.Equal(site.Guard.Exempt, tc.want) {
				t.Errorf("exempt = %v, want %v", site.Guard.Exempt, tc.want)
			}
			if site.Login == nil || !slices.Equal(site.Login.Paths, []string{"/join/"}) {
				t.Errorf("join page = %+v, want /join/ under the login limit", site.Login)
			}
			if slices.Contains(site.Sub.Paths, "/join/") {
				t.Error("the join page is under the subscription limit")
			}
		})
	}

	// A neighbour entered by name is not resolved.
	doc := innerFrontDocument()
	doc.NextHop.Host = "real.example.net"
	cfg, _ := buildFrontT(t, doc)
	if slices.Contains(cfg.Site.Guard.Exempt, "real.example.net") || len(cfg.Site.Guard.Exempt) != 2 {
		t.Errorf("exempt = %v, want the two edges and no name", cfg.Site.Guard.Exempt)
	}
}

// TestFrontBringsTheJailsWithIt (#141): fail2ban's probe jail comes up with
// the front, carries the same exemptions, and goes when the front does. A box
// has no login, so no login jail.
func TestFrontBringsTheJailsWithIt(t *testing.T) {
	rig := newFrontRig(t, chain.FrontOnly443, "")
	doc := innerFrontDocument()
	if err := rig.apply(t, doc); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	jails := rig.log.index("jails.apply")
	if jails < 0 || jails < rig.log.index("nginx.apply") {
		t.Fatalf("the jails did not follow nginx up: %v", rig.log.events)
	}
	if got := rig.log.events[jails]; got != "jails.apply miss="+nginx.MissLogPath+" login= ignore=192.0.2.1,198.51.100.20,198.51.100.30" {
		t.Errorf("jails = %q", got)
	}

	// A new edge outside: exempt at the next revision.
	next := innerFrontDocument()
	next.Hops = append(next.Hops, chain.Hop{Name: "edge-c", Role: chain.RoleEdge, Host: "198.51.100.40", State: chain.StateJoined})
	next.Revision = 43
	rig.log.events = nil
	if err := rig.apply(t, next); err != nil {
		t.Fatal(err)
	}
	if !rig.log.has("jails.apply") || !strings.Contains(strings.Join(rig.log.events, "\n"), "198.51.100.40") {
		t.Errorf("the new neighbour is not exempt: %v", rig.log.events)
	}

	off := newFrontRig(t, chain.FrontOff, rig.cfg.StateDir)
	if err := off.apply(t, doc); err != nil {
		t.Fatal(err)
	}
	if !off.log.has("jails.remove") {
		t.Errorf("the jails outlived the front: %v", off.log.events)
	}
}
