package dbobjects

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bg12345/gorm-dbobjects/trigger"
)

// dialect renders a trigger.Definition into the DDL for one database
// engine. Register picks a dialect from the connected DB's driver name
// (gorm.DB.Name()), so adding a new engine never touches the trigger
// package's public API or Register's signature.
type dialect interface {
	render(def *trigger.Definition) string
}

// dialects is keyed by gorm.DB.Name() (e.g. "postgres", "mysql").
// Only postgres is implemented today; unknown names are rejected by
// Register with an explicit error rather than silently emitting the
// wrong SQL.
var dialects = map[string]dialect{
	"postgres": postgresDialect{},
}

func dialectFor(name string) (dialect, bool) {
	d, ok := dialects[name]
	return d, ok
}

type postgresDialect struct{}

func (postgresDialect) render(def *trigger.Definition) string {
	timing := strings.ToLower(def.Timing)
	event := strings.ToLower(def.Event)
	fnName := fmt.Sprintf("fn_%s_%s_%s", def.Table, timing, event)
	trgName := fmt.Sprintf("trg_%s_%s_%s", def.Table, timing, event)

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
	)
}
