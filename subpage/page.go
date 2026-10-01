// Package subpage is the subscription page (#235): the one template the
// panel's sub server and every hop of the chain render for a browser that
// opens a subscription link, with the protocol labels and the client app
// list it shows.
//
// Like package chain it is free of panel dependencies — no web/, no
// database/ — so a hop renders exactly the page the panel does. Each side
// fills a Page from what it has: the panel from its database and settings, a
// hop from its next hop's answers.
package subpage

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed page.html
var pageHTML string

var pageTemplate = template.Must(template.New("subpage").Parse(pageHTML))

// Page is what one subscription page shows.
type Page struct {
	// Title is the owner's subscription title; "" for the default.
	Title string
	// SubURL is the subscription link itself, "" on a page of tunnels alone;
	// SubURLs are its other formats (JSON, Clash) when those are on.
	SubURL  string
	SubURLs []NamedURL
	Usage   Usage
	// Links are the subscription's xray configurations.
	Links []Link
	// Tunnels are its AmneziaWG and WireGuard configurations.
	Tunnels []Tunnel
	// Apps are the recommended client apps.
	Apps []App
}

// NamedURL is a link with a caption.
type NamedURL struct {
	Name string
	URL  string
}

// Link is one xray share link with, when there is one, its client JSON
// config (#231).
type Link struct {
	URL  string
	JSON string
}

// Tunnel is one AmneziaWG ("awg") or WireGuard ("wg") client config, as the
// tunnel subscription answers it.
type Tunnel struct {
	Kind     string
	Name     string
	Filename string
	Enable   bool
	Conf     string
}

// view is the template's data.
type view struct {
	tr        translator
	Lang      string
	RTL       bool
	Title     string
	SubURL    string
	SubQR     template.URL
	SubURLs   []NamedURL
	Usage     *usageView
	Links     []linkView
	Tunnels   []tunnelView
	Apps      []appView
	Languages []pageLanguage
}

type appView struct {
	App
	// Icon is the platform's icon: android, apple, windows or "" for a
	// generic one.
	Icon string
}

type linkView struct {
	Name  string
	Label string
	URL   string
	JSON  string
}

type tunnelView struct {
	Name     string
	Label    string
	Kind     string
	Filename string
	Enable   bool
	Conf     string
	QR       template.URL
}

// T is a string in the visitor's language.
func (v view) T(key string) string { return v.tr.T(key, nil) }

// TD is a string with one placeholder filled: {{.N}}.
func (v view) TD(key string, n any) string { return v.tr.T(key, map[string]any{"N": n}) }

// Render answers r with the page.
func Render(w http.ResponseWriter, r *http.Request, p Page) {
	tr := newTranslator(r)
	v := view{
		tr:        tr,
		Lang:      tr.lang,
		RTL:       tr.rtl(),
		Title:     p.Title,
		SubURL:    p.SubURL,
		SubURLs:   p.SubURLs,
		Usage:     p.Usage.view(time.Now()),
		Languages: languages,
	}
	for _, app := range p.Apps {
		v.Apps = append(v.Apps, appView{App: app, Icon: platformIcon(app.Platform)})
	}
	if v.Title == "" {
		v.Title = tr.T("page.title", nil)
	}
	if p.SubURL != "" {
		v.SubQR = qrDataURI(p.SubURL, qrcode.Medium, 256)
	}
	for i, link := range p.Links {
		v.Links = append(v.Links, linkView{Name: LinkName(link.URL, i), Label: LinkLabel(link.URL), URL: link.URL, JSON: link.JSON})
	}
	for _, tun := range p.Tunnels {
		filename := tun.Filename
		if filename == "" {
			filename = tun.Kind
		}
		v.Tunnels = append(v.Tunnels, tunnelView{
			Name: tun.Name, Label: TunnelLabel(tun.Kind, tun.Conf), Kind: tun.Kind, Filename: filename,
			Enable: tun.Enable, Conf: tun.Conf,
			// Level L at 4 px a module: an AmneziaWG config is long.
			QR: qrDataURI(tun.Conf, qrcode.Low, -4),
		})
	}

	var body bytes.Buffer
	if err := pageTemplate.Execute(&body, v); err != nil {
		logger.Warning("subscription page:", err)
		http.Error(w, "subscription page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

// platformIcon picks the icon of an app card from its platform: android,
// apple, windows, or "" — the generic one — for none of them or several.
func platformIcon(platform string) string {
	p := strings.ToLower(platform)
	icon := ""
	for _, family := range []struct {
		icon  string
		words []string
	}{
		{"android", []string{"android"}},
		{"apple", []string{"iphone", "ipad", "ios", "mac", "apple"}},
		{"windows", []string{"windows"}},
	} {
		for _, word := range family.words {
			if strings.Contains(p, word) {
				if icon != "" && icon != family.icon {
					return ""
				}
				icon = family.icon
			}
		}
	}
	return icon
}

// qrDataURI is a QR code of text as a PNG data URI, "" when the text does not
// fit one.
func qrDataURI(text string, level qrcode.RecoveryLevel, size int) template.URL {
	png, err := qrcode.Encode(text, level, size)
	if err != nil {
		return ""
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
}
