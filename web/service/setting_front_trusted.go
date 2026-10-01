package service

import (
	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"

	"gorm.io/gorm"
)

// The front's trusted addresses (#228, entity/front_trusted.go). The setting
// lives in defaultValueMap and entity.AllSetting like the other preferences,
// so no migration is needed. It reaches two places: every hop's chain
// document (frontTrustedAddrs), and the panel's own front, whose guard
// renders it every half minute (nginx_guard.go).

// GetFrontTrustedAddrs is the list, normalised; nil for none. A stored value
// with an entry that is not an address (written past the form) reads as
// none rather than half a list.
func (s *SettingService) GetFrontTrustedAddrs() ([]string, error) {
	raw, err := s.getString("frontTrustedAddrs")
	if err != nil {
		return nil, err
	}
	list, err := entity.ParseFrontTrustedAddrs(raw)
	if err != nil {
		return nil, nil
	}
	return list, nil
}

// SetFrontTrustedAddrs checks and stores the list ("" clears it), and moves a
// chained panel to a new revision when the list changed. The CLI calls it.
func (s *SettingService) SetFrontTrustedAddrs(raw string) error {
	normalized, err := entity.NormalizeFrontTrustedAddrs(raw)
	if err != nil {
		return err
	}
	previous, _ := s.getString("frontTrustedAddrs")
	if err := s.setString("frontTrustedAddrs", normalized); err != nil {
		return err
	}
	if normalized != previous {
		return frontTrustedAddrsChanged()
	}
	return nil
}

// frontTrustedAddrsChanged moves a panel with a chain to a new revision: the
// list travels in every chain document, and the hops poll by ETag.
func frontTrustedAddrsChanged() error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error { return bumpIfChained(tx) })
}
