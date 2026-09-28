package database

import (
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
)

// The users table (docs/spec/users.md): the name is unique in the schema, not
// only in the service.
func TestSubUsersTableEnforcesUniqueName(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
	db := GetDB()

	if err := db.Create(&model.SubUser{SubId: "s1", Name: "ivan"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Create(&model.SubUser{SubId: "s2", Name: "ivan"}).Error; err == nil {
		t.Error("a second user named ivan was accepted")
	}
	if err := db.Create(&model.SubUser{SubId: "s1", Name: "petr"}).Error; err == nil {
		t.Error("a second user with subId s1 was accepted")
	}
}
