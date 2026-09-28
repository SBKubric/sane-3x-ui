package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// withPackagePage points the lookup at a welcome page of the test's own, the
// way the nginx package would have left one on the box.
func withPackagePage(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.nginx-debian.html")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := NginxWelcomePages
	NginxWelcomePages = []string{path}
	t.Cleanup(func() { NginxWelcomePages = prev })
	return path
}

func templateByKey(t *testing.T, s *StubService, key string) StubTemplate {
	t.Helper()
	for _, tpl := range s.Templates() {
		if tpl.Key == key {
			return tpl
		}
	}
	t.Fatalf("there is no built-in page %q", key)
	return StubTemplate{}
}

// TestNginxDefaultIsTheBoxsOwnPackagePage: a prober who opens the box by
// address should meet what any freshly installed nginx on that distribution
// shows — the bytes the package put there, not a copy that is almost the same
// (#153 Q5).
func TestNginxDefaultIsTheBoxsOwnPackagePage(t *testing.T) {
	s := newStubTestService(t)
	page := "<!DOCTYPE html>\n<title>Welcome to nginx!</title><p>this box's own\n"
	withPackagePage(t, page)

	tpl, ok := s.DefaultTemplate()
	if !ok {
		t.Fatal("there is no default page")
	}
	if tpl.Key != NginxDefaultKey {
		t.Errorf("the default page is %q, want %q", tpl.Key, NginxDefaultKey)
	}
	if tpl.Html != page || tpl.Size != len(page) {
		t.Errorf("the default page is not the package's file byte for byte: %q", tpl.Html)
	}

	if err := s.SyncToDisk(); err != nil {
		t.Fatalf("SyncToDisk: %v", err)
	}
	if got := nginx.StubOnDisk(); got != page {
		t.Errorf("a fresh install serves %q, want the package's page", got)
	}
}

// TestNginxDefaultFallsBackToTheEmbeddedCopy: nginx built from source, or a
// package that left no page behind, still gets nginx's stock welcome page
// rather than nothing.
func TestNginxDefaultFallsBackToTheEmbeddedCopy(t *testing.T) {
	s := newStubTestService(t)
	empty := filepath.Join(t.TempDir(), "empty.html")
	if err := os.WriteFile(empty, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := NginxWelcomePages
	NginxWelcomePages = []string{filepath.Join(t.TempDir(), "missing.html"), empty}
	t.Cleanup(func() { NginxWelcomePages = prev })

	tpl := templateByKey(t, s, NginxDefaultKey)
	if !strings.Contains(tpl.Html, "<title>Welcome to nginx!</title>") {
		t.Errorf("the fallback is not nginx's welcome page: %q", tpl.Html)
	}
	if tpl.Size != len(tpl.Html) {
		t.Errorf("size = %d, want %d", tpl.Size, len(tpl.Html))
	}
}

// TestNginxDefaultTakesTheFirstPageFound: Debian keeps its copy under
// /var/www/html and the original under /usr/share/nginx/html; the one nginx
// actually serves out of the box is the first.
func TestNginxDefaultTakesTheFirstPageFound(t *testing.T) {
	s := newStubTestService(t)
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.html"), filepath.Join(dir, "b.html")
	_ = os.WriteFile(first, []byte("<p>first"), 0o644)
	_ = os.WriteFile(second, []byte("<p>second"), 0o644)
	prev := NginxWelcomePages
	NginxWelcomePages = []string{filepath.Join(dir, "missing.html"), first, second}
	t.Cleanup(func() { NginxWelcomePages = prev })

	if got := templateByKey(t, s, NginxDefaultKey).Html; got != "<p>first" {
		t.Errorf("got %q, want the first page that exists", got)
	}
}

// seedActive puts a page in the database the way an earlier build left it:
// active, and never looked at since.
func seedActive(t *testing.T, name, html string) *model.StubSite {
	t.Helper()
	site := &model.StubSite{Name: name, Html: html, Size: len(html), Active: true, CreatedAt: 1, UpdatedAt: 1}
	if err := database.GetDB().Create(site).Error; err != nil {
		t.Fatal(err)
	}
	return site
}

// TestUpgradeSwitchesTheUntouchedConstructionPage: «Coming soon» was the page
// nobody chose — the panel put it there — so the upgrade moves it to the
// nginx page by itself (#153 Q6). Anything somebody did choose stays.
func TestUpgradeSwitchesTheUntouchedConstructionPage(t *testing.T) {
	page := "<!DOCTYPE html>\n<title>Welcome to nginx!</title>\n"

	t.Run("construction", func(t *testing.T) {
		s := newStubTestService(t)
		withPackagePage(t, page)
		site := seedActive(t, "Site under construction", templateByKey(t, s, "construction").Html)

		if err := s.SyncToDisk(); err != nil {
			t.Fatal(err)
		}
		active := s.ActiveSite()
		if active == nil || active.Html != page {
			t.Fatalf("the untouched construction page is still active: %+v", active)
		}
		if active.Id != site.Id {
			t.Error("the page was switched by adding a row instead of in place")
		}
		if got := nginx.StubOnDisk(); got != page {
			t.Errorf("nginx still serves %q", got)
		}

		// Once only: an operator who picks «Coming soon» again after the
		// upgrade has made a choice, and the panel does not undo it.
		if err := s.ActivateTemplate("construction"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSites(); err != nil {
			t.Fatal(err)
		}
		if err := s.SyncToDisk(); err != nil {
			t.Fatal(err)
		}
		if active := s.ActiveSite(); active == nil || active.Html != templateByKey(t, s, "construction").Html {
			t.Error("the migration ran again and overrode a page picked after the upgrade")
		}
	})

	for _, tc := range []struct{ name, html string }{
		{"snake", ""},
		{"tetris", ""},
		{"edited construction", "\n<!-- ours -->"},
		{"own page", "<!doctype html><title>shop</title>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStubTestService(t)
			withPackagePage(t, page)
			var html string
			switch tc.name {
			case "snake", "tetris":
				html = templateByKey(t, s, tc.name).Html
			case "edited construction":
				html = templateByKey(t, s, "construction").Html + tc.html
			default:
				html = tc.html
			}
			seedActive(t, tc.name, html)

			if err := s.SyncToDisk(); err != nil {
				t.Fatal(err)
			}
			if active := s.ActiveSite(); active == nil || active.Html != html {
				t.Errorf("a page somebody chose was replaced: %+v", active)
			}
			if got := nginx.StubOnDisk(); got != html {
				t.Error("nginx no longer serves the page somebody chose")
			}
		})
	}
}

// TestEveryUpstreamPageIsFlagged: «Coming soon», «Snake» and «Tetris» ship
// with every copy of the panel, so each of them served unchanged is the same
// fingerprint, picked on purpose or not (#153 Q6). The nginx page is what
// every stock nginx serves, and is not.
func TestEveryUpstreamPageIsFlagged(t *testing.T) {
	s := newStubTestService(t)
	for _, tpl := range s.Templates() {
		t.Run(tpl.Key, func(t *testing.T) {
			warnings, err := s.SaveSite(&model.StubSite{Name: tpl.Name, Html: tpl.Html})
			if err != nil {
				t.Fatal(err)
			}
			flagged := false
			for _, w := range warnings {
				if w.Code == "stockCoverPage" && len(w.Params) == 1 && w.Params[0] == tpl.Name {
					flagged = true
				}
			}
			if tpl.Key == NginxDefaultKey {
				if len(warnings) != 0 {
					t.Errorf("the nginx page drew warnings: %+v", warnings)
				}
				return
			}
			if !flagged {
				t.Errorf("an unchanged upstream page was not flagged by name: %+v", warnings)
			}
		})
	}
}

// TestUpgradeNeverOverridesAChoiceMadeBeforeIt: the switch runs before the
// first thing an operator does to the pages, so picking «Coming soon» as the
// very first action on a fresh panel is a choice like any later one.
func TestUpgradeNeverOverridesAChoiceMadeBeforeIt(t *testing.T) {
	s := newStubTestService(t)
	construction := templateByKey(t, s, "construction")
	site := &model.StubSite{Name: construction.Name, Html: construction.Html}
	if _, err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSites(); err != nil {
		t.Fatal(err)
	}
	if active := s.ActiveSite(); active == nil || active.Html != construction.Html {
		t.Errorf("the page saved as the first action was switched: %+v", active)
	}
}
