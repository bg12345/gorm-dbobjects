package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dbobjects "github.com/bg12345/gorm-dbobjects"
	"github.com/bg12345/gorm-dbobjects/internal/testutil"
	"github.com/bg12345/gorm-dbobjects/trigger"
	"github.com/bg12345/gorm-dbobjects/view"
	"gorm.io/gorm"
)

func TestRender_NilDB(t *testing.T) {
	client := dbobjects.NewClient(nil)

	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).Set("updated_at", trigger.Now())
	if _, err := client.Render([]dbobjects.DBObject{tr}, dbobjects.Idempotent); err == nil {
		t.Fatal("Render() error = nil, want error when the Client has no *gorm.DB")
	}
}

// TestRender_Declarative_Postgres_IsValidDDL is an integration test: it
// connects to a real Postgres, renders a trigger in Declarative mode,
// confirms the output contains neither "OR REPLACE" nor "IF EXISTS"
// (the whole point of the mode), and then actually executes each
// returned statement against the live DB -- proving it's syntactically
// valid DDL a real engine accepts, not just a string that happens to
// look right. Skips itself if no Postgres is reachable.
func TestRender_Declarative_Postgres_IsValidDDL(t *testing.T) {
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
		Name("trg_render_declarative_pg_test")

	stmts, err := client.Render([]dbobjects.DBObject{tr}, dbobjects.Declarative)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("Render() returned %d object(s), want 1", len(stmts))
	}
	sql := stmts[0]
	if strings.Contains(sql, "OR REPLACE") || strings.Contains(sql, "IF EXISTS") {
		t.Errorf("Declarative output should contain neither OR REPLACE nor IF EXISTS, got:\n%s", sql)
	}

	// Declarative output is bare CREATE, not idempotent -- drop first so
	// this test can re-run against a DB that already has the object from
	// a previous run.
	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })
	_ = client.Drop(context.Background(), tr)

	for _, stmt := range strings.Split(sql, "\n\n") {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("executing declarative statement failed (not valid DDL):\n%s\nerror: %v", stmt, err)
		}
	}

	var trgCount int64
	if err := db.Raw(`SELECT count(*) FROM pg_trigger WHERE tgname = ?`, "trg_render_declarative_pg_test").
		Scan(&trgCount).Error; err != nil {
		t.Fatalf("querying pg_trigger: %v", err)
	}
	if trgCount != 1 {
		t.Errorf("pg_trigger rows named trg_render_declarative_pg_test = %d, want 1 (declarative DDL should have created it)", trgCount)
	}
}

// TestRender_Declarative_MySQL_IsValidDDL is the MySQL equivalent of
// TestRender_Declarative_Postgres_IsValidDDL. MySQL's declarative output
// is structurally the most different case (a single CREATE TRIGGER
// statement, no backing function, no DROP), so this is the more
// interesting proof of the two. Skips itself if no MySQL is reachable.
func TestRender_Declarative_MySQL_IsValidDDL(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	tr := trigger.BeforeUpdate(&testutil.UserMaster{}).
		Set("updated_at", trigger.Now()).
		Name("trg_render_declarative_mysql_test")

	stmts, err := client.Render([]dbobjects.DBObject{tr}, dbobjects.Declarative)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("Render() returned %d object(s), want 1", len(stmts))
	}
	sql := stmts[0]
	if strings.Contains(sql, "OR REPLACE") || strings.Contains(sql, "IF EXISTS") {
		t.Errorf("Declarative output should contain neither OR REPLACE nor IF EXISTS, got:\n%s", sql)
	}

	t.Cleanup(func() { _ = client.Drop(context.Background(), tr) })
	_ = client.Drop(context.Background(), tr)

	for _, stmt := range strings.Split(sql, "\n\n") {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("executing declarative statement failed (not valid DDL):\n%s\nerror: %v", stmt, err)
		}
	}

	var trgCount int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.triggers WHERE trigger_name = ?`,
		"trg_render_declarative_mysql_test").Scan(&trgCount).Error; err != nil {
		t.Fatalf("querying information_schema.triggers: %v", err)
	}
	if trgCount != 1 {
		t.Errorf("triggers named trg_render_declarative_mysql_test = %d, want 1 (declarative DDL should have created it)", trgCount)
	}
}

// TestRender_Declarative_View_Postgres_IsValidDDL proves Declarative
// mode's bare "CREATE VIEW" (no OR REPLACE) is valid DDL by actually
// creating and querying a view from it, reusing the row-matching
// pattern from view_test.go. Skips itself if no Postgres is reachable.
func TestRender_Declarative_View_Postgres_IsValidDDL(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	v := view.New("v_render_declarative_pg_test").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "RenderDeclarativeMatch")
		})

	stmts, err := client.Render([]dbobjects.DBObject{v}, dbobjects.Declarative)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("Render() returned %d object(s), want 1", len(stmts))
	}
	if strings.Contains(stmts[0], "OR REPLACE") {
		t.Errorf("Declarative view output should not contain OR REPLACE, got:\n%s", stmts[0])
	}

	t.Cleanup(func() { _ = client.Drop(context.Background(), v) })
	_ = client.Drop(context.Background(), v)

	if err := db.Exec(stmts[0]).Error; err != nil {
		t.Fatalf("executing declarative CREATE VIEW failed (not valid DDL): %v", err)
	}

	match := testutil.UserMaster{Name: "RenderDeclarativeMatch", Email: fmt.Sprintf("rd-match-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&match) })

	var rows []testutil.UserMaster
	if err := db.Table("v_render_declarative_pg_test").Find(&rows).Error; err != nil {
		t.Fatalf("querying view: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("view returned %d row(s), want 1: %+v", len(rows), rows)
	}
}

// TestRender_Declarative_View_MySQL_IsValidDDL is the MySQL equivalent
// of TestRender_Declarative_View_Postgres_IsValidDDL. Skips itself if no
// MySQL is reachable.
func TestRender_Declarative_View_MySQL_IsValidDDL(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}

	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	v := view.New("v_render_declarative_mysql_test").
		Query(func(tx *gorm.DB) *gorm.DB {
			return tx.Model(&testutil.UserMaster{}).Where("name = ?", "RenderDeclarativeMatchMySQL")
		})

	stmts, err := client.Render([]dbobjects.DBObject{v}, dbobjects.Declarative)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("Render() returned %d object(s), want 1", len(stmts))
	}
	if strings.Contains(stmts[0], "OR REPLACE") {
		t.Errorf("Declarative view output should not contain OR REPLACE, got:\n%s", stmts[0])
	}

	t.Cleanup(func() { _ = client.Drop(context.Background(), v) })
	_ = client.Drop(context.Background(), v)

	if err := db.Exec(stmts[0]).Error; err != nil {
		t.Fatalf("executing declarative CREATE VIEW failed (not valid DDL): %v", err)
	}

	match := testutil.UserMaster{Name: "RenderDeclarativeMatchMySQL", Email: fmt.Sprintf("rd-match-mysql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&match).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&match) })

	var rows []testutil.UserMaster
	if err := db.Table("v_render_declarative_mysql_test").Find(&rows).Error; err != nil {
		t.Fatalf("querying view: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("view returned %d row(s), want 1: %+v", len(rows), rows)
	}
}
