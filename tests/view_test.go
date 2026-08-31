package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbobjects "github.com/bg12345/gorm-dbobjects"
	"github.com/bg12345/gorm-dbobjects/internal/testutil"
	"github.com/bg12345/gorm-dbobjects/view"
	"gorm.io/gorm"
)

// TestRegister_AppliesView is an integration test: it connects to a
// real Postgres, registers a view built via view.Query(fn) -- the
// callback path, which needs a live db.ToSQL resolution, unlike the
// Raw() path already covered by dialect_test.go -- and verifies
// querying the view returns only the row the callback's Where clause
// actually matches, proving the auto-appended .Find() terminal call in
// renderViewCreateOrReplace resolves real SQL, not empty/wrong SQL.
// Skips itself if no Postgres is reachable.
func TestRegister_AppliesView(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)

	v := view.New("v_test_active_users").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "ViewTestMatch")
		})

	if err := client.Register(context.Background(), v); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Drop(context.Background(), v)
	})

	match := testutil.UserMaster{Name: "ViewTestMatch", Email: fmt.Sprintf("match-%d@example.com", time.Now().UnixNano())}
	nonMatch := testutil.UserMaster{Name: "ViewTestNonMatch", Email: fmt.Sprintf("nonmatch-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("Create match: %v", err)
	}
	if err := db.Create(&nonMatch).Error; err != nil {
		t.Fatalf("Create nonMatch: %v", err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&match)
		db.Unscoped().Delete(&nonMatch)
	})

	var rows []testutil.UserMaster
	if err := db.Table("v_test_active_users").Find(&rows).Error; err != nil {
		t.Fatalf("querying view: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("view returned %d row(s), want 1: %+v", len(rows), rows)
	}
	if rows[0].Name != "ViewTestMatch" {
		t.Errorf("view returned row with Name = %q, want %q", rows[0].Name, "ViewTestMatch")
	}
}

// TestRegister_MySQL_AppliesView is the MySQL equivalent of
// TestRegister_AppliesView. Skips itself if no MySQL is reachable.
func TestRegister_MySQL_AppliesView(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)

	v := view.New("v_test_active_users_mysql").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "ViewTestMatchMySQL")
		})

	if err := client.Register(context.Background(), v); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Drop(context.Background(), v)
	})

	match := testutil.UserMaster{Name: "ViewTestMatchMySQL", Email: fmt.Sprintf("match-mysql-%d@example.com", time.Now().UnixNano())}
	nonMatch := testutil.UserMaster{Name: "ViewTestNonMatchMySQL", Email: fmt.Sprintf("nonmatch-mysql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("Create match: %v", err)
	}
	if err := db.Create(&nonMatch).Error; err != nil {
		t.Fatalf("Create nonMatch: %v", err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&match)
		db.Unscoped().Delete(&nonMatch)
	})

	var rows []testutil.UserMaster
	if err := db.Table("v_test_active_users_mysql").Find(&rows).Error; err != nil {
		t.Fatalf("querying view: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("view returned %d row(s), want 1: %+v", len(rows), rows)
	}
	if rows[0].Name != "ViewTestMatchMySQL" {
		t.Errorf("view returned row with Name = %q, want %q", rows[0].Name, "ViewTestMatchMySQL")
	}
}

// TestRegister_SQLite_AppliesView is the SQLite equivalent of
// TestRegister_AppliesView. SQLite has no external service to be
// unreachable, so this never self-skips (see newSQLiteDSN in
// trigger_test.go).
func TestRegister_SQLite_AppliesView(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)

	v := view.New("v_test_active_users_sqlite").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "ViewTestMatchSQLite")
		})

	if err := client.Register(context.Background(), v); err != nil {
		t.Fatalf("Register: %v", err)
	}

	match := testutil.UserMaster{Name: "ViewTestMatchSQLite", Email: fmt.Sprintf("match-sqlite-%d@example.com", time.Now().UnixNano())}
	nonMatch := testutil.UserMaster{Name: "ViewTestNonMatchSQLite", Email: fmt.Sprintf("nonmatch-sqlite-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("Create match: %v", err)
	}
	if err := db.Create(&nonMatch).Error; err != nil {
		t.Fatalf("Create nonMatch: %v", err)
	}

	var rows []testutil.UserMaster
	if err := db.Table("v_test_active_users_sqlite").Find(&rows).Error; err != nil {
		t.Fatalf("querying view: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("view returned %d row(s), want 1: %+v", len(rows), rows)
	}
	if rows[0].Name != "ViewTestMatchSQLite" {
		t.Errorf("view returned row with Name = %q, want %q", rows[0].Name, "ViewTestMatchSQLite")
	}
}

// TestRegister_SQLServer_AppliesView is the SQL Server equivalent of
// TestRegister_AppliesView -- proves CREATE OR ALTER VIEW (SQL Server's
// idempotent-native view syntax, unlike Postgres/MySQL's CREATE OR
// REPLACE VIEW) actually works end to end via the Query(fn) callback
// path, not just the DB-less Raw() path already covered by
// dialect_test.go. Skips itself if no SQL Server is reachable.
func TestRegister_SQLServer_AppliesView(t *testing.T) {
	db, err := testutil.NewSQLServer()
	if err != nil {
		t.Skipf("skipping integration test, no SQL Server reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)

	v := view.New("v_test_active_users_sqlserver").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "ViewTestMatchSQLServer")
		})

	if err := client.Register(context.Background(), v); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), v) })

	match := testutil.UserMaster{Name: "ViewTestMatchSQLServer", Email: fmt.Sprintf("match-sqlserver-%d@example.com", time.Now().UnixNano())}
	nonMatch := testutil.UserMaster{Name: "ViewTestNonMatchSQLServer", Email: fmt.Sprintf("nonmatch-sqlserver-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("Create match: %v", err)
	}
	if err := db.Create(&nonMatch).Error; err != nil {
		t.Fatalf("Create nonMatch: %v", err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&match)
		db.Unscoped().Delete(&nonMatch)
	})

	var rows []testutil.UserMaster
	if err := db.Table("v_test_active_users_sqlserver").Find(&rows).Error; err != nil {
		t.Fatalf("querying view: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("view returned %d row(s), want 1: %+v", len(rows), rows)
	}
	if rows[0].Name != "ViewTestMatchSQLServer" {
		t.Errorf("view returned row with Name = %q, want %q", rows[0].Name, "ViewTestMatchSQLServer")
	}
}
