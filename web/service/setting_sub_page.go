package service

import (
	"github.com/coinman-dev/3ax-ui/v2/subpage"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"
)

// The subscription page's app list (#235, entity/sub_page.go): the client
// apps the page recommends, each with the protocol labels it works with.
// Stored as "" while the owner keeps the built-in list, so a release that
// changes subpage.DefaultApps reaches every panel that never edited it; the
// settings form shows the built-in list in its place (subPageAppsForForm).

// GetSubPageApps is the app list the page shows. A stored value that does
// not parse — written past the form — gives the built-in list, with the
// error for the caller to log.
func (s *SettingService) GetSubPageApps() ([]subpage.App, error) {
	raw, err := s.getString("subPageApps")
	if err != nil {
		return subpage.DefaultApps(), err
	}
	apps, err := subpage.ParseApps(raw)
	if err != nil {
		return subpage.DefaultApps(), err
	}
	return apps, nil
}

// subPageAppsForForm puts the list the page shows into the settings form:
// the built-in one when nothing is stored, for the owner to edit.
func subPageAppsForForm(all *entity.AllSetting) {
	if all.SubPageApps == "" {
		all.SubPageApps = subpage.AppsJSON(subpage.DefaultApps())
	}
}
