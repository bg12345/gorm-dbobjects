package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// TestTrigger_BeforeUpdate_Set_PrimaryKeyResolvedBestEffort confirms
// PrimaryKey is now resolved for a BEFORE trigger too, whenever the
// table has exactly one primary key column -- widened so SQL Server's
// Set()-based UPDATE ... FROM join (which forces AFTER under the hood
// even for a BEFORE-declared trigger, since SQL Server has no BEFORE
// DML trigger at all) has something to target. Harmless for
// Postgres/MySQL, which never read PrimaryKey on a BEFORE-declared
// trigger (NEW.col = expr doesn't need it).
func TestTrigger_BeforeUpdate_Set_PrimaryKeyResolvedBestEffort(t *testing.T) {
	def, err := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.PrimaryKey != "id" {
		t.Errorf("PrimaryKey = %q, want %q", def.PrimaryKey, "id")
	}
}

// TestTrigger_BeforeUpdate_Set_CompositePrimaryKeyLeavesEmpty confirms
// a BEFORE trigger on a composite/missing-PK table still doesn't error
// at Build() time (unlike AFTER, which hard-errors there) -- it just
// leaves PrimaryKey empty, since only a dialect that actually needs it
// for a BEFORE-declared trigger (SQL Server) should surface an error,
// and only that dialect knows it needs one.
func TestTrigger_BeforeUpdate_Set_CompositePrimaryKeyLeavesEmpty(t *testing.T) {
	type compositeKeyModel struct {
		OrgID  string `gorm:"primaryKey"`
		UserID string `gorm:"primaryKey"`
		Status string
	}

	def, err := trigger.BeforeUpdate(&compositeKeyModel{}).
		Set("status", trigger.Raw("'x'")).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v, want no error for a BEFORE trigger on a composite-PK table", err)
	}
	if def.PrimaryKey != "" {
		t.Errorf("PrimaryKey = %q, want empty", def.PrimaryKey)
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

// --- client.Register() ---

func TestRegister_NilDB(t *testing.T) {
	client := dbobjects.NewClient(nil)

	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err == nil {
		t.Fatal("Register() error = nil, want error when the Client has no *gorm.DB")
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

	client := dbobjects.NewClient(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err != nil {
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

	client := dbobjects.NewClient(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Name("trg_users_touch")
	if err := client.Register(context.Background(), tr); err != nil {
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

	client := dbobjects.NewClient(db)
	tr := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) VALUES (OLD.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

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

	client := dbobjects.NewClient(db)
	tr := trigger.AfterInsert(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_insert_audit(user_master_id) VALUES (NEW.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

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

	client := dbobjects.NewClient(db)
	tr := trigger.AfterUpdate(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_update_audit(user_master_id) VALUES (NEW.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

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

	client := dbobjects.NewClient(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err != nil {
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

	client := dbobjects.NewClient(db)
	tr := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) VALUES (OLD.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

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

	client := dbobjects.NewClient(db)
	tr := trigger.AfterInsert(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_insert_audit(user_master_id) VALUES (NEW.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

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

	client := dbobjects.NewClient(db)
	tr := trigger.AfterUpdate(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_update_audit(user_master_id) VALUES (NEW.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

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

	client := dbobjects.NewClient(db)

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

	if err := client.Register(context.Background(), good, bad); err == nil {
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

// --- SQLite ---
//
// SQLite needs no external service (embedded, file-based), so unlike
// Postgres/MySQL these tests don't self-skip on an unreachable DB --
// t.Skipf is kept only for the (should-never-happen) case that opening
// the CGO-backed driver itself fails.

func newSQLiteDSN(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db") + "?_busy_timeout=5000&_journal_mode=WAL"
}

// TestRegister_SQLite_AppliesTrigger is an integration test verifying a
// BeforeUpdate(...).Set(...) trigger fires for real on SQLite, despite
// being translated to a literal AFTER UPDATE trigger under the hood
// (see dialect.go's effectiveTriggerTiming) -- the caller-observable
// behavior (updated_at ends up overridden) must match Postgres/MySQL's
// BeforeUpdate(...).Set(...) even though the underlying SQL mechanism
// differs.
func TestRegister_SQLite_AppliesTrigger(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-sqlite-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

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
		t.Fatalf("UpdatedAt = %v, want a timestamp close to now (trigger should have set CURRENT_TIMESTAMP)", reloaded.UpdatedAt)
	}
}

// TestRegister_SQLite_AppliesDeleteTrigger mirrors the Postgres/MySQL
// AfterDelete(...).Body(...) coverage.
func TestRegister_SQLite_AppliesDeleteTrigger(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_delete_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		deleted_user_id INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}

	client := dbobjects.NewClient(db)
	tr := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) VALUES (OLD.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	user := testutil.UserMaster{Name: "Grace", Email: fmt.Sprintf("grace-sqlite-%d@example.com", time.Now().UnixNano())}
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

// TestRegister_SQLite_AppliesAfterInsertTrigger mirrors the Postgres/
// MySQL AfterInsert(...).Body(...) coverage.
func TestRegister_SQLite_AppliesAfterInsertTrigger(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS user_master_insert_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_master_id INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}

	client := dbobjects.NewClient(db)
	tr := trigger.AfterInsert(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_insert_audit(user_master_id) VALUES (NEW.id);")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	user := testutil.UserMaster{Name: "Alan", Email: fmt.Sprintf("alan-sqlite-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

	var count int64
	if err := db.Raw(`SELECT count(*) FROM user_master_insert_audit WHERE user_master_id = ?`, user.ID).
		Scan(&count).Error; err != nil {
		t.Fatalf("querying audit table: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows for inserted user = %d, want 1 (AFTER INSERT trigger should have fired)", count)
	}
}

// TestRegister_SQLite_DropTriggerLeavesGuardTableForOthers registers two
// independently-named, independently-guarded AfterUpdate(...).Set(...)
// triggers on the same table, drops one, and verifies the other still
// fires -- guards the decision that dropTrigger never touches the
// shared dbobjects_guard table (dialect.go).
func TestRegister_SQLite_DropTriggerLeavesGuardTableForOthers(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	trA := trigger.AfterUpdate(&testutil.UserMaster{}).
		Set("name", trigger.Raw("'touched-by-a'")).
		Name("trg_guard_test_a")
	trB := trigger.AfterUpdate(&testutil.UserMaster{}).
		Set("email", trigger.Raw("'touched-by-b@example.com'")).
		Name("trg_guard_test_b")

	if err := client.Register(context.Background(), trA, trB); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), trB) })

	if err := client.Drop(context.Background(), trA); err != nil {
		t.Fatalf("Drop trA: %v", err)
	}

	user := testutil.UserMaster{Name: "Original", Email: fmt.Sprintf("orig-sqlite-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := db.Model(&user).UpdateColumn("name", "Changed").Error; err != nil {
		t.Fatalf("UpdateColumn: %v", err)
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if reloaded.Name == "touched-by-a" {
		t.Errorf("Name = %q, trA fired but should have been dropped", reloaded.Name)
	}
	if reloaded.Email != "touched-by-b@example.com" {
		t.Errorf("Email = %q, want %q (trB should still fire after trA was dropped, "+
			"proving dropTrigger didn't remove dbobjects_guard out from under it)", reloaded.Email, "touched-by-b@example.com")
	}
}

// guardRecoveryRow backs TestRegister_SQLite_GuardRecoversFromFailedCorrectiveUpdate
// -- a table with a CHECK constraint the trigger's Set() is deliberately
// engineered to violate, and no AutoMigrate involvement (its CREATE
// TABLE is issued directly so the CHECK constraint is exact).
type guardRecoveryRow struct {
	ID     uint `gorm:"primaryKey"`
	Status string
}

func (guardRecoveryRow) TableName() string { return "guard_recovery_rows" }

// TestRegister_SQLite_GuardRecoversFromFailedCorrectiveUpdate is the
// launch-gate test for docs/PLAN.md §3.1b decision #3's open risk:
// whether a failed corrective UPDATE rolls back the dbobjects_guard
// INSERT that already ran in the same trigger firing, or leaves it
// stuck. SQLite's own docs don't settle this (checked directly against
// lang_conflict.html/lang_transaction.html) -- this proves it live.
// If this test fails, the guard-table design itself needs to be
// reconsidered; there's no SQLite-native fallback (no exception-handler
// equivalent) to patch around it with.
func TestRegister_SQLite_GuardRecoversFromFailedCorrectiveUpdate(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.Exec(`CREATE TABLE guard_recovery_rows (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		status TEXT NOT NULL DEFAULT 'ok' CHECK (status != 'bad')
	)`).Error; err != nil {
		t.Fatalf("creating table: %v", err)
	}

	client := dbobjects.NewClient(db)
	// Deliberately broken: the corrective UPDATE this renders always
	// tries to set status = 'bad', which the CHECK constraint always
	// rejects.
	tr := trigger.AfterUpdate(&guardRecoveryRow{}).Set("status", trigger.Raw("'bad'"))
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	row := guardRecoveryRow{Status: "ok"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First UPDATE: fires the AFTER UPDATE trigger; its corrective
	// UPDATE fails the CHECK constraint, on purpose.
	if err := db.Exec(`UPDATE guard_recovery_rows SET status = 'ok' WHERE id = ?`, row.ID).Error; err == nil {
		t.Fatal("expected the corrective UPDATE to fail the CHECK constraint, got nil error")
	}

	var guardCount int64
	if err := db.Raw(`SELECT count(*) FROM dbobjects_guard`).Scan(&guardCount).Error; err != nil {
		t.Fatalf("querying dbobjects_guard: %v", err)
	}
	if guardCount != 0 {
		t.Fatalf("dbobjects_guard has %d stuck row(s) after a failed corrective UPDATE -- "+
			"SQLite's per-statement atomicity did not protect the guard table; "+
			"the guard-table design (docs/PLAN.md §3.1b decision #3) must be re-opened, not patched around", guardCount)
	}

	// Second UPDATE: if the guard were stuck, its WHEN clause would see
	// an existing guard row and skip the trigger body entirely, so this
	// would silently succeed. It must fail again, identically -- proof
	// the trigger actually re-fired, not that it was silently disabled.
	if err := db.Exec(`UPDATE guard_recovery_rows SET status = 'ok' WHERE id = ?`, row.ID).Error; err == nil {
		t.Fatal("expected the trigger to fire again and fail again; a nil error here means the guard is stuck and silently disabling the trigger")
	}
}

// TestRegister_SQLite_RecursionGuardSurvivesConcurrentConnections is the
// other launch-gate test for docs/PLAN.md §3.1b decision #3: proving
// the dbobjects_guard table -- a real, permanent schema object chosen
// specifically because SQLite TEMP tables/PRAGMAs are connection-scoped
// and invisible across a pooled *gorm.DB's separate physical
// connections -- actually works when the trigger fires from several
// distinct connections at once. recursive_triggers defaults OFF in
// SQLite, so this test has to manufacture the scenario the guard exists
// for: PRAGMA recursive_triggers = ON is set explicitly on each
// goroutine's own reserved *sql.Conn (not just db.Exec, which could
// land on any pooled connection) immediately before that same
// connection issues its UPDATE, guaranteeing the pragma is in effect
// for the connection that actually runs the trigger cascade.
func TestRegister_SQLite_RecursionGuardSurvivesConcurrentConnections(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Skipf("skipping integration test, no SQLite available: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	tr := trigger.AfterUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	user := testutil.UserMaster{Name: "Concurrent", Email: fmt.Sprintf("concurrent-sqlite-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			conn, err := sqlDB.Conn(ctx)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: Conn: %w", i, err)
				return
			}
			defer conn.Close()

			// Same physical connection for both statements, so the
			// pragma is guaranteed to be in effect for the UPDATE (and
			// therefore for the trigger cascade it fires) below.
			if _, err := conn.ExecContext(ctx, "PRAGMA recursive_triggers = ON"); err != nil {
				errs <- fmt.Errorf("goroutine %d: PRAGMA: %w", i, err)
				return
			}
			if _, err := conn.ExecContext(ctx, "UPDATE user_master SET name = ? WHERE id = ?",
				fmt.Sprintf("updated-%d", i), user.ID); err != nil {
				errs <- fmt.Errorf("goroutine %d: UPDATE: %w", i, err)
				return
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for concurrent updates -- possible infinite recursion, the guard did not cross the connection pool")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent update failed: %v", err)
		}
	}

	var guardCount int64
	if err := db.Raw(`SELECT count(*) FROM dbobjects_guard`).Scan(&guardCount).Error; err != nil {
		t.Fatalf("querying dbobjects_guard: %v", err)
	}
	if guardCount != 0 {
		t.Errorf("dbobjects_guard has %d leftover row(s) after all concurrent updates settled, want 0", guardCount)
	}
}

// --- SQL Server ---

// TestRegister_SQLServer_AppliesTrigger is an integration test verifying
// a BeforeUpdate(...).Set(...) trigger fires for real on SQL Server,
// despite being translated to a literal AFTER UPDATE trigger under the
// hood (SQL Server has no BEFORE DML trigger at all -- see
// dialect.go's sqlServerDialect.triggerBody) -- the caller-observable
// behavior must match Postgres/MySQL/SQLite's BeforeUpdate(...).Set(...)
// even though the underlying mechanism (a set-based UPDATE ... FROM
// inserted join, not a row-level NEW.col assignment) differs entirely.
// Skips itself if no SQL Server is reachable.
func TestRegister_SQLServer_AppliesTrigger(t *testing.T) {
	db, err := testutil.NewSQLServer()
	if err != nil {
		t.Skipf("skipping integration test, no SQL Server reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-sqlserver-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

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
		t.Fatalf("UpdatedAt = %v, want a timestamp close to now (trigger should have set GETDATE())", reloaded.UpdatedAt)
	}
}

// TestRegister_SQLServer_AppliesDeleteTrigger is an integration test
// verifying an AfterDelete(...).Body(...) trigger fires for real on a
// real DELETE, and specifically exercises the deleted-not-OLD
// portability note: SQL Server has no NEW/OLD at all, so this Body()
// references the deleted pseudo-table by its real name -- a Body()
// string copy-pasted from the Postgres/MySQL/SQLite examples elsewhere
// in this test file (which use OLD.id) would silently fail here.
func TestRegister_SQLServer_AppliesDeleteTrigger(t *testing.T) {
	db, err := testutil.NewSQLServer()
	if err != nil {
		t.Skipf("skipping integration test, no SQL Server reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := db.Exec(`IF NOT EXISTS (SELECT * FROM sys.tables WHERE name = 'user_master_delete_audit')
		CREATE TABLE user_master_delete_audit (
			id INT IDENTITY(1,1) PRIMARY KEY,
			deleted_user_id INT NOT NULL
		)`).Error; err != nil {
		t.Fatalf("creating audit table: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS user_master_delete_audit") })

	client := dbobjects.NewClient(db)
	tr := trigger.AfterDelete(&testutil.UserMaster{}).
		Body("INSERT INTO user_master_delete_audit(deleted_user_id) SELECT id FROM deleted;")
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Grace", Email: fmt.Sprintf("grace-sqlserver-%d@example.com", time.Now().UnixNano())}
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

// TestRegister_SQLServer_RecursionGuardUnderExplicitRecursiveTriggersOn
// proves TRIGGER_NESTLEVEL() > 1 actually stops the recursion it's
// meant to stop, live. RECURSIVE_TRIGGERS defaults OFF in SQL Server
// (verified directly against learn.microsoft.com, docs/PLAN.md §3.1c),
// so this test has to force the scenario the guard exists for by
// explicitly turning it on for the test database, the same
// "manufacture the scenario" reasoning SQLite's equivalent concurrent
// test already uses. Unlike SQLite's guard table, there's no
// dbobjects-owned state here that can leak or get stuck -- this is
// confirming the engine builtin behaves as documented, not settling an
// open risk the way SQLite's two launch-gate tests were.
func TestRegister_SQLServer_RecursionGuardUnderExplicitRecursiveTriggersOn(t *testing.T) {
	db, err := testutil.NewSQLServer()
	if err != nil {
		t.Skipf("skipping integration test, no SQL Server reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	dbName := os.Getenv("MSSQL_DB")
	if err := db.Exec(fmt.Sprintf("ALTER DATABASE %s SET RECURSIVE_TRIGGERS ON", dbName)).Error; err != nil {
		t.Fatalf("ALTER DATABASE ... SET RECURSIVE_TRIGGERS ON: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(fmt.Sprintf("ALTER DATABASE %s SET RECURSIVE_TRIGGERS OFF", dbName))
	})

	client := dbobjects.NewClient(db)
	tr := trigger.AfterUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if err := client.Register(context.Background(), tr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })

	user := testutil.UserMaster{Name: "Recursive", Email: fmt.Sprintf("recursive-sqlserver-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	done := make(chan error, 1)
	go func() {
		done <- db.Model(&user).UpdateColumn("name", "Recursive Updated").Error
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpdateColumn: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out -- possible infinite recursion, TRIGGER_NESTLEVEL guard did not stop it")
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if time.Since(reloaded.UpdatedAt) > 10*time.Second {
		t.Errorf("UpdatedAt = %v, want a timestamp close to now (trigger should still have fired once)", reloaded.UpdatedAt)
	}
}
