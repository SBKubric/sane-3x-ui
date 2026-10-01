package subpage

import (
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// Every language of the page has a file, and every file every key of the
// English one: a missing key would show English in the middle of a page.
func TestEveryLanguageHasEveryString(t *testing.T) {
	keys := func(name string) []string {
		raw, err := fs.ReadFile(translationFS, "translation/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]map[string]string
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return slices.Sorted(maps.Keys(doc["page"]))
	}
	want := keys("translate.en_US.toml")
	if len(want) == 0 {
		t.Fatal("the English file has no page strings")
	}
	for _, lang := range languages {
		name := "translate." + strings.ReplaceAll(lang.Code, "-", "_") + ".toml"
		if got := keys(name); !slices.Equal(got, want) {
			t.Errorf("%s keys differ from English:\n got %v\nwant %v", name, got, want)
		}
	}
	files, _ := fs.ReadDir(translationFS, "translation")
	if len(files) != len(languages) {
		t.Errorf("%d translation files for %d languages", len(files), len(languages))
	}
	if _, err := translations(); err != nil {
		t.Fatalf("the translations do not load: %v", err)
	}
}
