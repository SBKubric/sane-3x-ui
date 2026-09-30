package web

import (
	"html/template"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// renderPage executes one top-level page with i18n printing the bare key.
func renderPage(t *testing.T, page string) string {
	t.Helper()
	tpl := template.New("").Funcs(template.FuncMap{"i18n": func(key string, params ...string) string { return key }})
	dirs := map[string]bool{}
	if err := fs.WalkDir(htmlFS, "html", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".html") {
			dirs[path.Dir(p)] = true
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for dir := range dirs {
		var err error
		if tpl, err = tpl.ParseFS(htmlFS, dir+"/*.html"); err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}
	}
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, page, map[string]any{"base_path": "/", "cur_ver": "test", "cur_lang": "en-US", "host": "127.0.0.1", "request_uri": "/panel/", "title": "test"}); err != nil {
		t.Fatalf("%s: %v", page, err)
	}
	return b.String()
}

// TestClientModalSaysTheTgIdIsTheUsers (#186): the client modal's Telegram
// field says the id is set on the user.
func TestClientModalSaysTheTgIdIsTheUsers(t *testing.T) {
	out := renderPage(t, "inbounds.html")
	i := strings.Index(out, `v-model.number="client.tgId"`)
	hint := strings.Index(out, `data-testid="client-tgid-hint">pages.subUsers.telegram.clientHint<`)
	if i < 0 || hint < i {
		t.Errorf("no hint after the tgId field (field at %d, hint at %d)", i, hint)
	}
}

// TestUsersPageShowsTheTelegramConflict (#186): the users page marks a user
// in conflict and has the Telegram modal with assign and unlink.
func TestUsersPageShowsTheTelegramConflict(t *testing.T) {
	out := renderPage(t, "users.html")
	for _, want := range []string{`data-testid="user-tg-conflict"`, "pages.subUsers.telegram.conflict", `data-testid="user-telegram"`,
		`'users-tg-assign-' + c.tgId`, `data-testid="users-tg-unlink"`, "'setTelegram/'", "'unlinkTelegram/'"} {
		if !strings.Contains(out, want) {
			t.Errorf("users.html lacks %s", want)
		}
	}
}
