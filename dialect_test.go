package dbobjects

import (
	"strings"
	"testing"

	"github.com/bg12345/gorm-dbobjects/procedure"
	"github.com/bg12345/gorm-dbobjects/trigger"
	"github.com/bg12345/gorm-dbobjects/view"
)

// postgresDialect.renderTrigger/renderExpr are unexported, so this test
// lives inside package dbobjects (white-box) rather than in tests/,
// which only sees the public API. Fills a real gap: nothing previously
// asserted the exact SQL a dialect produces, so a dialect leak like the
// old hardcoded NOW() in trigger.Expr could regress silently.

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

// AFTER triggers can't assign to NEW (the row's already written), so
// Set() on an AFTER trigger renders as a follow-up UPDATE targeting the
// row via its primary key instead of NEW.col = expr. These four tests
// guard that shape plus its two recursion guards -- Postgres's
// pg_trigger_depth() and MySQL's session-variable-plus-EXIT-HANDLER --
// since a rendering regression here would silently reintroduce the
// infinite-recursion bug the guards exist to prevent.

func TestPostgresDialect_RenderTrigger_AfterUpdateSetEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
	}

	stmts, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	for _, want := range []string{
		"IF pg_trigger_depth() > 1 THEN RETURN NULL; END IF;",
		"UPDATE accounts SET status = 'synced' WHERE id = NEW.id;",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "NEW.status = ") {
		t.Errorf("renderTrigger() should not assign NEW.status directly on an AFTER trigger, got:\n%s", sql)
	}
}

func TestPostgresDialect_RenderTrigger_AfterInsertSetEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "INSERT",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'new'")},
		PrimaryKey: "id",
	}

	stmts, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "UPDATE accounts SET status = 'new' WHERE id = NEW.id;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
	// AFTER INSERT can't recurse into itself via this UPDATE (it's not
	// another INSERT), so no pg_trigger_depth() guard is expected here.
	if strings.Contains(sql, "pg_trigger_depth") {
		t.Errorf("renderTrigger() should not need a recursion guard on AFTER INSERT, got:\n%s", sql)
	}
}

func TestMySQLDialect_RenderTrigger_AfterUpdateSetEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
		Name:       "trg_accounts_after_update",
	}

	stmts, err := mysqlDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	for _, want := range []string{
		"@dbobjects_guard_trg_accounts_after_update IS NULL",
		"DECLARE EXIT HANDLER FOR SQLEXCEPTION",
		"RESIGNAL;",
		"UPDATE accounts SET status = 'synced' WHERE id = NEW.id;",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
		}
	}
}

func TestMySQLDialect_RenderTrigger_AfterInsertSetEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "INSERT",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'new'")},
		PrimaryKey: "id",
	}

	stmts, err := mysqlDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "UPDATE accounts SET status = 'new' WHERE id = NEW.id;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
	if strings.Contains(sql, "DECLARE EXIT HANDLER") {
		t.Errorf("renderTrigger() should not need the recursion-guard handler on AFTER INSERT, got:\n%s", sql)
	}
}

// renderTriggerDeclarative shares triggerBody/createTriggerStmt with
// renderTrigger (dialect.go), so these two tests exist specifically to
// catch that extraction ever drifting -- both variants must render
// identical body content (including the recursion guards above) for the
// same Definition, differing only in the outer CREATE-OR-REPLACE-vs-bare
// and DROP-vs-no-DROP wrapper.

func TestPostgresDialect_RenderTriggerDeclarative_StatementShape(t *testing.T) {
	def := &trigger.Definition{
		Table:  "user_master",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"updated_at": trigger.Now()},
	}

	stmts, err := postgresDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("renderTriggerDeclarative() returned %d statement(s), want 2 (CREATE FUNCTION, CREATE TRIGGER):\n%s",
			len(stmts), strings.Join(stmts, "\n---\n"))
	}
	sql := strings.Join(stmts, "\n")
	if strings.Contains(sql, "OR REPLACE") {
		t.Errorf("renderTriggerDeclarative() output should not contain OR REPLACE, got:\n%s", sql)
	}
	if strings.Contains(sql, "DROP") {
		t.Errorf("renderTriggerDeclarative() output should not contain a DROP statement, got:\n%s", sql)
	}
	if want := "CREATE FUNCTION"; !strings.Contains(sql, want) {
		t.Errorf("renderTriggerDeclarative() output missing %q, got:\n%s", want, sql)
	}
}

func TestPostgresDialect_RenderTriggerDeclarative_BodyMatchesIdempotent(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
	}

	idempotent, err := postgresDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	declarative, err := postgresDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}

	idempotentSQL := strings.Join(idempotent, "\n")
	declarativeSQL := strings.Join(declarative, "\n")
	for _, want := range []string{
		"IF pg_trigger_depth() > 1 THEN RETURN NULL; END IF;",
		"UPDATE accounts SET status = 'synced' WHERE id = NEW.id;",
	} {
		if !strings.Contains(idempotentSQL, want) {
			t.Fatalf("test setup: idempotent output missing %q, got:\n%s", want, idempotentSQL)
		}
		if !strings.Contains(declarativeSQL, want) {
			t.Errorf("renderTriggerDeclarative() body diverged from renderTrigger(): missing %q, got:\n%s", want, declarativeSQL)
		}
	}
}

func TestMySQLDialect_RenderTriggerDeclarative_StatementShape(t *testing.T) {
	def := &trigger.Definition{
		Table:  "user_master",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"updated_at": trigger.Now()},
	}

	stmts, err := mysqlDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderTriggerDeclarative() returned %d statement(s), want 1 (CREATE TRIGGER only, no DROP):\n%s",
			len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if strings.Contains(stmts[0], "DROP") {
		t.Errorf("renderTriggerDeclarative() output should not contain a DROP statement, got:\n%s", stmts[0])
	}
	if strings.Contains(stmts[0], "fn_") || strings.Contains(stmts[0], "FUNCTION") {
		t.Errorf("renderTriggerDeclarative() statement references a function, but MySQL triggers have no backing function:\n%s", stmts[0])
	}
}

func TestMySQLDialect_RenderTriggerDeclarative_BodyMatchesIdempotent(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
		Name:       "trg_accounts_after_update",
	}

	idempotent, err := mysqlDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	declarative, err := mysqlDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}

	idempotentSQL := strings.Join(idempotent, "\n")
	declarativeSQL := strings.Join(declarative, "\n")
	for _, want := range []string{
		"@dbobjects_guard_trg_accounts_after_update IS NULL",
		"DECLARE EXIT HANDLER FOR SQLEXCEPTION",
		"RESIGNAL;",
		"UPDATE accounts SET status = 'synced' WHERE id = NEW.id;",
	} {
		if !strings.Contains(idempotentSQL, want) {
			t.Fatalf("test setup: idempotent output missing %q, got:\n%s", want, idempotentSQL)
		}
		if !strings.Contains(declarativeSQL, want) {
			t.Errorf("renderTriggerDeclarative() body diverged from renderTrigger(): missing %q, got:\n%s", want, declarativeSQL)
		}
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

// The Raw() path bypasses db.ToSQL entirely (renderViewCreateOrReplace
// only calls db.ToSQL when RawSQL is empty), so these can pass a nil
// *gorm.DB safely and stay fully DB-less -- the Query()-callback path
// genuinely needs a live connection and is covered by integration
// tests instead.

func TestPostgresDialect_RenderView_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := postgresDialect{}.renderView(nil, def)
	if err != nil {
		t.Fatalf("renderView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE OR REPLACE VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderView()[0] = %q, want %q", stmts[0], want)
	}
}

func TestMySQLDialect_RenderView_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := mysqlDialect{}.renderView(nil, def)
	if err != nil {
		t.Fatalf("renderView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE OR REPLACE VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderView()[0] = %q, want %q", stmts[0], want)
	}
}

func TestPostgresDialect_RenderViewDeclarative_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := postgresDialect{}.renderViewDeclarative(nil, def)
	if err != nil {
		t.Fatalf("renderViewDeclarative() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderViewDeclarative() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderViewDeclarative()[0] = %q, want %q", stmts[0], want)
	}
}

func TestMySQLDialect_RenderViewDeclarative_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := mysqlDialect{}.renderViewDeclarative(nil, def)
	if err != nil {
		t.Fatalf("renderViewDeclarative() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderViewDeclarative() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderViewDeclarative()[0] = %q, want %q", stmts[0], want)
	}
}

func TestPostgresDialect_DropView(t *testing.T) {
	def := &view.Definition{Name: "active_users"}

	stmts, err := postgresDialect{}.dropView(def)
	if err != nil {
		t.Fatalf("dropView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if want := "DROP VIEW IF EXISTS active_users;"; stmts[0] != want {
		t.Errorf("dropView()[0] = %q, want %q", stmts[0], want)
	}
}

func TestMySQLDialect_DropView(t *testing.T) {
	def := &view.Definition{Name: "active_users"}

	stmts, err := mysqlDialect{}.dropView(def)
	if err != nil {
		t.Fatalf("dropView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if want := "DROP VIEW IF EXISTS active_users;"; stmts[0] != want {
		t.Errorf("dropView()[0] = %q, want %q", stmts[0], want)
	}
}

// TestRenderView_SharedAcrossDialects guards the "share it" decision:
// postgresDialect and mysqlDialect both delegate to
// renderViewCreateOrReplace, so their output for the same Definition
// should be byte-for-byte identical. Catches one dialect's renderView
// accidentally diverging from the shared helper without the other
// following, which would otherwise go unnoticed since the two are
// tested independently above.
func TestRenderView_SharedAcrossDialects(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	pgStmts, err := postgresDialect{}.renderView(nil, def)
	if err != nil {
		t.Fatalf("postgresDialect renderView() error = %v", err)
	}
	myStmts, err := mysqlDialect{}.renderView(nil, def)
	if err != nil {
		t.Fatalf("mysqlDialect renderView() error = %v", err)
	}
	if strings.Join(pgStmts, "\n") != strings.Join(myStmts, "\n") {
		t.Errorf("postgresDialect and mysqlDialect produced different DDL for the same Definition:\npostgres: %v\nmysql:    %v",
			pgStmts, myStmts)
	}
}

// TestRenderViewDeclarative_SharedAcrossDialects mirrors
// TestRenderView_SharedAcrossDialects for the declarative path: both
// dialects delegate renderViewDeclarative to the same renderViewCreate
// helper, so their output for the same Definition should also be
// byte-for-byte identical.
func TestRenderViewDeclarative_SharedAcrossDialects(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	pgStmts, err := postgresDialect{}.renderViewDeclarative(nil, def)
	if err != nil {
		t.Fatalf("postgresDialect renderViewDeclarative() error = %v", err)
	}
	myStmts, err := mysqlDialect{}.renderViewDeclarative(nil, def)
	if err != nil {
		t.Fatalf("mysqlDialect renderViewDeclarative() error = %v", err)
	}
	if strings.Join(pgStmts, "\n") != strings.Join(myStmts, "\n") {
		t.Errorf("postgresDialect and mysqlDialect produced different declarative DDL for the same Definition:\npostgres: %v\nmysql:    %v",
			pgStmts, myStmts)
	}
}

// SQLite trigger bodies can't assign to NEW/OLD on any timing (not just
// AFTER like Postgres/MySQL), so Set()-based triggers always render as
// AFTER with a rowid-targeted follow-up UPDATE, regardless of the
// caller's declared Timing. These tests guard that translation, the
// rowid-not-PrimaryKey targeting, and the WHEN-clause recursion guard
// (SQLite trigger bodies have no IF/procedural control flow at all, so
// the guard can't be inline the way Postgres's/MySQL's are).

func TestSQLiteDialect_RenderExpr(t *testing.T) {
	d := sqliteDialect{}
	tests := []struct {
		name string
		expr trigger.Expr
		want string
	}{
		{"Now", trigger.Now(), "CURRENT_TIMESTAMP"},
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

func TestSQLiteDialect_RenderTrigger_BeforeUpdateSet_RendersAsAfter(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
	}

	stmts, err := sqliteDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if !strings.Contains(sql, "AFTER UPDATE") {
		t.Errorf("renderTrigger() output missing \"AFTER UPDATE\", got:\n%s", sql)
	}
	if strings.Contains(sql, "BEFORE UPDATE") {
		t.Errorf("renderTrigger() should not contain a literal BEFORE UPDATE, got:\n%s", sql)
	}
}

func TestSQLiteDialect_RenderTrigger_BodyKeepsDeclaredTiming(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "BEFORE",
		Event:  "UPDATE",
		Body:   "SELECT RAISE(ABORT, 'bad status') WHERE NEW.status = 'invalid';",
	}

	stmts, err := sqliteDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if !strings.Contains(sql, "BEFORE UPDATE") {
		t.Errorf("renderTrigger() with Body() should keep the declared BEFORE timing, got:\n%s", sql)
	}
}

func TestSQLiteDialect_RenderTrigger_UsesRowidNotPrimaryKey(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "BEFORE",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id", // set to prove it's ignored, not just absent
	}

	stmts, err := sqliteDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "WHERE rowid = NEW.rowid;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
	if strings.Contains(sql, "WHERE id =") {
		t.Errorf("renderTrigger() should never target Definition.PrimaryKey on SQLite, got:\n%s", sql)
	}
}

func TestSQLiteDialect_RenderTrigger_AfterUpdateSet_HasGuard(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
	}

	stmts, err := sqliteDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS dbobjects_guard (name TEXT PRIMARY KEY);",
		"WHEN NOT EXISTS (SELECT 1 FROM dbobjects_guard WHERE name = 'trg_accounts_after_update')",
		"INSERT INTO dbobjects_guard(name) VALUES ('trg_accounts_after_update');",
		"DELETE FROM dbobjects_guard WHERE name = 'trg_accounts_after_update';",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
		}
	}
}

func TestSQLiteDialect_RenderTrigger_AfterInsertSet_NoGuard(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "INSERT",
		Sets:   map[string]trigger.Expr{"status": trigger.Raw("'new'")},
	}

	stmts, err := sqliteDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if strings.Contains(sql, "dbobjects_guard") {
		t.Errorf("renderTrigger() should not need a recursion guard on AFTER INSERT, got:\n%s", sql)
	}
	if want := "WHERE rowid = NEW.rowid;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
}

func TestSQLiteDialect_RenderTriggerDeclarative_BodyMatchesIdempotent(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
	}

	idempotent, err := sqliteDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	declarative, err := sqliteDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}

	idempotentSQL := strings.Join(idempotent, "\n")
	declarativeSQL := strings.Join(declarative, "\n")
	if strings.Contains(declarativeSQL, "DROP") {
		t.Errorf("renderTriggerDeclarative() output should not contain DROP, got:\n%s", declarativeSQL)
	}
	for _, want := range []string{
		"WHEN NOT EXISTS (SELECT 1 FROM dbobjects_guard WHERE name = 'trg_accounts_after_update')",
		"WHERE rowid = NEW.rowid;",
	} {
		if !strings.Contains(idempotentSQL, want) {
			t.Fatalf("test setup: idempotent output missing %q, got:\n%s", want, idempotentSQL)
		}
		if !strings.Contains(declarativeSQL, want) {
			t.Errorf("renderTriggerDeclarative() body diverged from renderTrigger(): missing %q, got:\n%s", want, declarativeSQL)
		}
	}
}

func TestSQLiteDialect_DropTrigger_DoesNotTouchGuardTable(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
	}

	stmts, err := sqliteDialect{}.dropTrigger(def)
	if err != nil {
		t.Fatalf("dropTrigger() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropTrigger() returned %d statement(s), want 1", len(stmts))
	}
	if strings.Contains(stmts[0], "dbobjects_guard") {
		t.Errorf("dropTrigger() should never reference dbobjects_guard, got:\n%s", stmts[0])
	}
	if want := "DROP TRIGGER IF EXISTS trg_accounts_after_update;"; stmts[0] != want {
		t.Errorf("dropTrigger()[0] = %q, want %q", stmts[0], want)
	}
}

func TestSQLiteDialect_RenderView_ComposesDropAndCreate(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	got, err := sqliteDialect{}.renderView(nil, def)
	if err != nil {
		t.Fatalf("renderView() error = %v", err)
	}

	drop, _ := dropViewIfExists(def)
	create, err := renderViewCreate(nil, def)
	if err != nil {
		t.Fatalf("renderViewCreate() error = %v", err)
	}
	want := append(drop, create...)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("renderView() = %v, want %v (drop+create composition)", got, want)
	}
}

func TestSQLiteDialect_RenderViewDeclarative_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := sqliteDialect{}.renderViewDeclarative(nil, def)
	if err != nil {
		t.Fatalf("renderViewDeclarative() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderViewDeclarative() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderViewDeclarative()[0] = %q, want %q", stmts[0], want)
	}
}

func TestSQLiteDialect_DropView(t *testing.T) {
	def := &view.Definition{Name: "active_users"}

	stmts, err := sqliteDialect{}.dropView(def)
	if err != nil {
		t.Fatalf("dropView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if want := "DROP VIEW IF EXISTS active_users;"; stmts[0] != want {
		t.Errorf("dropView()[0] = %q, want %q", stmts[0], want)
	}
}

// SQL Server has no BEFORE DML trigger at all (only AFTER/INSTEAD OF),
// and its triggers are set-based (inserted/deleted can hold zero, one,
// or many rows per firing, not a single NEW/OLD row alias) -- so Set()
// always renders AFTER with a join-update against Definition.PrimaryKey
// (SQL Server has no rowid-style fallback the way SQLite does), and
// Body() on a BEFORE-declared trigger is a hard error rather than a
// silent AFTER coercion, since SQL Server has no mechanism that makes
// that translation safe the way Set()'s forced translation is.

func TestSQLServerDialect_RenderExpr(t *testing.T) {
	d := sqlServerDialect{}
	tests := []struct {
		name string
		expr trigger.Expr
		want string
	}{
		{"Now", trigger.Now(), "GETDATE()"},
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

func TestSQLServerDialect_RenderTrigger_BeforeUpdateSet_RendersAsAfter(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "BEFORE",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
	}

	stmts, err := sqlServerDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if !strings.Contains(sql, "AFTER UPDATE") {
		t.Errorf("renderTrigger() output missing \"AFTER UPDATE\", got:\n%s", sql)
	}
	if strings.Contains(sql, "BEFORE") {
		t.Errorf("renderTrigger() should not contain a literal BEFORE, got:\n%s", sql)
	}
}

func TestSQLServerDialect_RenderTrigger_AfterUpdateSetEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
	}

	stmts, err := sqlServerDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	for _, want := range []string{
		"IF TRIGGER_NESTLEVEL() > 1 RETURN;",
		"UPDATE t SET status = 'synced' FROM accounts AS t INNER JOIN inserted AS i ON t.id = i.id;",
		"CREATE OR ALTER TRIGGER",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
		}
	}
}

func TestSQLServerDialect_RenderTrigger_AfterInsertSetEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "INSERT",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'new'")},
		PrimaryKey: "id",
	}

	stmts, err := sqlServerDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "UPDATE t SET status = 'new' FROM accounts AS t INNER JOIN inserted AS i ON t.id = i.id;"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
	if strings.Contains(sql, "TRIGGER_NESTLEVEL") {
		t.Errorf("renderTrigger() should not need a recursion guard on AFTER INSERT, got:\n%s", sql)
	}
}

func TestSQLServerDialect_RenderTrigger_Set_NoPrimaryKey_Errors(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "UPDATE",
		Sets:   map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		// PrimaryKey deliberately left empty -- composite or missing PK.
	}

	_, err := sqlServerDialect{}.renderTrigger(def)
	if err == nil {
		t.Fatal("renderTrigger() error = nil, want error for Set() with no resolvable primary key")
	}
}

func TestSQLServerDialect_RenderTrigger_BeforeInsertBody_Errors(t *testing.T) {
	def := &trigger.Definition{Table: "accounts", Timing: "BEFORE", Event: "INSERT", Body: "SELECT 1;"}
	_, err := sqlServerDialect{}.renderTrigger(def)
	if err == nil {
		t.Fatal("renderTrigger() error = nil, want error for Body() on a BEFORE INSERT trigger")
	}
}

func TestSQLServerDialect_RenderTrigger_BeforeUpdateBody_Errors(t *testing.T) {
	def := &trigger.Definition{Table: "accounts", Timing: "BEFORE", Event: "UPDATE", Body: "SELECT 1;"}
	_, err := sqlServerDialect{}.renderTrigger(def)
	if err == nil {
		t.Fatal("renderTrigger() error = nil, want error for Body() on a BEFORE UPDATE trigger")
	}
}

func TestSQLServerDialect_RenderTrigger_BeforeDeleteBody_Errors(t *testing.T) {
	def := &trigger.Definition{Table: "accounts", Timing: "BEFORE", Event: "DELETE", Body: "SELECT 1;"}
	_, err := sqlServerDialect{}.renderTrigger(def)
	if err == nil {
		t.Fatal("renderTrigger() error = nil, want error for Body() on a BEFORE DELETE trigger")
	}
}

func TestSQLServerDialect_RenderTrigger_AfterDeleteBodyEndToEnd(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "DELETE",
		Body:   "INSERT INTO audit(id) VALUES ((SELECT id FROM deleted));",
	}

	stmts, err := sqlServerDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	sql := strings.Join(stmts, "\n")
	if want := "INSERT INTO audit(id) VALUES ((SELECT id FROM deleted));"; !strings.Contains(sql, want) {
		t.Errorf("renderTrigger() output missing %q, got:\n%s", want, sql)
	}
	if !strings.Contains(sql, "AFTER DELETE") {
		t.Errorf("renderTrigger() output missing \"AFTER DELETE\", got:\n%s", sql)
	}
}

func TestSQLServerDialect_RenderTriggerDeclarative_StatementShape(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
	}

	stmts, err := sqlServerDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderTriggerDeclarative() returned %d statement(s), want 1 (no DROP, no OR ALTER):\n%s",
			len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if strings.Contains(stmts[0], "OR ALTER") || strings.Contains(stmts[0], "DROP") {
		t.Errorf("renderTriggerDeclarative() output should contain neither OR ALTER nor DROP, got:\n%s", stmts[0])
	}
}

func TestSQLServerDialect_RenderTriggerDeclarative_BodyMatchesIdempotent(t *testing.T) {
	def := &trigger.Definition{
		Table:      "accounts",
		Timing:     "AFTER",
		Event:      "UPDATE",
		Sets:       map[string]trigger.Expr{"status": trigger.Raw("'synced'")},
		PrimaryKey: "id",
	}

	idempotent, err := sqlServerDialect{}.renderTrigger(def)
	if err != nil {
		t.Fatalf("renderTrigger() error = %v", err)
	}
	declarative, err := sqlServerDialect{}.renderTriggerDeclarative(def)
	if err != nil {
		t.Fatalf("renderTriggerDeclarative() error = %v", err)
	}

	idempotentSQL := strings.Join(idempotent, "\n")
	declarativeSQL := strings.Join(declarative, "\n")
	for _, want := range []string{
		"IF TRIGGER_NESTLEVEL() > 1 RETURN;",
		"UPDATE t SET status = 'synced' FROM accounts AS t INNER JOIN inserted AS i ON t.id = i.id;",
	} {
		if !strings.Contains(idempotentSQL, want) {
			t.Fatalf("test setup: idempotent output missing %q, got:\n%s", want, idempotentSQL)
		}
		if !strings.Contains(declarativeSQL, want) {
			t.Errorf("renderTriggerDeclarative() body diverged from renderTrigger(): missing %q, got:\n%s", want, declarativeSQL)
		}
	}
}

func TestSQLServerDialect_DropTrigger_StatementShape(t *testing.T) {
	def := &trigger.Definition{
		Table:  "accounts",
		Timing: "AFTER",
		Event:  "UPDATE",
		Body:   "SELECT 1;",
	}

	stmts, err := sqlServerDialect{}.dropTrigger(def)
	if err != nil {
		t.Fatalf("dropTrigger() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropTrigger() returned %d statement(s), want 1", len(stmts))
	}
	if want := "DROP TRIGGER IF EXISTS trg_accounts_after_update;"; stmts[0] != want {
		t.Errorf("dropTrigger()[0] = %q, want %q", stmts[0], want)
	}
}

func TestSQLServerDialect_RenderView_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := sqlServerDialect{}.renderView(nil, def)
	if err != nil {
		t.Fatalf("renderView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE OR ALTER VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderView()[0] = %q, want %q", stmts[0], want)
	}
}

func TestSQLServerDialect_RenderViewDeclarative_Raw(t *testing.T) {
	def := &view.Definition{
		Name:   "active_users",
		RawSQL: "SELECT * FROM user_master WHERE deleted_at IS NULL",
	}

	stmts, err := sqlServerDialect{}.renderViewDeclarative(nil, def)
	if err != nil {
		t.Fatalf("renderViewDeclarative() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("renderViewDeclarative() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	want := `CREATE VIEW active_users AS SELECT * FROM user_master WHERE deleted_at IS NULL`
	if stmts[0] != want {
		t.Errorf("renderViewDeclarative()[0] = %q, want %q", stmts[0], want)
	}
}

func TestSQLServerDialect_DropView(t *testing.T) {
	def := &view.Definition{Name: "active_users"}

	stmts, err := sqlServerDialect{}.dropView(def)
	if err != nil {
		t.Fatalf("dropView() error = %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("dropView() returned %d statement(s), want 1:\n%s", len(stmts), strings.Join(stmts, "\n---\n"))
	}
	if want := "DROP VIEW IF EXISTS active_users;"; stmts[0] != want {
		t.Errorf("dropView()[0] = %q, want %q", stmts[0], want)
	}
}

// --- procedure ---

func TestPostgresDialect_ParamType(t *testing.T) {
	d := postgresDialect{}
	tests := []struct {
		name string
		in   procedure.ParamType
		want string
	}{
		{"Int", procedure.Int, "INT"},
		{"Text", procedure.Text, "TEXT"},
		{"Bool", procedure.Bool, "BOOLEAN"},
		{"Time", procedure.Time, "TIMESTAMP"},
		{"Varchar", procedure.Varchar(50), "VARCHAR(50)"},
		{"Char", procedure.Char(10), "CHAR(10)"},
		{"Decimal", procedure.Decimal(10, 2), "DECIMAL(10,2)"},
		{"Float", procedure.Float, "DOUBLE PRECISION"},
		{"Bytes", procedure.Bytes, "BYTEA"},
		{"Raw", procedure.Raw("JSONB"), "JSONB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.paramType(tt.in)
			if err != nil {
				t.Fatalf("paramType(%+v) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("paramType(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPostgresDialect_ParamType_InvalidSize_Errors(t *testing.T) {
	d := postgresDialect{}
	tests := []procedure.ParamType{procedure.Varchar(0), procedure.Char(-1), procedure.Decimal(0, 0), procedure.Decimal(5, 6)}
	for _, in := range tests {
		if _, err := d.paramType(in); err == nil {
			t.Errorf("paramType(%+v) error = nil, want error", in)
		}
	}
}

func TestPostgresDialect_RenderProcedure_StatementShape(t *testing.T) {
	def := &procedure.Definition{
		Name:   "recalc_balances",
		Params: []procedure.Param{{Name: "user_id", Type: procedure.Int}},
		Body:   "UPDATE accounts SET balance = balance + 1 WHERE id = user_id;",
	}

	stmts, err := postgresDialect{}.renderProcedure(def)
	if err != nil {
		t.Fatalf("renderProcedure() error = %v", err)
	}
	want := []string{`CREATE OR REPLACE PROCEDURE recalc_balances(user_id INT) LANGUAGE plpgsql AS $$
BEGIN
  UPDATE accounts SET balance = balance + 1 WHERE id = user_id;
END;
$$;`}
	if !slicesEqual(stmts, want) {
		t.Errorf("renderProcedure() = %q, want %q", stmts, want)
	}
}

func TestPostgresDialect_RenderProcedureDeclarative_BodyMatchesIdempotent(t *testing.T) {
	def := &procedure.Definition{
		Name:   "recalc_balances",
		Params: []procedure.Param{{Name: "user_id", Type: procedure.Int}},
		Body:   "UPDATE accounts SET balance = balance + 1 WHERE id = user_id;",
	}

	idempotent, err := postgresDialect{}.renderProcedure(def)
	if err != nil {
		t.Fatalf("renderProcedure() error = %v", err)
	}
	declarative, err := postgresDialect{}.renderProcedureDeclarative(def)
	if err != nil {
		t.Fatalf("renderProcedureDeclarative() error = %v", err)
	}
	if strings.Contains(declarative[0], "OR REPLACE") {
		t.Errorf("renderProcedureDeclarative() should not contain OR REPLACE, got:\n%s", declarative[0])
	}
	for _, want := range []string{"user_id INT", "UPDATE accounts SET balance = balance + 1 WHERE id = user_id;"} {
		if !strings.Contains(idempotent[0], want) {
			t.Fatalf("test setup: renderProcedure() missing %q, got:\n%s", want, idempotent[0])
		}
		if !strings.Contains(declarative[0], want) {
			t.Errorf("renderProcedureDeclarative() diverged from renderProcedure(): missing %q, got:\n%s", want, declarative[0])
		}
	}
}

func TestPostgresDialect_DropProcedure_StatementShape(t *testing.T) {
	def := &procedure.Definition{Name: "recalc_balances"}
	stmts, err := postgresDialect{}.dropProcedure(def)
	if err != nil {
		t.Fatalf("dropProcedure() error = %v", err)
	}
	if want := "DROP PROCEDURE IF EXISTS recalc_balances;"; len(stmts) != 1 || stmts[0] != want {
		t.Errorf("dropProcedure() = %q, want [%q]", stmts, want)
	}
}

func TestMySQLDialect_ParamType(t *testing.T) {
	d := mysqlDialect{}
	tests := []struct {
		name string
		in   procedure.ParamType
		want string
	}{
		{"Time", procedure.Time, "DATETIME"},
		{"Float", procedure.Float, "DOUBLE"},
		{"Bytes", procedure.Bytes, "BLOB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.paramType(tt.in)
			if err != nil {
				t.Fatalf("paramType(%+v) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("paramType(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMySQLDialect_RenderProcedure_StatementShape_NoParams(t *testing.T) {
	def := &procedure.Definition{
		Name: "reset_counters",
		Body: "UPDATE counters SET value = 0;",
	}

	stmts, err := mysqlDialect{}.renderProcedure(def)
	if err != nil {
		t.Fatalf("renderProcedure() error = %v", err)
	}
	want := []string{
		`DROP PROCEDURE IF EXISTS reset_counters;`,
		`CREATE PROCEDURE reset_counters() BEGIN
  UPDATE counters SET value = 0;
END`,
	}
	if !slicesEqual(stmts, want) {
		t.Errorf("renderProcedure() = %q, want %q", stmts, want)
	}
}

func TestMySQLDialect_RenderProcedure_ParamCeremony(t *testing.T) {
	def := &procedure.Definition{
		Name:   "sp_set_user_name",
		Params: []procedure.Param{{Name: "user_id", Type: procedure.Int}, {Name: "new_name", Type: procedure.Varchar(100)}},
		Body:   "UPDATE user_master SET name = new_name WHERE id = user_id;",
	}

	stmts, err := mysqlDialect{}.renderProcedure(def)
	if err != nil {
		t.Fatalf("renderProcedure() error = %v", err)
	}
	if want := "IN user_id INT, IN new_name VARCHAR(100)"; !strings.Contains(stmts[1], want) {
		t.Errorf("renderProcedure() missing param ceremony %q, got:\n%s", want, stmts[1])
	}
}

func TestMySQLDialect_RenderProcedureDeclarative_BodyMatchesIdempotent(t *testing.T) {
	def := &procedure.Definition{
		Name:   "sp_set_user_name",
		Params: []procedure.Param{{Name: "user_id", Type: procedure.Int}},
		Body:   "UPDATE user_master SET name = 'x' WHERE id = user_id;",
	}

	idempotent, err := mysqlDialect{}.renderProcedure(def)
	if err != nil {
		t.Fatalf("renderProcedure() error = %v", err)
	}
	declarative, err := mysqlDialect{}.renderProcedureDeclarative(def)
	if err != nil {
		t.Fatalf("renderProcedureDeclarative() error = %v", err)
	}
	if len(declarative) != 1 {
		t.Fatalf("renderProcedureDeclarative() returned %d statement(s), want 1 (no DROP)", len(declarative))
	}
	if declarative[0] != idempotent[1] {
		t.Errorf("renderProcedureDeclarative()[0] diverged from renderProcedure()'s CREATE statement:\ngot:  %s\nwant: %s", declarative[0], idempotent[1])
	}
}

func TestMySQLDialect_DropProcedure_StatementShape(t *testing.T) {
	def := &procedure.Definition{Name: "sp_set_user_name"}
	stmts, err := mysqlDialect{}.dropProcedure(def)
	if err != nil {
		t.Fatalf("dropProcedure() error = %v", err)
	}
	if want := "DROP PROCEDURE IF EXISTS sp_set_user_name;"; len(stmts) != 1 || stmts[0] != want {
		t.Errorf("dropProcedure() = %q, want [%q]", stmts, want)
	}
}

func TestSQLServerDialect_ParamType(t *testing.T) {
	d := sqlServerDialect{}
	tests := []struct {
		name string
		in   procedure.ParamType
		want string
	}{
		{"Text", procedure.Text, "NVARCHAR(MAX)"},
		{"Bool", procedure.Bool, "BIT"},
		{"Time", procedure.Time, "DATETIME2"},
		{"Varchar", procedure.Varchar(50), "NVARCHAR(50)"},
		{"Char", procedure.Char(10), "NCHAR(10)"},
		{"Float", procedure.Float, "FLOAT"},
		{"Bytes", procedure.Bytes, "VARBINARY(MAX)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.paramType(tt.in)
			if err != nil {
				t.Fatalf("paramType(%+v) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("paramType(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSQLServerDialect_RenderProcedure_StatementShape(t *testing.T) {
	def := &procedure.Definition{
		Name:   "sp_set_user_name",
		Params: []procedure.Param{{Name: "user_id", Type: procedure.Int}, {Name: "new_name", Type: procedure.Varchar(100)}},
		Body:   "UPDATE user_master SET name = @new_name WHERE id = @user_id;",
	}

	stmts, err := sqlServerDialect{}.renderProcedure(def)
	if err != nil {
		t.Fatalf("renderProcedure() error = %v", err)
	}
	want := []string{`CREATE OR ALTER PROCEDURE sp_set_user_name @user_id INT, @new_name NVARCHAR(100) AS
BEGIN
  UPDATE user_master SET name = @new_name WHERE id = @user_id;
END;`}
	if !slicesEqual(stmts, want) {
		t.Errorf("renderProcedure() = %q, want %q", stmts, want)
	}
}

func TestSQLServerDialect_RenderProcedureDeclarative_StatementShape(t *testing.T) {
	def := &procedure.Definition{
		Name:   "sp_set_user_name",
		Params: []procedure.Param{{Name: "user_id", Type: procedure.Int}},
		Body:   "UPDATE user_master SET id = @user_id;",
	}

	stmts, err := sqlServerDialect{}.renderProcedureDeclarative(def)
	if err != nil {
		t.Fatalf("renderProcedureDeclarative() error = %v", err)
	}
	if len(stmts) != 1 || strings.Contains(stmts[0], "OR ALTER") {
		t.Errorf("renderProcedureDeclarative() should be a single statement with no OR ALTER, got:\n%s", strings.Join(stmts, "\n---\n"))
	}
}

func TestSQLServerDialect_DropProcedure_StatementShape(t *testing.T) {
	def := &procedure.Definition{Name: "sp_set_user_name"}
	stmts, err := sqlServerDialect{}.dropProcedure(def)
	if err != nil {
		t.Fatalf("dropProcedure() error = %v", err)
	}
	if want := "DROP PROCEDURE IF EXISTS sp_set_user_name;"; len(stmts) != 1 || stmts[0] != want {
		t.Errorf("dropProcedure() = %q, want [%q]", stmts, want)
	}
}

// TestResolveDDL_SQLite_RejectsProcedure confirms SQLite -- which has no
// stored procedure concept at all -- is rejected via resolveDDL's own
// dialect-capability check, the same mechanism trigger/view already use,
// rather than needing any SQLite-specific procedure code.
func TestResolveDDL_SQLite_RejectsProcedure(t *testing.T) {
	proc := procedure.New("noop").Body("SELECT 1;")
	client := &Client{}
	if _, _, _, err := client.resolveDDL(sqliteDialect{}, proc, opRender); err == nil {
		t.Fatal("resolveDDL() error = nil, want error: sqlite has no procedure support")
	}
}

// slicesEqual is a tiny local helper -- this file predates slices.Equal
// being reached for elsewhere in the codebase, and pulling in a new
// import for one comparison isn't worth it.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
