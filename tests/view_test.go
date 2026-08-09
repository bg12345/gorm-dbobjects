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

	dbobjects.Init(db)

	v := view.New("v_test_active_users").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "ViewTestMatch")
		})

	if err := dbobjects.Register(context.Background(), v); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		_ = dbobjects.Drop(context.Background(), v)
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

	dbobjects.Init(db)

	v := view.New("v_test_active_users_mysql").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "ViewTestMatchMySQL")
		})

	if err := dbobjects.Register(context.Background(), v); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		_ = dbobjects.Drop(context.Background(), v)
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
