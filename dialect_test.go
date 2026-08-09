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

	sql, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
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

	sql, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	if want := "NEW.version = version + 1;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
}
