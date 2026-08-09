package tests

import (
	"fmt"
	"testing"
	"time"
	"context"

	dbobjects "github.com/bg12345/gorm-dbobjects"
	"github.com/bg12345/gorm-dbobjects/internal/testutil"
	"github.com/bg12345/gorm-dbobjects/trigger"
)

// --- trigger.Build() unit tests (no DB required) ---

func TestTrigger_Build(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Table != "user_master" {
		t.Errorf("Table = %q, want %q", def.Table, "user_master")
	}
	if def.Timing != "BEFORE" || def.Event != "UPDATE" {
		t.Errorf("Timing/Event = %s/%s, want BEFORE/UPDATE", def.Timing, def.Event)
	}
	if got := def.Sets["updated_at"].Kind; got != trigger.ExprNow {
		t.Errorf("Sets[updated_at].Kind = %v, want ExprNow", got)
	}
}

func TestTrigger_BeforeInsert(t *testing.T) {
	def, err := trigger.BeforeInsert(&testutil.UserMaster{}).
		Set("name", trigger.Raw("'unknown'")).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Event != "INSERT" {
		t.Errorf("Event = %s, want INSERT", def.Event)
	}
}

func TestTrigger_MultipleSets(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMasterLogs{}).
		Set("ip_address", trigger.Raw("'0.0.0.0'")).
		Set("log_message", trigger.Raw("'unknown'")).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(def.Sets) != 2 {
		t.Errorf("len(Sets) = %d, want 2", len(def.Sets))
	}
}

func TestTrigger_Set_UnknownColumn(t *testing.T) {
	_, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("does_not_exist", trigger.Now()).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for unknown column")
	}
}

func TestTrigger_Build_NoColumnsSet(t *testing.T) {
	_, err := trigger.BeforeUpdate(&testutil.UserMaster{}).Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error when no columns were Set")
	}
}

func TestTrigger_Kind(t *testing.T) {
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if got := tr.Kind(); got != "trigger" {
		t.Errorf("Kind() = %q, want %q", got, "trigger")
	}
}

func TestTrigger_Name(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Name("trg_custom_name").
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Name != "trg_custom_name" {
		t.Errorf("Name = %q, want %q", def.Name, "trg_custom_name")
	}
}

func TestTrigger_Name_DefaultsEmpty(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Name != "" {
		t.Errorf("Name = %q, want empty when Name() was never called", def.Name)
	}
}

// --- dbobjects.Register() ---

func TestRegister_WithoutInit(t *testing.T) {
	// Init(nil) resets dbobjects' package-level DB handle so this test's
	// outcome doesn't depend on whether another test already called Init.
	dbobjects.Init(nil)

	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := dbobjects.Register(context.Background(), tr); err == nil {
		t.Fatal("Register() error = nil, want error when Init was never called")
	}
}

// TestRegister_AppliesTrigger is an integration test: it connects to a real
// Postgres (via internal/testutil.NewPostgres, configured through .env) and
// verifies the registered BEFORE UPDATE trigger actually overrides
// updated_at at the database level, independent of gorm's own hooks. It
// skips itself if no DB is reachable.
func TestRegister_AppliesTrigger(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	dbobjects.Init(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	// Force updated_at to an old value via a raw column update. This
	// bypasses gorm's own timestamp hook, but it's still a real UPDATE
	// statement, so the DB-level trigger fires on it regardless.
	old := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	if err := db.Model(&user).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}

	if reloaded.UpdatedAt.Equal(old) {
		t.Fatal("UpdatedAt still equals the value we tried to force; trigger did not fire")
	}
	if time.Since(reloaded.UpdatedAt) > 10*time.Second {
		t.Fatalf("UpdatedAt = %v, want a timestamp close to now (trigger should have set NOW())", reloaded.UpdatedAt)
	}
}

// TestRegister_AppliesCustomName is an integration test verifying that a
// trigger built with Name(...) is created in Postgres under that exact
// name, with its backing function paired as fn_<name without trg_ prefix>
// (see dialect.go's triggerNames). It skips itself if no DB is reachable.
func TestRegister_AppliesCustomName(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	dbobjects.Init(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Name("trg_users_touch")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DROP TRIGGER IF EXISTS trg_users_touch ON user_master")
		db.Exec("DROP FUNCTION IF EXISTS fn_users_touch()")
	})

	var trgCount int64
	if err := db.Raw(`SELECT count(*) FROM pg_trigger WHERE tgname = ?`, "trg_users_touch").Scan(&trgCount).Error; err != nil {
		t.Fatalf("querying pg_trigger: %v", err)
	}
	if trgCount != 1 {
		t.Errorf("pg_trigger rows named trg_users_touch = %d, want 1", trgCount)
	}

	var fnCount int64
	if err := db.Raw(`SELECT count(*) FROM pg_proc WHERE proname = ?`, "fn_users_touch").Scan(&fnCount).Error; err != nil {
		t.Fatalf("querying pg_proc: %v", err)
	}
	if fnCount != 1 {
		t.Errorf("pg_proc rows named fn_users_touch = %d, want 1", fnCount)
	}
}

// TestRegister_MySQL_AppliesTrigger is an integration test: it connects
// to a real MySQL (via internal/testutil.NewMySQL, configured through
// .env) and verifies the registered BEFORE UPDATE trigger actually
// overrides updated_at at the database level, independent of gorm's own
// hooks -- the MySQL equivalent of TestRegister_AppliesTrigger above.
// Skips itself if no MySQL is reachable.
func TestRegister_MySQL_AppliesTrigger(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	dbobjects.Init(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-mysql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	// Force updated_at to an old value via a raw column update. This
	// bypasses gorm's own timestamp hook, but it's still a real UPDATE
	// statement, so the DB-level trigger fires on it regardless.
	old := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	if err := db.Model(&user).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}

	if reloaded.UpdatedAt.Equal(old) {
		t.Fatal("UpdatedAt still equals the value we tried to force; trigger did not fire")
	}
	if time.Since(reloaded.UpdatedAt) > 10*time.Second {
		t.Fatalf("UpdatedAt = %v, want a timestamp close to now (trigger should have set NOW())", reloaded.UpdatedAt)
	}
}

// TestRegister_MySQL_CompensatesOnFailure is an integration test: it
// connects to a real MySQL (transactional() == false, docs/PLAN.md §2.5
// -- the only dialect that exercises Register's compensating-rollback
// path) and verifies that when a later object in one Register call
// fails, an earlier object that already succeeded gets compensated
// (dropped) rather than left behind half-applied. The second trigger is
// engineered to fail at *execution* time via an intentionally invalid
// Raw() expression -- Set() only validates that the column exists, not
// that the expression is valid SQL, so this fails inside MySQL's own
// CREATE TRIGGER parsing, after the first trigger has already been
// applied. Skips itself if no MySQL is reachable.
func TestRegister_MySQL_CompensatesOnFailure(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	dbobjects.Init(db)

	good := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Name("trg_compensation_good")
	bad := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("name", trigger.Raw("this is not valid sql !!!")).
		Name("trg_compensation_bad")

	t.Cleanup(func() {
		db.Exec("DROP TRIGGER IF EXISTS trg_compensation_good")
		db.Exec("DROP TRIGGER IF EXISTS trg_compensation_bad")
	})

	if err := dbobjects.Register(context.Background(), good, bad); err == nil {
		t.Fatal("Register() error = nil, want error from the intentionally invalid second trigger")
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.triggers WHERE trigger_name = ?`,
		"trg_compensation_good").Scan(&count).Error; err != nil {
		t.Fatalf("querying information_schema.triggers: %v", err)
	}
	if count != 0 {
		t.Errorf("trg_compensation_good still exists after Register failed on a later object; "+
			"compensation did not run. count = %d, want 0", count)
	}
}
