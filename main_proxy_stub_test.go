package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/web/service"
)

// A box has no panel database and no gallery: its decoy is whatever
// proxy.json names, and otherwise the welcome page its own nginx package left
// behind (#160) — the same page a prober would meet on any stock nginx.

func withWelcomePage(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.nginx-debian.html")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := service.NginxWelcomePages
	service.NginxWelcomePages = []string{path}
	t.Cleanup(func() { service.NginxWelcomePages = prev })
}

func TestProxyDecoyWithoutFrontStubIsThePackagePage(t *testing.T) {
	page := "<!DOCTYPE html>\n<title>Welcome to nginx!</title><p>this box\n"
	withWelcomePage(t, page)

	html, warning := proxyDecoy("")
	if html != page {
		t.Errorf("the box serves %q, want its nginx package's page", html)
	}
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}
}

// An owner may still point front.stub at one of the panel's built-in pages.
// It is served as asked — front.stub wins (#153 Q3) — but the log says that
// every box running this panel can serve the same bytes.
func TestProxyDecoyWarnsAboutAnUpstreamPageInFrontStub(t *testing.T) {
	withWelcomePage(t, "<p>nginx")
	for _, tpl := range (&service.StubService{}).Templates() {
		t.Run(tpl.Key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stub.html")
			if err := os.WriteFile(path, []byte(tpl.Html), 0o644); err != nil {
				t.Fatal(err)
			}
			_, warning := proxyDecoy(path)
			if tpl.Key == service.NginxDefaultKey {
				if warning != "" {
					t.Errorf("the nginx page drew a warning: %q", warning)
				}
				return
			}
			if !strings.Contains(warning, tpl.Name) || !strings.Contains(warning, path) {
				t.Errorf("no warning naming %q and %s: %q", tpl.Name, path, warning)
			}
		})
	}

	own := filepath.Join(t.TempDir(), "own.html")
	if err := os.WriteFile(own, []byte("<!doctype html><title>shop</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, warning := proxyDecoy(own); warning != "" {
		t.Errorf("an own page drew a warning: %q", warning)
	}
}
