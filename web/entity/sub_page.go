package entity

import (
	"github.com/coinman-dev/3ax-ui/v2/subpage"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// checkSubPageApps checks the subscription page's app list (#235) the
// settings form sends and turns it into the value to store: the list
// re-indented, or "" for the built-in one (subpage.NormalizeAppsSetting).
func checkSubPageApps(s *AllSetting) error {
	normalized, err := subpage.NormalizeAppsSetting(s.SubPageApps)
	if err != nil {
		return common.NewErrorf("subscription page apps: %v", err)
	}
	s.SubPageApps = normalized
	return nil
}
