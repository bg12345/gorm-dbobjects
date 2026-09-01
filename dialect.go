package dbobjects

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bg12345/gorm-dbobjects/procedure"
	"github.com/bg12345/gorm-dbobjects/trigger"
	"github.com/bg12345/gorm-dbobjects/view"
	"gorm.io/gorm"
)


type triggerDialect interface {
	renderTrigger(def *trigger.Definition) ([]string, error)
	renderTriggerDeclarative(def *trigger.Definition) ([]string, error)
	dropTrigger(def *trigger.Definition) ([]string, error)
}

type viewDialect interface {
	renderView(db *gorm.DB, def *view.Definition) ([]string, error)
	renderViewDeclarative(db *gorm.DB, def *view.Definition) ([]string, error)
	dropView(def *view.Definition) ([]string, error)
}

// procedureDialect is implemented only by engines with a real stored
// procedure concept -- Postgres, MySQL, SQL Server. Not sqliteDialect
// (SQLite has none at all): resolveDDL's existing d.(procedureDialect)
// type-assertion failure already produces the right rejection once a
// dialect simply doesn't implement this, no SQLite-specific code needed.
type procedureDialect interface {
	renderProcedure(def *procedure.Definition) ([]string, error)
	renderProcedureDeclarative(def *procedure.Definition) ([]string, error)
	dropProcedure(def *procedure.Definition) ([]string, error)
}

type dialect interface {
	Name() string
	transactional() bool
}

var dialects = map[string]dialect{
	"postgres":  postgresDialect{},
	"mysql":     mysqlDialect{},
	"sqlite":    sqliteDialect{},
	"sqlserver": sqlServerDialect{},
}

func dialectFor(name string) (dialect, bool) {
	d, ok := dialects[name]
	return d, ok
}

type postgresDialect struct{}

func (postgresDialect) Name() string {
	return "postgres"
}

func (postgresDialect) transactional() bool {
	return true
}

type mysqlDialect struct{}

func (mysqlDialect) Name() string {
	return "mysql"
}

func (mysqlDialect) transactional() bool {
	return false
}

type sqliteDialect struct{}

func (sqliteDialect) Name() string {
	return "sqlite"
}

func (sqliteDialect) transactional() bool {
	return true
}

type sqlServerDialect struct{}

func (sqlServerDialect) Name() string {
	return "sqlserver"
}

func (sqlServerDialect) transactional() bool {
	return true
}


// renderExpr resolves e to this dialect's SQL spelling. Postgres and
// MySQL both happen to spell "current timestamp" as NOW(); other
// engines will need their own version of this method once those
// dialects exist.
func (postgresDialect) renderExpr(e trigger.Expr) string {
	switch e.Kind {
	case trigger.ExprNow:
		return "NOW()"
	default:
		return e.Raw
	}
}

func (mysqlDialect) renderExpr(e trigger.Expr) string {
	switch e.Kind {
	case trigger.ExprNow:
		return "NOW()"
	default:
		return e.Raw
	}
}

func (sqliteDialect) renderExpr(e trigger.Expr) string {
	switch e.Kind {
	case trigger.ExprNow:
		return "CURRENT_TIMESTAMP"
	default:
		return e.Raw
	}
}

// GETDATE() over SYSDATETIME()/SYSUTCDATETIME(): the idiomatic,
// long-standing spelling (Microsoft's own CREATE TRIGGER examples use
// it), and this project already tolerates cross-engine Now()
// inconsistency (Postgres/MySQL local time, SQLite UTC) rather than
// normalizing it, so there's no concrete reason to reach for
// SYSUTCDATETIME()'s different return type (datetime2, sub-millisecond
// precision) here specifically.
func (sqlServerDialect) renderExpr(e trigger.Expr) string {
	switch e.Kind {
	case trigger.ExprNow:
		return "GETDATE()"
	default:
		return e.Raw
	}
}

// triggerNames derives the trigger and backing-function names for def. If
// def.Name is set, it is used verbatim as the trigger name and the function
// name is paired to it ("fn_" + Name with any "trg_" prefix stripped), so a
// caller-supplied Name("trg_users_touch") yields function fn_users_touch.
// Otherwise both names are generated from table/timing/event.
func triggerNames(def *trigger.Definition) (fnName, trgName string) {
	if def.Name != "" {
		return "fn_" + strings.TrimPrefix(def.Name, "trg_"), def.Name
	}
	timing := strings.ToLower(def.Timing)
	event := strings.ToLower(def.Event)
	return fmt.Sprintf("fn_%s_%s_%s", def.Table, timing, event),
		fmt.Sprintf("trg_%s_%s_%s", def.Table, timing, event)
}

// sortedSetColumns returns the columns of sets in sorted order, so
// generated SQL is deterministic across Go's randomized map iteration.
func sortedSetColumns(sets map[string]trigger.Expr) []string {
	columns := make([]string, 0, len(sets))
	for column := range sets {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return columns
}

// triggerBody renders the BEGIN...END body for def's trigger function and
// the RETURN variable (NEW/OLD) it should use. Shared by renderTrigger
// and renderTriggerDeclarative so the two variants can never diverge on
// body content -- only the outer wrapper (CREATE OR REPLACE vs bare
// CREATE, presence/absence of a DROP) differs between them.
func (d postgresDialect) triggerBody(def *trigger.Definition) (body, retVar string) {
	columns := sortedSetColumns(def.Sets)

	var b strings.Builder
	switch {
	case def.Body != "":
		fmt.Fprintf(&b, "  %s\n", def.Body)
	case def.Timing == "AFTER":
		// NEW isn't assignable once an AFTER trigger fires -- the row's
		// already written -- so Set() renders as an explicit follow-up
		// UPDATE targeting the row via its primary key instead of
		// NEW.col = expr (see Definition.PrimaryKey).
		assigns := make([]string, len(columns))
		for i, column := range columns {
			assigns[i] = fmt.Sprintf("%s = %s", column, d.renderExpr(def.Sets[column]))
		}
		if def.Event == "UPDATE" {
			// This UPDATE re-fires this same AFTER UPDATE trigger.
			// pg_trigger_depth() > 1 means we're already inside that
			// recursive firing -- skip the body there so the follow-up
			// UPDATE runs exactly once per original statement, not
			// forever.
			fmt.Fprintf(&b, "  IF pg_trigger_depth() > 1 THEN RETURN NULL; END IF;\n")
		}
		fmt.Fprintf(&b, "  UPDATE %s SET %s WHERE %s = NEW.%s;\n",
			def.Table, strings.Join(assigns, ", "), def.PrimaryKey, def.PrimaryKey)
	default:
		for _, column := range columns {
			fmt.Fprintf(&b, "  NEW.%s = %s;\n", column, d.renderExpr(def.Sets[column]))
		}
	}

	retVar = "NEW"
	if def.Event == "DELETE" {
		retVar = "OLD"
	}
	return b.String(), retVar
}

// createTriggerStmt renders the CREATE TRIGGER statement itself -- shared
// by renderTrigger and renderTriggerDeclarative since this part never
// has a conditional form to strip (Postgres's CREATE TRIGGER has no
// bare-vs-OR-REPLACE distinction; only the backing function does).
func (postgresDialect) createTriggerStmt(def *trigger.Definition, fnName, trgName string) string {
	return fmt.Sprintf(`CREATE TRIGGER %s %s %s ON %s
FOR EACH ROW EXECUTE FUNCTION %s();`, trgName, def.Timing, def.Event, def.Table, fnName)
}

func (d postgresDialect) renderTrigger(def *trigger.Definition) ([]string, error) {
	fnName, trgName := triggerNames(def)
	body, retVar := d.triggerBody(def)

	createFn := fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS TRIGGER AS $$
BEGIN
%s  RETURN %s;
END;
$$ LANGUAGE plpgsql;`, fnName, body, retVar)

	dropTrg := fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;`, trgName, def.Table)

	return []string{createFn, dropTrg, d.createTriggerStmt(def, fnName, trgName)}, nil
}

// renderTriggerDeclarative renders def as bare CREATE statements -- no
// CREATE OR REPLACE, no DROP -- for structural DDL consumers (e.g. an
// Atlas external schema source) that parse and diff desired-state SQL
// themselves rather than executing conditional DDL directly. Shares
// triggerBody/createTriggerStmt with renderTrigger so the two can never
// render different body content for the same def.
func (d postgresDialect) renderTriggerDeclarative(def *trigger.Definition) ([]string, error) {
	fnName, trgName := triggerNames(def)
	body, retVar := d.triggerBody(def)

	createFn := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS TRIGGER AS $$
BEGIN
%s  RETURN %s;
END;
$$ LANGUAGE plpgsql;`, fnName, body, retVar)

	return []string{createFn, d.createTriggerStmt(def, fnName, trgName)}, nil
}

func (postgresDialect) dropTrigger(def *trigger.Definition) ([]string, error) {
	fnName, trgName := triggerNames(def)

	return []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;`, trgName, def.Table),
		fmt.Sprintf(`DROP FUNCTION IF EXISTS %s();`, fnName),
	}, nil
}

// triggerBody renders the BEGIN...END body for def's trigger -- shared
// by renderTrigger and renderTriggerDeclarative so the two variants can
// never diverge on body content -- only the outer CREATE/DROP wrapper
// differs between them. trgName is needed here (not just by the caller)
// because the AFTER UPDATE recursion guard's session-variable name is
// scoped by trigger name.
func (d mysqlDialect) triggerBody(def *trigger.Definition, trgName string) string {
	columns := sortedSetColumns(def.Sets)

	var body strings.Builder
	switch {
	case def.Body != "":
		fmt.Fprintf(&body, "  %s\n", def.Body)
	case def.Timing == "AFTER":
		// NEW isn't assignable in a MySQL AFTER trigger at all ("Updating
		// of NEW row is not allowed in after trigger"), so Set() renders
		// as an explicit follow-up UPDATE targeting the row via its
		// primary key instead (see Definition.PrimaryKey).
		assigns := make([]string, len(columns))
		for i, column := range columns {
			assigns[i] = fmt.Sprintf("%s = %s", column, d.renderExpr(def.Sets[column]))
		}
		updateStmt := fmt.Sprintf("UPDATE %s SET %s WHERE %s = NEW.%s;",
			def.Table, strings.Join(assigns, ", "), def.PrimaryKey, def.PrimaryKey)
		if def.Event == "UPDATE" {
			// MySQL has no pg_trigger_depth() equivalent to detect a
			// recursive firing. This UPDATE re-fires this same AFTER
			// UPDATE trigger, so a session variable stands in as a
			// manual reentrancy guard -- the recursive firing sees it
			// already set and skips its own UPDATE.
			//
			// A nested EXIT HANDLER FOR SQLEXCEPTION wraps just the
			// UPDATE: DECLARE HANDLER must be the first statement in
			// its enclosing BEGIN...END, hence the extra nesting. If
			// the UPDATE errors, the handler resets the guard *before*
			// RESIGNALing the original error, so a failed statement
			// doesn't leave the guard stuck at 1 for the rest of this
			// (possibly pooled) session -- session variables survive a
			// rolled-back statement, unlike table data.
			guardVar := "@dbobjects_guard_" + trgName
			fmt.Fprintf(&body, `  IF %[1]s IS NULL THEN
    SET %[1]s = 1;
    BEGIN
      DECLARE EXIT HANDLER FOR SQLEXCEPTION
      BEGIN
        SET %[1]s = NULL;
        RESIGNAL;
      END;
      %[2]s
      SET %[1]s = NULL;
    END;
  END IF;
`, guardVar, updateStmt)
		} else {
			fmt.Fprintf(&body, "  %s\n", updateStmt)
		}
	default:
		for _, column := range columns {
			fmt.Fprintf(&body, "  SET NEW.%s = %s;\n", column, d.renderExpr(def.Sets[column]))
		}
	}
	return body.String()
}

// createTriggerStmt renders the CREATE TRIGGER statement itself -- shared
// by renderTrigger and renderTriggerDeclarative, since MySQL has no
// CREATE OR REPLACE TRIGGER to begin with: this part of the output is
// already identical between idempotent and declarative modes.
func (mysqlDialect) createTriggerStmt(def *trigger.Definition, trgName, body string) string {
	return fmt.Sprintf(`CREATE TRIGGER %s %s %s ON %s
FOR EACH ROW BEGIN
%sEND;`, trgName, def.Timing, def.Event, def.Table, body)
}

func (d mysqlDialect) renderTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	dropStmt := fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName)
	return []string{dropStmt, d.createTriggerStmt(def, trgName, d.triggerBody(def, trgName))}, nil
}

// renderTriggerDeclarative renders def as a single bare CREATE TRIGGER
// statement -- no DROP -- for structural DDL consumers (e.g. an Atlas
// external schema source). MySQL has no CREATE OR REPLACE TRIGGER, so
// unlike Postgres there's no second conditional to strip; omitting the
// DROP is the only difference from renderTrigger.
func (d mysqlDialect) renderTriggerDeclarative(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	return []string{d.createTriggerStmt(def, trgName, d.triggerBody(def, trgName))}, nil
}

func (d mysqlDialect) dropTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)

	return []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName),
	}, nil
}

// triggerBody renders the BEGIN...END body for def's trigger and the
// WHEN clause it needs, if any. SQLite trigger bodies have no
// procedural control flow at all (no IF, no exception handler) -- WHEN
// is the only conditional gate SQLite has, evaluated once before the
// body runs, so any recursion guard has to live there rather than
// inline in the body the way Postgres's pg_trigger_depth() check or
// MySQL's IF do. Set()-based bodies always target the row via rowid,
// never Definition.PrimaryKey -- rowid is implicitly present on every
// ordinary table and, since the caller-facing timing is always coerced
// to AFTER for the Set() path (the row's guaranteed to already exist by
// then), NEW.rowid is always well-defined; a table declared WITHOUT
// ROWID has none, and that surfaces as a real "no such column: rowid"
// error the first time such a trigger fires, not something this layer
// can pre-validate.
func (d sqliteDialect) triggerBody(def *trigger.Definition, trgName string) (body, when string) {
	if def.Body != "" {
		return fmt.Sprintf("  %s\n", def.Body), ""
	}

	columns := sortedSetColumns(def.Sets)
	assigns := make([]string, len(columns))
	for i, column := range columns {
		assigns[i] = fmt.Sprintf("%s = %s", column, d.renderExpr(def.Sets[column]))
	}
	updateStmt := fmt.Sprintf("UPDATE %s SET %s WHERE rowid = NEW.rowid;",
		def.Table, strings.Join(assigns, ", "))

	if def.Event != "UPDATE" {
		// AFTER INSERT can't recurse into itself via this UPDATE (it's
		// not another INSERT), so no guard is needed.
		return fmt.Sprintf("  %s\n", updateStmt), ""
	}

	// This UPDATE re-fires this same AFTER UPDATE trigger. Guarded via a
	// permanent dbobjects_guard table (created alongside the trigger,
	// see renderTrigger/renderTriggerDeclarative) rather than a
	// per-connection TEMP table or PRAGMA, since both of those are
	// connection-scoped and invisible to a trigger firing on a
	// different pooled connection than the one that created them.
	body = fmt.Sprintf("  INSERT INTO dbobjects_guard(name) VALUES ('%s');\n  %s\n  DELETE FROM dbobjects_guard WHERE name = '%s';\n",
		trgName, updateStmt, trgName)
	when = fmt.Sprintf("NOT EXISTS (SELECT 1 FROM dbobjects_guard WHERE name = '%s')", trgName)
	return body, when
}

// createTriggerStmt renders the CREATE TRIGGER statement itself --
// shared by renderTrigger and renderTriggerDeclarative. timing is
// passed in rather than read from def.Timing directly: Set()-based
// triggers always render as AFTER regardless of what the caller
// declared (BEFORE INSERT/UPDATE can't work at all on SQLite -- see
// triggerBody), while Body()-based triggers keep the declared timing
// verbatim, and the caller (not this helper) is what decides which
// case applies.
func (sqliteDialect) createTriggerStmt(def *trigger.Definition, trgName, timing, body, when string) string {
	var whenClause string
	if when != "" {
		whenClause = fmt.Sprintf("\nWHEN %s", when)
	}
	return fmt.Sprintf(`CREATE TRIGGER %s %s %s ON %s%s
BEGIN
%sEND;`, trgName, timing, def.Event, def.Table, whenClause, body)
}

// effectiveTriggerTiming returns the timing SQLite DDL should actually
// use for def. Set()-based triggers always render as AFTER (see
// triggerBody's doc comment); Body()-based triggers keep def.Timing
// verbatim, since a literal BEFORE UPDATE ... WHEN ... BEGIN
// SELECT RAISE(...) END read-only-validation pattern has nothing to do
// with the NEW-assignment problem Set() has to work around.
//
// Known cosmetic-only quirk: the auto-generated trigger name (from the
// shared triggerNames helper, used by renderTrigger/dropTrigger alike)
// is still derived from def.Timing, not this function's return value --
// a Set()-based BeforeUpdate(...) trigger ends up named
// trg_<table>_before_update while its actual DDL is AFTER UPDATE.
// Harmless: renderTrigger and dropTrigger both derive the name from
// def.Timing identically, so they always agree and dropping still
// targets the right trigger. Not fixed here since it'd mean special-
// casing the shared triggerNames helper for one dialect's benefit.
func effectiveTriggerTiming(def *trigger.Definition) string {
	if def.Body == "" {
		return "AFTER"
	}
	return def.Timing
}

func (d sqliteDialect) renderTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	body, when := d.triggerBody(def, trgName)

	stmts := []string{fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName)}
	if when != "" {
		stmts = append(stmts, `CREATE TABLE IF NOT EXISTS dbobjects_guard (name TEXT PRIMARY KEY);`)
	}
	return append(stmts, d.createTriggerStmt(def, trgName, effectiveTriggerTiming(def), body, when)), nil
}

// renderTriggerDeclarative renders def as bare CREATE statements -- no
// DROP -- for structural DDL consumers (e.g. an Atlas external schema
// source). The guard table's CREATE TABLE IF NOT EXISTS is included
// here too, when needed: a structural-DDL consumer applying this output
// against a fresh database needs that table to exist, or the emitted
// WHEN clause references a nonexistent one.
func (d sqliteDialect) renderTriggerDeclarative(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	body, when := d.triggerBody(def, trgName)

	var stmts []string
	if when != "" {
		stmts = append(stmts, `CREATE TABLE IF NOT EXISTS dbobjects_guard (name TEXT PRIMARY KEY);`)
	}
	return append(stmts, d.createTriggerStmt(def, trgName, effectiveTriggerTiming(def), body, when)), nil
}

func (sqliteDialect) dropTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	// Deliberately never touches dbobjects_guard -- it's shared across
	// every guarded trigger in the database, so dropping it here would
	// break every other still-registered guarded trigger.
	return []string{fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName)}, nil
}

// sqlServerAfterConstructorHint names the After* constructor a BEFORE
// + Body() error should point callers at, for def's event.
func sqlServerAfterConstructorHint(event string) string {
	switch event {
	case "INSERT":
		return "AfterInsert"
	case "UPDATE":
		return "AfterUpdate"
	case "DELETE":
		return "AfterDelete"
	default:
		return "After" + event
	}
}

// triggerBody renders the BEGIN...END body for def's trigger, or an
// error when def can't be rendered on SQL Server at all -- two cases:
//
//   - Body() on a BEFORE-declared trigger: unlike Set() (mechanically
//     forced to AFTER and semantically equivalent either way -- the row
//     ends up in the same state), Body() is caller-authored SQL that
//     assumes a specific firing point relative to the write. SQL Server
//     has no mechanism -- not even INSTEAD OF, a different statement-
//     replacement primitive (works on tables too, not just views, but
//     nothing is written unless the trigger body issues its own
//     INSERT/UPDATE/DELETE, and constraints check *after* it runs, not
//     before) -- deliberately not used here -- that makes "run this
//     before the write, with the write still happening automatically
//     afterward" safe. Silently coercing Body()'s declared timing the
//     way Set() safely can would be a correctness bug dressed as
//     flexibility, so it's a render-time error instead.
//   - Set() on a table with no usable primary key (Definition.PrimaryKey
//     empty): SQL Server has no rowid-style fallback the way SQLite
//     does, so a composite or missing primary key genuinely can't be
//     targeted by the UPDATE ... FROM inserted join below.
func (d sqlServerDialect) triggerBody(def *trigger.Definition) (string, error) {
	if def.Body != "" {
		if def.Timing == "BEFORE" {
			return "", fmt.Errorf("dbobjects: sqlserver has no BEFORE trigger of any kind (not even Body()-based) -- declare this as %s instead, or issue a raw INSTEAD OF trigger outside this library if you need to intercept the write itself",
				sqlServerAfterConstructorHint(def.Event))
		}
		return fmt.Sprintf("  %s\n", def.Body), nil
	}

	if def.PrimaryKey == "" {
		return "", fmt.Errorf("dbobjects: sqlserver Set() requires table %q to have exactly one primary key column (found none or a composite key) to target the UPDATE ... FROM inserted join",
			def.Table)
	}

	columns := sortedSetColumns(def.Sets)
	assigns := make([]string, len(columns))
	for i, column := range columns {
		assigns[i] = fmt.Sprintf("%s = %s", column, d.renderExpr(def.Sets[column]))
	}
	// Set-based join-update, not a single-row WHERE: a SQL Server
	// trigger fires once per statement, and inserted/deleted can hold
	// zero, one, or many rows in that one firing, not just one.
	updateStmt := fmt.Sprintf("UPDATE t SET %s FROM %s AS t INNER JOIN inserted AS i ON t.%s = i.%s;",
		strings.Join(assigns, ", "), def.Table, def.PrimaryKey, def.PrimaryKey)

	if def.Event != "UPDATE" {
		// AFTER INSERT can't recurse into itself via this UPDATE.
		return fmt.Sprintf("  %s\n", updateStmt), nil
	}

	// This UPDATE re-fires this same AFTER UPDATE trigger.
	// TRIGGER_NESTLEVEL() is SQL Server's real analog of Postgres's
	// pg_trigger_depth() -- a genuine engine-maintained call-stack
	// counter, not a home-grown table the way SQLite needed (SQL Server
	// has no rowid-style gap forcing that kind of workaround here).
	// > 1 means we're already inside the recursive firing.
	return fmt.Sprintf("  IF TRIGGER_NESTLEVEL() > 1 RETURN;\n  %s\n", updateStmt), nil
}

// createTriggerStmt renders the CREATE TRIGGER statement itself --
// shared by renderTrigger and renderTriggerDeclarative. Timing is
// always the literal AFTER: the Set() path forces it (triggerBody), and
// the Body() path never reaches here with a BEFORE timing at all --
// triggerBody already returned an error before this is called. orAlter
// is "OR ALTER " for the idempotent path, "" for declarative.
func (sqlServerDialect) createTriggerStmt(def *trigger.Definition, trgName, orAlter, body string) string {
	return fmt.Sprintf(`CREATE %sTRIGGER %s ON %s AFTER %s AS
BEGIN
  SET NOCOUNT ON;
%sEND;`, orAlter, trgName, def.Table, def.Event, body)
}

// renderTrigger renders CREATE OR ALTER TRIGGER as a single statement,
// no DROP -- unlike MySQL/SQLite (no CREATE OR REPLACE/ALTER TRIGGER at
// all, need DROP IF EXISTS + CREATE as two statements), SQL Server's own
// OR ALTER fully redefines the trigger -- including its event list --
// in one statement, so a separate DROP first would be redundant, the
// same way Postgres's CREATE OR REPLACE FUNCTION needs no DROP either.
func (d sqlServerDialect) renderTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	body, err := d.triggerBody(def)
	if err != nil {
		return nil, err
	}
	return []string{d.createTriggerStmt(def, trgName, "OR ALTER ", body)}, nil
}

func (d sqlServerDialect) renderTriggerDeclarative(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	body, err := d.triggerBody(def)
	if err != nil {
		return nil, err
	}
	return []string{d.createTriggerStmt(def, trgName, "", body)}, nil
}

func (sqlServerDialect) dropTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)
	return []string{fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName)}, nil
}

// Known cosmetic-only quirk, same as SQLite's already-documented
// equivalent: the auto-generated trigger name (from the shared
// triggerNames helper) is derived from def.Timing, so a
// BeforeUpdate(...).Set(...) trigger is named trg_<table>_before_update
// while its real DDL is AFTER UPDATE (Set() always forces AFTER here --
// see triggerBody). Harmless: renderTrigger and dropTrigger both derive
// the name from def.Timing identically, so they always agree.


// resolveViewBody returns def's view body: RawSQL verbatim if set,
// otherwise fully-interpolated SQL from QueryFn via db.ToSQL. Shared by
// renderViewCreateOrReplace and renderViewCreate so the two can never
// diverge on body content -- only the outer CREATE OR REPLACE vs bare
// CREATE wrapper differs.
//
// ToSQL only populates stmt.SQL once a finisher method (Find, First,
// ...) runs gorm's query callback chain -- .Model()/.Where() alone just
// accumulate clauses on the Statement. view.Query(fn) deliberately
// doesn't require callers to know that (no .Find(&dest) in their
// callback), so it's appended here instead. The destination is a
// generic map, not the caller's model type, since this layer never
// knows that type -- it relies on .Model() already being set inside fn
// to determine what gets selected.
func resolveViewBody(db *gorm.DB, def *view.Definition) string {
	if def.RawSQL != "" {
		return def.RawSQL
	}
	return db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return def.QueryFn(tx).Find(&[]map[string]any{})
	})
}

// renderViewCreateOrReplace renders def as a CREATE OR REPLACE VIEW
// statement -- shared by postgresDialect and mysqlDialect since both
// support identical syntax here, unlike triggers where the two engines
// diverge structurally (SQL Server's CREATE OR ALTER and SQLite's lack
// of CREATE OR REPLACE will need their own implementations later).
func renderViewCreateOrReplace(db *gorm.DB, def *view.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`CREATE OR REPLACE VIEW %s AS %s`, def.Name, resolveViewBody(db, def))}, nil
}

// renderViewCreate renders def as a bare CREATE VIEW statement -- no OR
// REPLACE -- for structural DDL consumers (e.g. an Atlas external
// schema source). Shared by both dialects for the same reason
// renderViewCreateOrReplace is.
func renderViewCreate(db *gorm.DB, def *view.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`CREATE VIEW %s AS %s`, def.Name, resolveViewBody(db, def))}, nil
}

// renderViewCreateOrAlter renders def as a CREATE OR ALTER VIEW
// statement -- SQL Server's own idempotent-native spelling; not
// CREATE OR REPLACE VIEW (Postgres/MySQL's keyword, not valid T-SQL).
func renderViewCreateOrAlter(db *gorm.DB, def *view.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`CREATE OR ALTER VIEW %s AS %s`, def.Name, resolveViewBody(db, def))}, nil
}

// dropViewIfExists renders a DROP VIEW IF EXISTS statement -- shared
// for the same reason renderViewCreateOrReplace is.
func dropViewIfExists(def *view.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`DROP VIEW IF EXISTS %s;`, def.Name)}, nil
}

func (postgresDialect) renderView(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreateOrReplace(db, def)
}

func (postgresDialect) renderViewDeclarative(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreate(db, def)
}

func (postgresDialect) dropView(def *view.Definition) ([]string, error) {
	return dropViewIfExists(def)
}

func (mysqlDialect) renderView(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreateOrReplace(db, def)
}

func (mysqlDialect) renderViewDeclarative(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreate(db, def)
}

func (mysqlDialect) dropView(def *view.Definition) ([]string, error) {
	return dropViewIfExists(def)
}

// renderView composes SQLite's idempotent view DDL from the two
// existing shared helpers -- no SQLite-specific rendering logic needed.
// SQLite has no CREATE OR REPLACE VIEW, so idempotent is DROP VIEW IF
// EXISTS + bare CREATE VIEW as two statements, the same shape MySQL's
// trigger rendering already uses.
func (sqliteDialect) renderView(db *gorm.DB, def *view.Definition) ([]string, error) {
	drop, _ := dropViewIfExists(def)
	create, err := renderViewCreate(db, def)
	if err != nil {
		return nil, err
	}
	return append(drop, create...), nil
}

func (sqliteDialect) renderViewDeclarative(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreate(db, def)
}

func (sqliteDialect) dropView(def *view.Definition) ([]string, error) {
	return dropViewIfExists(def)
}

// renderView, renderViewDeclarative, and dropView are pure composition
// of the shared helpers -- same as SQLite's view story, zero new
// rendering logic. CREATE OR ALTER VIEW (2016 SP1+) is idempotent-
// native, so unlike sqliteDialect's renderView this doesn't need a
// DROP-first composition -- one statement is enough.
func (sqlServerDialect) renderView(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreateOrAlter(db, def)
}

func (sqlServerDialect) renderViewDeclarative(db *gorm.DB, def *view.Definition) ([]string, error) {
	return renderViewCreate(db, def)
}

func (sqlServerDialect) dropView(def *view.Definition) ([]string, error) {
	return dropViewIfExists(def)
}

// validateSizedParam checks Varchar/Char/Decimal's numeric arguments --
// shared by every dialect's paramType since the constraint itself
// (positive size, positive precision, a scale that fits within it)
// doesn't vary by engine, only the resulting type name's spelling does.
func validateSizedParam(t procedure.ParamType) error {
	switch t.Kind {
	case procedure.ParamVarchar, procedure.ParamChar:
		if t.Size <= 0 {
			return fmt.Errorf("dbobjects: procedure param size must be positive, got %d", t.Size)
		}
	case procedure.ParamDecimal:
		if t.Size <= 0 {
			return fmt.Errorf("dbobjects: procedure param precision must be positive, got %d", t.Size)
		}
		if t.Scale < 0 || t.Scale > t.Size {
			return fmt.Errorf("dbobjects: procedure param scale must be between 0 and precision (%d), got %d", t.Size, t.Scale)
		}
	}
	return nil
}

func (postgresDialect) paramType(t procedure.ParamType) (string, error) {
	if err := validateSizedParam(t); err != nil {
		return "", err
	}
	switch t.Kind {
	case procedure.ParamInt:
		return "INT", nil
	case procedure.ParamText:
		return "TEXT", nil
	case procedure.ParamBool:
		return "BOOLEAN", nil
	case procedure.ParamTime:
		return "TIMESTAMP", nil
	case procedure.ParamVarchar:
		return fmt.Sprintf("VARCHAR(%d)", t.Size), nil
	case procedure.ParamChar:
		return fmt.Sprintf("CHAR(%d)", t.Size), nil
	case procedure.ParamDecimal:
		return fmt.Sprintf("DECIMAL(%d,%d)", t.Size, t.Scale), nil
	case procedure.ParamFloat:
		return "DOUBLE PRECISION", nil
	case procedure.ParamBytes:
		return "BYTEA", nil
	default: // ParamRaw
		return t.Raw, nil
	}
}

func (mysqlDialect) paramType(t procedure.ParamType) (string, error) {
	if err := validateSizedParam(t); err != nil {
		return "", err
	}
	switch t.Kind {
	case procedure.ParamInt:
		return "INT", nil
	case procedure.ParamText:
		return "TEXT", nil
	case procedure.ParamBool:
		return "BOOLEAN", nil
	case procedure.ParamTime:
		return "DATETIME", nil
	case procedure.ParamVarchar:
		return fmt.Sprintf("VARCHAR(%d)", t.Size), nil
	case procedure.ParamChar:
		return fmt.Sprintf("CHAR(%d)", t.Size), nil
	case procedure.ParamDecimal:
		return fmt.Sprintf("DECIMAL(%d,%d)", t.Size, t.Scale), nil
	case procedure.ParamFloat:
		return "DOUBLE", nil
	case procedure.ParamBytes:
		return "BLOB", nil
	default: // ParamRaw
		return t.Raw, nil
	}
}

// paramType's sized-string variants stay N-prefixed (NVARCHAR/NCHAR),
// same reasoning as Text's own NVARCHAR(MAX) choice -- a consistent
// Unicode-safe default across every string-shaped portable type on
// this engine, not just the unsized one.
func (sqlServerDialect) paramType(t procedure.ParamType) (string, error) {
	if err := validateSizedParam(t); err != nil {
		return "", err
	}
	switch t.Kind {
	case procedure.ParamInt:
		return "INT", nil
	case procedure.ParamText:
		return "NVARCHAR(MAX)", nil
	case procedure.ParamBool:
		return "BIT", nil
	case procedure.ParamTime:
		return "DATETIME2", nil
	case procedure.ParamVarchar:
		return fmt.Sprintf("NVARCHAR(%d)", t.Size), nil
	case procedure.ParamChar:
		return fmt.Sprintf("NCHAR(%d)", t.Size), nil
	case procedure.ParamDecimal:
		return fmt.Sprintf("DECIMAL(%d,%d)", t.Size, t.Scale), nil
	case procedure.ParamFloat:
		return "FLOAT", nil
	case procedure.ParamBytes:
		return "VARBINARY(MAX)", nil
	default: // ParamRaw
		return t.Raw, nil
	}
}

// procedureParamList renders def's params as a comma-joined signature
// fragment. paramType resolves each param's dialect-specific SQL type;
// sig formats the per-engine ceremony around it (Postgres: "name TYPE";
// MySQL: "IN name TYPE"; SQL Server: "@name TYPE") -- shared here since
// the iteration/error-wrapping is identical, only that ceremony varies.
func procedureParamList(def *procedure.Definition, paramType func(procedure.ParamType) (string, error), sig func(name, sqlType string) string) (string, error) {
	parts := make([]string, len(def.Params))
	for i, p := range def.Params {
		t, err := paramType(p.Type)
		if err != nil {
			return "", fmt.Errorf("dbobjects: procedure %q param %q: %w", def.Name, p.Name, err)
		}
		parts[i] = sig(p.Name, t)
	}
	return strings.Join(parts, ", "), nil
}

func postgresProcedureSig(name, sqlType string) string { return name + " " + sqlType }

func (d postgresDialect) renderProcedure(def *procedure.Definition) ([]string, error) {
	params, err := procedureParamList(def, d.paramType, postgresProcedureSig)
	if err != nil {
		return nil, err
	}
	create := fmt.Sprintf(`CREATE OR REPLACE PROCEDURE %s(%s) LANGUAGE plpgsql AS $$
BEGIN
  %s
END;
$$;`, def.Name, params, def.Body)
	return []string{create}, nil
}

func (d postgresDialect) renderProcedureDeclarative(def *procedure.Definition) ([]string, error) {
	params, err := procedureParamList(def, d.paramType, postgresProcedureSig)
	if err != nil {
		return nil, err
	}
	create := fmt.Sprintf(`CREATE PROCEDURE %s(%s) LANGUAGE plpgsql AS $$
BEGIN
  %s
END;
$$;`, def.Name, params, def.Body)
	return []string{create}, nil
}

func (postgresDialect) dropProcedure(def *procedure.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`DROP PROCEDURE IF EXISTS %s;`, def.Name)}, nil
}

func mysqlProcedureSig(name, sqlType string) string { return "IN " + name + " " + sqlType }

// renderProcedure: MySQL has no CREATE OR REPLACE PROCEDURE, so
// idempotent is DROP PROCEDURE IF EXISTS + bare CREATE as two
// statements, the same shape MySQL's trigger rendering already uses.
func (d mysqlDialect) renderProcedure(def *procedure.Definition) ([]string, error) {
	params, err := procedureParamList(def, d.paramType, mysqlProcedureSig)
	if err != nil {
		return nil, err
	}
	dropStmt := fmt.Sprintf(`DROP PROCEDURE IF EXISTS %s;`, def.Name)
	create := fmt.Sprintf(`CREATE PROCEDURE %s(%s) BEGIN
  %s
END`, def.Name, params, def.Body)
	return []string{dropStmt, create}, nil
}

func (d mysqlDialect) renderProcedureDeclarative(def *procedure.Definition) ([]string, error) {
	params, err := procedureParamList(def, d.paramType, mysqlProcedureSig)
	if err != nil {
		return nil, err
	}
	create := fmt.Sprintf(`CREATE PROCEDURE %s(%s) BEGIN
  %s
END`, def.Name, params, def.Body)
	return []string{create}, nil
}

func (mysqlDialect) dropProcedure(def *procedure.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`DROP PROCEDURE IF EXISTS %s;`, def.Name)}, nil
}

func sqlServerProcedureSig(name, sqlType string) string { return "@" + name + " " + sqlType }

// createProcedureStmt renders the CREATE PROCEDURE statement itself --
// shared by renderProcedure and renderProcedureDeclarative. orAlter is
// "OR ALTER " for the idempotent path, "" for declarative. Unlike a
// function/procedure's parenthesized param list on Postgres/MySQL, T-SQL
// procedure params are listed bare after the name, no parens.
func (sqlServerDialect) createProcedureStmt(def *procedure.Definition, orAlter, params string) string {
	return fmt.Sprintf(`CREATE %sPROCEDURE %s %s AS
BEGIN
  %s
END;`, orAlter, def.Name, params, def.Body)
}

func (d sqlServerDialect) renderProcedure(def *procedure.Definition) ([]string, error) {
	params, err := procedureParamList(def, d.paramType, sqlServerProcedureSig)
	if err != nil {
		return nil, err
	}
	return []string{d.createProcedureStmt(def, "OR ALTER ", params)}, nil
}

func (d sqlServerDialect) renderProcedureDeclarative(def *procedure.Definition) ([]string, error) {
	params, err := procedureParamList(def, d.paramType, sqlServerProcedureSig)
	if err != nil {
		return nil, err
	}
	return []string{d.createProcedureStmt(def, "", params)}, nil
}

func (sqlServerDialect) dropProcedure(def *procedure.Definition) ([]string, error) {
	return []string{fmt.Sprintf(`DROP PROCEDURE IF EXISTS %s;`, def.Name)}, nil
}

var _ procedureDialect = postgresDialect{}
var _ procedureDialect = mysqlDialect{}
var _ procedureDialect = sqlServerDialect{}

var _ triggerDialect = postgresDialect{}
var _ triggerDialect = mysqlDialect{}
var _ triggerDialect = sqliteDialect{}
var _ triggerDialect = sqlServerDialect{}
var _ viewDialect = postgresDialect{}
var _ viewDialect = mysqlDialect{}
var _ viewDialect = sqliteDialect{}
var _ viewDialect = sqlServerDialect{}
