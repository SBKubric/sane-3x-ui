package entity

import (
	"github.com/coinman-dev/3ax-ui/v2/clientrouting"
	"github.com/coinman-dev/3ax-ui/v2/util/common"
)

// checkSubClientRouting checks the client routing (#263) the settings form
// sends and turns it into the value to store: the object re-indented with
// every field, or "" for none (clientrouting.NormalizeSetting).
func checkSubClientRouting(s *AllSetting) error {
	normalized, err := clientrouting.NormalizeSetting(s.SubClientRouting)
	if err != nil {
		return common.NewErrorf("client routing: %v", err)
	}
	s.SubClientRouting = normalized
	return nil
}
