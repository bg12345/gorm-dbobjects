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

func TestTrigger_AfterInsert(t *testing.T) {
	def, err := trigger.AfterInsert(&testutil.UserMaster{}).
		Set("name", trigger.Raw("'unknown'")).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Timing != "AFTER" || def.Event != "INSERT" {
		t.Errorf("Timing/Event = %s/%s, want AFTER/INSERT", def.Timing, def.Event)
	}
	// Set() on an AFTER trigger renders as a follow-up UPDATE keyed by
	// primary key (see dialect.go), so Build() must resolve one.
	if def.PrimaryKey != "id" {
		t.Errorf("PrimaryKey = %q, want %q", def.PrimaryKey, "id")
	}
}

func TestTrigger_AfterUpdate(t *testing.T) {
	def, err := trigger.AfterUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Timing != "AFTER" || def.Event != "UPDATE" {
		t.Errorf("Timing/Event = %s/%s, want AFTER/UPDATE", def.Timing, def.Event)
	}
	if def.PrimaryKey != "id" {
		t.Errorf("PrimaryKey = %q, want %q", def.PrimaryKey, "id")
	}
}

// TestTrigger_AfterUpdate_Set_RequiresSinglePrimaryKey verifies Build()
// rejects Set() on an AFTER trigger for a table with a composite (or
// missing) primary key -- the follow-up UPDATE dialect.go renders needs
// exactly one column to target the right row unambiguously.
func TestTrigger_AfterUpdate_Set_RequiresSinglePrimaryKey(t *testing.T) {
	type compositeKeyModel struct {
		OrgID  string `gorm:"primaryKey"`
		UserID string `gorm:"primaryKey"`
		Status string
	}

	_, err := trigger.AfterUpdate(&compositeKeyModel{}).
		Set("status", trigger.Raw("'x'")).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for AFTER Set() on a composite-primary-key table")
	}
}

// TestTrigger_BeforeUpdate_Set_NoPrimaryKeyRequired confirms PrimaryKey
// resolution is scoped to AFTER triggers only -- a BEFORE trigger renders
// Set() as a direct NEW.col assignment and never needs one, so Build()
// shouldn't require (or populate) it there.
func TestTrigger_BeforeUpdate_Set_NoPrimaryKeyRequired(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.PrimaryKey != "" {
		t.Errorf("PrimaryKey = %q, want empty for a BEFORE trigger", def.PrimaryKey)
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

func TestTrigger_SetColumns(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMasterLogs{}).
		SetColumns(map[string]trigger.Expr{
			"ip_address":  trigger.Raw("'0.0.0.0'"),
			"log_message": trigger.Raw("'unknown'"),
		}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(def.Sets) != 2 {
		t.Errorf("len(Sets) = %d, want 2", len(def.Sets))
	}
}

func TestTrigger_SetColumns_UnknownColumn(t *testing.T) {
	_, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		SetColumns(map[string]trigger.Expr{"does_not_exist": trigger.Now()}).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for unknown column")
	}
}

func TestTrigger_SetColumns_RejectedOnDelete(t *testing.T) {
	_, err := trigger.AfterDelete(&testutil.UserMaster{}).
		SetColumns(map[string]trigger.Expr{"name": trigger.Raw("'x'")}).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error using SetColumns() on a DELETE trigger")
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

func TestTrigger_BeforeDelete_RequiresBody(t *testing.T) {
	_, err := trigger.BeforeDelete(&testutil.UserMaster{}).Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error when a DELETE trigger has no Body()")
	}
}

func TestTrigger_Set_RejectedOnDelete(t *testing.T) {
	_, err := trigger.AfterDelete(&testutil.UserMaster{}).
		Set("name", trigger.Raw("'x'")).
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error using Set() on a DELETE trigger")
	}
}

func TestTrigger_AfterDelete_Body(t *testing.T) {
	def, err := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) VALUES (OLD.id);").
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Timing != "AFTER" || def.Event != "DELETE" {
		t.Errorf("Timing/Event = %s/%s, want AFTER/DELETE", def.Timing, def.Event)
	}
	if def.Body == "" {
		t.Error("Body is empty, want the raw SQL passed to Body()")
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

// TestRegister_AppliesDeleteTrigger is an integration test: it connects to a
// real Postgres and verifies an AfterDelete(...).Body(...) trigger actually
// fires at the database level on a real DELETE. gorm's default Delete does a
// soft delete (UPDATE deleted_at) on a model embedding gorm.Model, which
// would never fire a DELETE trigger at all, so this uses Unscoped().Delete
// to issue a real DELETE statement. Skips itself if no Postgres is reachable.
func TestRegister_AppliesDeleteTrigger(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_delete_audit (
		id SERIAL PRIMARY KEY,
		deleted_user_id BIGINT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_delete_audit") })

	dbobjects.Init(db)
	tr := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) VALUES (OLD.id);")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = dbobjects.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Grace", Email: fmt.Sprintf("grace-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := db.Unscoped().Delete(&user).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_delete_audit WHERE deleted_user_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for deleted user = %d, want 1 (AFTER DELETE trigger should have fired)", count)
	}
}

// TestRegister_AppliesAfterInsertTrigger is an integration test: it connects
// to a real Postgres and verifies an AfterInsert(...).Body(...) trigger
// fires on a real INSERT. Uses Body(), not Set() -- an AFTER trigger's NEW
// assignment has no effect on the already-written row on Postgres (silently
// ignored) and is a hard error on MySQL ("Updating of NEW row is not
// allowed in after trigger"), so Set()'s NEW.col=expr model only makes
// sense for BEFORE triggers. NEW is still readable (just not assignable)
// in an AFTER trigger, which is what the audit-insert body relies on.
// Skips itself if no Postgres is reachable.
func TestRegister_AppliesAfterInsertTrigger(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_insert_audit (
		id SERIAL PRIMARY KEY,
		user_master_id BIGINT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_insert_audit") })

	dbobjects.Init(db)
	tr := trigger.AfterInsert(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_insert_audit(user_master_id) VALUES (NEW.id);")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = dbobjects.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Alan", Email: fmt.Sprintf("alan-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_insert_audit WHERE user_master_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for inserted user = %d, want 1 (AFTER INSERT trigger should have fired)", count)
	}
}

// TestRegister_AppliesAfterUpdateTrigger is an integration test: it
// connects to a real Postgres and verifies an AfterUpdate(...).Body(...)
// trigger fires on a real UPDATE. Same Body()-not-Set() reasoning as
// TestRegister_AppliesAfterInsertTrigger above. Skips itself if no
// Postgres is reachable.
func TestRegister_AppliesAfterUpdateTrigger(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_update_audit (
		id SERIAL PRIMARY KEY,
		user_master_id BIGINT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_update_audit") })

	dbobjects.Init(db)
	tr := trigger.AfterUpdate(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_update_audit(user_master_id) VALUES (NEW.id);")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = dbobjects.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Alan", Email: fmt.Sprintf("alan-upd-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	if err := db.Model(&user).UpdateColumn("name", "Alan Updated").Error; err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_update_audit WHERE user_master_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for updated user = %d, want 1 (AFTER UPDATE trigger should have fired)", count)
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

// TestRegister_MySQL_AppliesDeleteTrigger is an integration test: it
// connects to a real MySQL and verifies an AfterDelete(...).Body(...)
// trigger actually fires on a real DELETE -- the MySQL equivalent of
// TestRegister_AppliesDeleteTrigger above. Skips itself if no MySQL is
// reachable.
func TestRegister_MySQL_AppliesDeleteTrigger(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_delete_audit (
		id INT AUTO_INCREMENT PRIMARY KEY,
		deleted_user_id BIGINT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_delete_audit") })

	dbobjects.Init(db)
	tr := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) VALUES (OLD.id);")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = dbobjects.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Grace", Email: fmt.Sprintf("grace-mysql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := db.Unscoped().Delete(&user).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_delete_audit WHERE deleted_user_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for deleted user = %d, want 1 (AFTER DELETE trigger should have fired)", count)
	}
}

// TestRegister_MySQL_AppliesAfterInsertTrigger is an integration test: it
// connects to a real MySQL and verifies an AfterInsert(...).Body(...)
// trigger fires on a real INSERT -- the MySQL equivalent of
// TestRegister_AppliesAfterInsertTrigger above. Skips itself if no MySQL
// is reachable.
func TestRegister_MySQL_AppliesAfterInsertTrigger(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_insert_audit (
		id INT AUTO_INCREMENT PRIMARY KEY,
		user_master_id BIGINT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_insert_audit") })

	dbobjects.Init(db)
	tr := trigger.AfterInsert(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_insert_audit(user_master_id) VALUES (NEW.id);")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = dbobjects.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Alan", Email: fmt.Sprintf("alan-mysql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_insert_audit WHERE user_master_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for inserted user = %d, want 1 (AFTER INSERT trigger should have fired)", count)
	}
}

// TestRegister_MySQL_AppliesAfterUpdateTrigger is an integration test: it
// connects to a real MySQL and verifies an AfterUpdate(...).Body(...)
// trigger fires on a real UPDATE -- the MySQL equivalent of
// TestRegister_AppliesAfterUpdateTrigger above. Skips itself if no MySQL
// is reachable.
func TestRegister_MySQL_AppliesAfterUpdateTrigger(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_update_audit (
		id INT AUTO_INCREMENT PRIMARY KEY,
		user_master_id BIGINT NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_update_audit") })

	dbobjects.Init(db)
	tr := trigger.AfterUpdate(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_update_audit(user_master_id) VALUES (NEW.id);")
	if err := dbobjects.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = dbobjects.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Alan", Email: fmt.Sprintf("alan-mysql-upd-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	if err := db.Model(&user).UpdateColumn("name", "Alan Updated").Error; err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_update_audit WHERE user_master_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for updated user = %d, want 1 (AFTER UPDATE trigger should have fired)", count)
	}
}

// TestRegister_MySQL_CompensatesOnFailure is an integration test: it
// connects to a real MySQL (transactional() == false -- the only
// dialect that exercises Register's compensating-rollback path) and
// verifies that when a later object in one Register call
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
