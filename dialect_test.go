package dbobjects

import (
	"strings"
	"testing"

	"github.com/bg12345/gorm-dbobjects/trigger"
)

// postgresDialect.renderTrigger/renderExpr are unexported, so this test
// lives inside package dbobjects (white-box) rather than in tests/,
// which only sees the public API. Fills the gap flagged in
// docs/PLAN.md §3.5/§3.7: nothing previously asserted the exact SQL a
// dialect produces, so a dialect leak like the old hardcoded NOW() in
// trigger.Expr could regress silently.

func TestPostgresDialect_RenderExpr(t *testing.T) {
	d := postgresDialect{}
	tests := []struct {
		name string
		expr trigger.Expr
		want string
	}{
		{"Now", trigger.Now(), "NOW()"},
		{"Raw", trigger.Raw("version + 1"), "version + 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.renderExpr(tt.expr); got != tt.want {
				t.Errorf("renderExpr(%+v) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestPostgresDialect_RenderTrigger_NowEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:  "user_master",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"updated_at": trigger.Now()},
	}

	stmts, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "NEW.updated_at = NOW();"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
}

func TestPostgresDialect_RenderTrigger_RawEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"version": trigger.Raw("version + 1")},
	}

	stmts, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "NEW.version = version + 1;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
}

func TestMySQLDialect_RenderExpr(t *testing.T) {
	d := mysqlDialect{}
	tests := []struct {
		name string
		expr trigger.Expr
		want string
	}{
		{"Now", trigger.Now(), "NOW()"},
		{"Raw", trigger.Raw("version + 1"), "version + 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.renderExpr(tt.expr); got != tt.want {
				t.Errorf("renderExpr(%+v) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestMySQLDialect_RenderTrigger_NowEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:  "user_master",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"updated_at": trigger.Now()},
	}

	stmts, err := mysqlDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	// MySQL trigger bodies require the SET keyword for NEW.col
	// assignment, unlike PL/pgSQL where a bare "NEW.col = expr;" is
	// valid on its own -- this is the regression guard for that bug.
	if want := "SET NEW.updated_at = NOW();"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
}

func TestMySQLDialect_RenderTrigger_RawEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"version": trigger.Raw("version + 1")},
	}

	stmts, err := mysqlDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "SET NEW.version = version + 1;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
}

// MySQL has no separate trigger function the way Postgres does, so
// renderTrigger should produce exactly two top-level statements (DROP
// TRIGGER, CREATE TRIGGER) and never reference a function -- guards
// against that shape regressing back toward Postgres's, e.g. via
// careless copy-paste between the two dialects' methods.
func TestMySQLDialect_RenderTrigger_StatementShape(t *testing.T) {
	def := &trigger.Definition{
		Table:  "user_master",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"updated_at": trigger.Now()},
	}

	stmts, err := mysqlDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("renderTrigger() returned %d statement(s), want 2 (DROP TRIGGER, CREATE TRIGGER):\n%s",
			len(stmts), strings.Join(stmts, "\n---\n"))
	}
	for _, s := range stmts {
		if strings.Contains(s, "fn_") || strings.Contains(s, "FUNCTION") {
			t.Errorf("renderTrigger() statement references a function, but MySQL triggers have no backing function:\n%s", s)
		}
	}
}

func TestMySQLDialect_DropTrigger_StatementShape(t *testing.T) {
	def := &trigger.Definition{
		Table:  "user_master",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"updated_at": trigger.Now()},
	}

	stmts, err := mysqlDialect{}.dropTrigger(def)
	if err != nil {
		t.Fatalf("dropTrigger() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropTrigger() returned %d statement(s), want 1 (no function to drop on MySQL):\n%s",
			len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if want := "DROP TRIGGER IF EXISTS trg_user_master_before_update;"; stmts[0] != want {
		t.Errorf("dropTrigger()[0] = %q, want %q", stmts[0], want)
	}
}
