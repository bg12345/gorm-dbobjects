package dbobjects

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bg12345/gorm-dbobjects/trigger"
)


type triggerDialect interface {
	renderTrigger(def *trigger.Definition) (string, error)
	dropTrigger(def *trigger.Definition) (string, error)
}

type dialect interface {
	Name() string
}

var dialects = map[string]dialect{
	"postgres": postgresDialect{},
}

func dialectFor(name string) (dialect, bool) {
	d, ok := dialects[name]
	return d, ok
}

type postgresDialect struct{}

func (postgresDialect) Name() string {
	return "postgres"
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

func (postgresDialect) renderTrigger(def *trigger.Definition) (string, error) {
	fnName, trgName := triggerNames(def)

	columns := make([]string, 0, len(def.Sets))
	for column := range def.Sets {
		columns = append(columns, column)
	}
	sort.Strings(columns)

	var body strings.Builder
	for _, column := range columns {
		fmt.Fprintf(&body, "  NEW.%s = %s;\n", column, def.Sets[column].Raw)
	}

	return fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS TRIGGER AS $$
BEGIN
%s  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS %s ON %s;
CREATE TRIGGER %s %s %s ON %s
FOR EACH ROW EXECUTE FUNCTION %s();`,
		fnName,
		body.String(),
		trgName, def.Table,
		trgName, def.Timing, def.Event, def.Table,
		fnName,
	), nil
}

func (postgresDialect) dropTrigger(def *trigger.Definition) (string, error) {
	fnName, trgName := triggerNames(def)

	return fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s;
DROP FUNCTION IF EXISTS %s();`,
		trgName, def.Table,
		fnName,
	), nil
}

var _ triggerDialect = postgresDialect{}
