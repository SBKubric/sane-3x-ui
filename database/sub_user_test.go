package database

import (
	"path/filepath"
	"testing"

	"github.com/coinman-dev/3ax-ui/v2/database/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

// TestSubUsersGainTheContactEmail: a database from before #193 gets the
// contact_email column, empty for its users, and keeps them.
func TestSubUsersGainTheContactEmail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x-ui.db")
	old, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Exec(`CREATE TABLE sub_users (sub_id text PRIMARY KEY, name text NOT NULL, tg_id integer NOT NULL DEFAULT 0,
		comment text NOT NULL DEFAULT '', created_at integer, updated_at integer)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := old.Exec(`INSERT INTO sub_users (sub_id, name, tg_id) VALUES ('s1', 'ivan', 42)`).Error; err != nil {
		t.Fatal(err)
	}
	if db, err := old.DB(); err == nil {
		db.Close()
	}

	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { CloseDB() })
	var u model.SubUser
	if err := GetDB().First(&u, "sub_id = ?", "s1").Error; err != nil {
		t.Fatalf("the old user: %v", err)
	}
	if u.Name != "ivan" || u.TgId != 42 || u.ContactEmail != "" {
		t.Errorf("after the migration: %+v", u)
	}
	u.ContactEmail = "ivan@example.org"
	if err := GetDB().Save(&u).Error; err != nil {
		t.Fatalf("save a contact email: %v", err)
	}
}
