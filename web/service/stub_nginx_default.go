package service

import (
	"os"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"github.com/coinman-dev/3ax-ui/v2/logger"
	"github.com/coinman-dev/3ax-ui/v2/nginx"
)

// The fork's default cover page (#153, #160): nginx's own welcome page, the
// bytes the nginx package installed on this very machine. A prober who opens
// the server by address meets what any freshly installed nginx on that
// distribution shows — nothing that says 3AX-UI. The pages that came with
// upstream stay in the gallery, and every one of them served unchanged is
// flagged: they ship with each copy of the panel, which makes them the same
// bytes on every server that runs it.

// NginxDefaultKey is the gallery key of nginx's welcome page. Its markup is
// not fixed at build time: it is read off the machine each time the templates
// are listed, so a page installed or synced is always this box's own.
const NginxDefaultKey = "nginx-default"

// constructionKey is the page upstream installs by itself — the one a panel
// upgraded from an earlier build is most likely still serving.
const constructionKey = "construction"

// NginxWelcomePages are where the nginx package leaves its welcome page, in
// the order they are tried. A variable so the tests can point it elsewhere.
var NginxWelcomePages = []string{
	// Debian, Ubuntu: nginx-common's postinst copies the page here, and it is
	// the one the stock "default" site serves.
	"/var/www/html/index.nginx-debian.html",
	// RHEL, Fedora, Arch, the nginx.org packages — and Debian's original.
	"/usr/share/nginx/html/index.html",
	// Alpine.
	"/var/lib/nginx/html/index.html",
}

// nginxDefaultTemplate is nginx's welcome page as this machine has it, or
// the embedded copy of the stock page when no package left one behind (nginx
// built from source, a page somebody deleted).
func nginxDefaultTemplate() (StubTemplate, bool) {
	html := ""
	for _, path := range NginxWelcomePages {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxStubSize {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(body)) == "" {
			continue
		}
		html = string(body)
		break
	}
	if html == "" {
		body, err := stubTemplates.ReadFile("stubs/nginx-default.html")
		if err != nil {
			logger.Warning("stub: missing the built-in nginx welcome page:", err)
			return StubTemplate{}, false
		}
		html = string(body)
	}
	return StubTemplate{Key: NginxDefaultKey, Name: "nginx default page", Html: html, Size: len(html)}, true
}

// isNginxDefault reports whether html is nginx's welcome page as served here.
func isNginxDefault(html string) bool {
	tpl, ok := nginxDefaultTemplate()
	return ok && strings.TrimSpace(html) == strings.TrimSpace(tpl.Html)
}

// UpstreamTemplate returns the page that came with upstream which html still
// is, unchanged: «Site under construction», «Snake», «Tetris». Served as is,
// any of them is a fingerprint of the panel, picked on purpose or not. nginx's
// welcome page is never one of them.
func (s *StubService) UpstreamTemplate(html string) (StubTemplate, bool) {
	for _, tpl := range s.Templates() {
		if tpl.Key == NginxDefaultKey {
			continue
		}
		if strings.TrimSpace(html) == strings.TrimSpace(tpl.Html) {
			return tpl, true
		}
	}
	return StubTemplate{}, false
}

// nginxDefaultSeeder marks the upgrade below as done, in the table upstream
// keeps for its own one-shot migrations.
const nginxDefaultSeeder = "StubNginxDefaultCover"

// adoptNginxDefault switches a server still serving the untouched
// «Site under construction» — the page the panel put there itself, which
// nobody chose — to nginx's welcome page. Once per database: an operator who
// picks that page again afterwards has made a choice, and the panel does not
// undo it. «Snake», «Tetris», edited pages and the operator's own are left
// alone (#153 Q6).
//
// The row is rewritten in place, as migrateLegacyStockSites does: the gallery
// keeps a tile for «Site under construction» whether or not a row holds it.
func (s *StubService) adoptNginxDefault() {
	db := database.GetDB()
	if db == nil {
		return
	}
	var done int64
	if err := db.Model(&model.HistoryOfSeeders{}).Where("seeder_name = ?", nginxDefaultSeeder).Count(&done).Error; err != nil || done > 0 {
		return
	}

	if active := s.ActiveSite(); active != nil {
		if tpl, ok := s.UpstreamTemplate(active.Html); ok && tpl.Key == constructionKey {
			page, ok := nginxDefaultTemplate()
			if !ok {
				return
			}
			err := db.Model(&model.StubSite{}).Where("id = ?", active.Id).Updates(map[string]any{
				"name":       page.Name,
				"html":       page.Html,
				"size":       page.Size,
				"updated_at": time.Now().Unix(),
			}).Error
			if err != nil {
				// Not marked done: the next pass tries again.
				logger.Warning("stub: could not switch the cover page to nginx's welcome page:", err)
				return
			}
			if err := nginx.WriteStub(page.Html); err != nil {
				logger.Warning("stub: switched to nginx's welcome page but could not write it:", err)
			}
			logger.Infof("stub: the untouched «%s» cover page is now nginx's welcome page", tpl.Name)
		}
	}
	if err := db.Create(&model.HistoryOfSeeders{SeederName: nginxDefaultSeeder}).Error; err != nil {
		logger.Warning("stub: could not record the switch to nginx's welcome page:", err)
	}
}
