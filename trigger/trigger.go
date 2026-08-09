package trigger

import (
	"fmt"
	"sync"
	"gorm.io/gorm/schema"
)

// schemaCache lets repeated BeforeInsert/BeforeUpdate calls on the same
// model reuse gorm's parsed schema instead of re-parsing every time.
var schemaCache = &sync.Map{}

// ExprKind distinguishes what an Expr represents, so a dialect can
// render it in its own engine's spelling (Postgres/MySQL's NOW() vs
// SQL Server's SYSUTCDATETIME() vs Oracle's SYSDATE, for ExprNow)
// instead of Expr baking in one dialect's spelling itself.
type ExprKind int

const (
	ExprRaw ExprKind = iota // zero value: Raw holds the literal SQL to emit verbatim
	ExprNow                  // "current timestamp" -- each dialect resolves its own spelling
)

// Expr represents a value assigned to a column inside the trigger body.
// Raw is written into the generated SQL verbatim, so callers should only
// build it via Now()/Raw() rather than embedding untrusted input.
type Expr struct {
	Kind ExprKind
	Raw  string
}

// Now renders as the Postgres NOW() function.
func Now() Expr {
	return Expr{Kind: ExprNow}
}

// Raw renders sql verbatim in the trigger body, e.g. Raw("version + 1").
func Raw(sql string) Expr {
	return Expr{Kind: ExprRaw, Raw: sql}
}

// Definition is the DB-agnostic description of a trigger, produced by
// Build() and consumed by whatever applies it (e.g. dbobjects.Register).
type Definition struct {
	Table  string
	Timing string // "BEFORE" or "AFTER"
	Event  string // "INSERT" or "UPDATE"
	Sets   map[string]Expr
	Name   string // optional; if empty, dbobjects will generate one
}

type Trigger interface {
	Kind() string
	Set(column string, value Expr) Trigger
	Name(n string) Trigger
	Build() (*Definition, error)
}

type triggerBuilder struct {
	schema *schema.Schema
	timing string
	event  string
	sets   map[string]Expr
	err    error
	name   string
}

func newTrigger(model any, timing, event string) Trigger {
	sc, err := schema.Parse(model, schemaCache, schema.NamingStrategy{})
	return &triggerBuilder{
		schema: sc,
		timing: timing,
		event:  event,
		sets:   map[string]Expr{},
		err:    err,
	}
}

func BeforeInsert(model any) Trigger {
	return newTrigger(model, "BEFORE", "INSERT")
}

func BeforeUpdate(model any) Trigger {
	return newTrigger(model, "BEFORE", "UPDATE")
}

func (t *triggerBuilder) Set(column string, value Expr) Trigger {
	if t.err != nil {
		return t
	}
	if _, ok := t.schema.FieldsByDBName[column]; !ok {
		t.err = fmt.Errorf("trigger: column %q not found on table %q", column, t.schema.Table)
		return t
	}
	t.sets[column] = value
	return t
}

func (t *triggerBuilder) Build() (*Definition, error) {
	if t.err != nil {
		return nil, t.err
	}
	if len(t.sets) == 0 {
		return nil, fmt.Errorf("trigger: no columns set for table %q", t.schema.Table)
	}
	return &Definition{
		Table:  t.schema.Table,
		Timing: t.timing,
		Event:  t.event,
		Sets:   t.sets,
		Name:   t.name,
	}, nil
}


func (t *triggerBuilder) Kind() string {
	return "trigger"
}

func (t *triggerBuilder) Name(n string) Trigger {
	t.name = n
	return t
}