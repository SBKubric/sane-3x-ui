package service

import (
	"strings"

	"github.com/coinman-dev/3ax-ui/v2/database"
	"github.com/coinman-dev/3ax-ui/v2/web/entity"

	"gorm.io/gorm"
)

// The public subscription address (#224, entity/sub_public.go). It lives in
// defaultValueMap and entity.AllSetting like the other sub settings; no
// migration is needed. Every producer of a subscription link reads it when it
// builds one — the bot, the sub server's page and headers, the panel's
// defaultSettings, the chain document — so a new address takes effect on
// save, and the link broadcast's detector sees the change on its next look.

// GetSubPublicURL is the origin every subscription link starts with, such as
// https://sub.example.com; "" when none is set. A stored value that is not an
// origin (written past the form) reads as none.
func (s *SettingService) GetSubPublicURL() (string, error) {
	raw, err := s.getString("subPublicURL")
	if err != nil {
		return "", err
	}
	normalized, err := entity.NormalizeSubPublicURL(raw)
	if err != nil {
		return "", nil
	}
	return normalized, nil
}

// SubPublicLink is a subscription link through the public address: the
// origin, the panel's own path with both its slashes, and the subId ("" for
// the links' base).
func SubPublicLink(origin, path, subId string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return origin + path + subId
}

// subPublicURLChanged moves a panel with a chain to a new revision after a
// new public subscription address was saved: the address travels in every
// chain document, and the hops poll by ETag.
func subPublicURLChanged() error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error { return bumpIfChained(tx) })
}
