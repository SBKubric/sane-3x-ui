package subpage

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
)

// The page's own strings, in every language of the panel (#235). They live
// beside the template rather than in web/translation because a hop renders
// the page too, and the panel's 1.6 MB of translations has no business on a
// hop; the panel's settings strings stay in web/translation.
//
//go:embed translation/*.toml
var translationFS embed.FS

// language is one entry of the page's language menu.
type pageLanguage struct {
	Code string
	Name string
}

// languages are the panel's languages (LanguageManager.supportedLanguages
// in web/assets/js/util/index.js), each with a translation file here.
var languages = []pageLanguage{
	{"en-US", "English"},
	{"ru-RU", "Русский"},
	{"uk-UA", "Українська"},
	{"ar-EG", "العربية"},
	{"fa-IR", "فارسی"},
	{"zh-CN", "简体中文"},
	{"zh-TW", "繁體中文"},
	{"ja-JP", "日本語"},
	{"vi-VN", "Tiếng Việt"},
	{"es-ES", "Español"},
	{"id-ID", "Indonesian"},
	{"tr-TR", "Türkçe"},
	{"pt-BR", "Português"},
}

var (
	bundleOnce sync.Once
	bundle     *i18n.Bundle
	bundleErr  error
)

func translations() (*i18n.Bundle, error) {
	bundleOnce.Do(func() {
		b := i18n.NewBundle(language.MustParse("en-US"))
		b.RegisterUnmarshalFunc("toml", toml.Unmarshal)
		bundleErr = fs.WalkDir(translationFS, "translation", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := translationFS.ReadFile(path)
			if err != nil {
				return err
			}
			_, err = b.ParseMessageFileBytes(data, path)
			return err
		})
		bundle = b
	})
	return bundle, bundleErr
}

// translator is the page's strings in one language.
type translator struct {
	lang     string
	page     *i18n.Localizer
	fallback *i18n.Localizer
}

// newTranslator picks the visitor's language: the lang cookie the language
// menu sets (the panel's own pages use the same cookie), then
// Accept-Language, then English.
func newTranslator(r *http.Request) translator {
	b, err := translations()
	if err != nil || b == nil {
		return translator{lang: "en-US"}
	}
	var prefs []string
	if c, err := r.Cookie("lang"); err == nil && c.Value != "" {
		prefs = append(prefs, c.Value)
	}
	prefs = append(prefs, r.Header.Get("Accept-Language"))
	t := translator{lang: "en-US", page: i18n.NewLocalizer(b, prefs...), fallback: i18n.NewLocalizer(b, "en-US")}
	if _, tag, err := t.page.LocalizeWithTag(&i18n.LocalizeConfig{MessageID: "page.title"}); err == nil {
		t.lang = tag.String()
	}
	return t
}

// T is one string, with data for its {{.Name}} placeholders; the key itself
// when no language has it.
func (t translator) T(key string, data map[string]any) string {
	for _, l := range []*i18n.Localizer{t.page, t.fallback} {
		if l == nil {
			continue
		}
		if msg, err := l.Localize(&i18n.LocalizeConfig{MessageID: key, TemplateData: data}); err == nil {
			return msg
		}
	}
	return key
}

// rtl reports whether the language is written right to left.
func (t translator) rtl() bool {
	return strings.HasPrefix(t.lang, "ar") || strings.HasPrefix(t.lang, "fa")
}
