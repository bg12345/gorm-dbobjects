package dbobjects

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bg12345/gorm-dbobjects/trigger"
)


type triggerDialect interface {
	renderTrigger(def *trigger.Definition) ([]string, error)
	dropTrigger(def *trigger.Definition) ([]string, error)
}

type dialect interface {
	Name() string
	transactional() bool
}

var dialects = map[string]dialect{
	"postgres": postgresDialect{},
	"mysql":    mysqlDialect{},
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

func (d postgresDialect) renderTrigger(def *trigger.Definition) ([]string, error) {
	fnName, trgName := triggerNames(def)

	columns := make([]string, 0, len(def.Sets))
	for column := range def.Sets {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	var body strings.Builder
	for _, column := range columns {
		fmt.Fprintf(&body, "  NEW.%s = %s;\n", column, d.renderExpr(def.Sets[column]))
	}

	createFn := fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS TRIGGER AS $$
BEGIN
%s  RETURN NEW;
END;
$$ LANGUAGE plpgsql;`, fnName, body.String())

	dropTrg := fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;`, trgName, def.Table)

	createTrg := fmt.Sprintf(`CREATE TRIGGER %s %s %s ON %s
FOR EACH ROW EXECUTE FUNCTION %s();`, trgName, def.Timing, def.Event, def.Table, fnName)

	return []string{createFn, dropTrg, createTrg}, nil
}

func (postgresDialect) dropTrigger(def *trigger.Definition) ([]string, error) {
	fnName, trgName := triggerNames(def)

	return []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;`, trgName, def.Table),
		fmt.Sprintf(`DROP FUNCTION IF EXISTS %s();`, fnName),
	}, nil
}

func (d mysqlDialect) renderTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)

	columns := make([]string, 0, len(def.Sets))
	for column := range def.Sets {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	var body strings.Builder
	for _, column := range columns {
		fmt.Fprintf(&body, "  SET NEW.%s = %s;\n", column, d.renderExpr(def.Sets[column]))
	}

	createStmt := fmt.Sprintf(`CREATE TRIGGER %s %s %s ON %s
FOR EACH ROW BEGIN
%sEND;`, trgName, def.Timing, def.Event, def.Table, body.String())

	dropStmt := fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName)

	return []string{dropStmt, createStmt}, nil
}

func (d mysqlDialect) dropTrigger(def *trigger.Definition) ([]string, error) {
	_, trgName := triggerNames(def)

	return []string{
		fmt.Sprintf(`DROP TRIGGER IF EXISTS %s;`, trgName),
	}, nil
}


var _ triggerDialect = postgresDialect{}
var _ triggerDialect = mysqlDialect{}
