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
	Event  string // "INSERT", "UPDATE", or "DELETE"
	Sets   map[string]Expr
	Body   string // raw full trigger body; required for DELETE (no NEW row to assign into)
	Name   string // optional; if empty, dbobjects will generate one

	// PrimaryKey is the table's single primary key column, resolved
	// best-effort whenever Sets is non-empty (regardless of Timing) and
	// the table has exactly one primary key field; empty otherwise. Used
	// by dialects that render Set() as a follow-up UPDATE targeting this
	// column instead of NEW.col = expr -- Postgres/MySQL for AFTER
	// triggers (NEW isn't assignable once an AFTER trigger fires, the
	// row's already written) and SQL Server for every Set()-based
	// trigger (SQL Server has no BEFORE DML trigger at all, so Set()
	// always renders AFTER there). Unused by SQLite, which targets
	// rowid instead. See dialect.go's renderTrigger per dialect.
	PrimaryKey string
}

type Trigger interface {
	Kind() string
	Set(column string, value Expr) Trigger
	SetColumns(columns map[string]Expr) Trigger
	Body(sql string) Trigger
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
	body   string
}

func newTrigger(model any, timing, event string) Trigger {
	sc, err := schema.Parse(model, schemaCache, schema.NamingStrategy{})
	return &triggerBuilder{
		schema: sc,
		timing: timing,
		event:  event,
		sets:   map[string]Expr{},
		err:    err,
		body:   "",
	}
}

func BeforeInsert(model any) Trigger {
	return newTrigger(model, "BEFORE", "INSERT")
}

func BeforeUpdate(model any) Trigger {
	return newTrigger(model, "BEFORE", "UPDATE")
}

func AfterInsert(model any) Trigger {
	return newTrigger(model, "AFTER", "INSERT")
}

func AfterUpdate(model any) Trigger {
	return newTrigger(model, "AFTER", "UPDATE")
}

func BeforeDelete(model any) Trigger {
	return newTrigger(model, "BEFORE", "DELETE")
}

func AfterDelete(model any) Trigger {
	return newTrigger(model, "AFTER", "DELETE")
}

func (t *triggerBuilder) Body(sql string) Trigger {
	t.body = sql
	return t
}


func (t *triggerBuilder) Set(column string, value Expr) Trigger {
	if t.err != nil {
		return t
	}
	if t.event == "DELETE" {
		t.err = fmt.Errorf("trigger: Set is not valid on a DELETE trigger (no NEW row); use Body instead")
		return t
	}
	if _, ok := t.schema.FieldsByDBName[column]; !ok {
		t.err = fmt.Errorf("trigger: column %q not found on table %q", column, t.schema.Table)
		return t
	}
	t.sets[column] = value
	return t
}

func (t *triggerBuilder) SetColumns(columns map[string]Expr) Trigger {
	if t.err != nil {
		return t
	}
	if t.event == "DELETE" {
		t.err = fmt.Errorf("trigger: Set is not valid on a DELETE trigger (no NEW row); use Body instead")
		return t
	}
	for column, value := range columns {
		if _, ok := t.schema.FieldsByDBName[column]; !ok {
			t.err = fmt.Errorf("trigger: column %q not found on table %q", column, t.schema.Table)
			return t
		}
		t.sets[column] = value
	}
	return t
}

func (t *triggerBuilder) Build() (*Definition, error) {
	if t.err != nil {
		return nil, t.err
	}
	if len(t.sets) == 0 && t.body == "" {
		return nil, fmt.Errorf("trigger: no columns set and no Body provided for table %q", t.schema.Table)
	}

	// Resolved best-effort whenever Set() was used, regardless of
	// declared timing: Postgres/MySQL's AFTER-trigger follow-up UPDATE
	// needs it (see Definition.PrimaryKey / dialect.go's renderTrigger),
	// and so does SQL Server's Set()-based UPDATE ... FROM join, which
	// forces AFTER under the hood even for a BEFORE-declared trigger
	// (SQL Server has no BEFORE DML trigger at all). Only AFTER hard-
	// errors here, exactly as before -- Postgres/MySQL never read
	// PrimaryKey on a BEFORE-declared trigger (NEW.col = expr doesn't
	// need it) and SQLite never reads it at all (uses rowid instead), so
	// leaving it unset/empty for a BEFORE-declared trigger on a
	// composite-or-missing-PK table stays harmless for every dialect
	// that doesn't need it, and SQL Server's own dialect surfaces its
	// own render-time error when it does need it and finds it empty.
	var pk string
	if len(t.sets) > 0 {
		switch {
		case len(t.schema.PrimaryFields) == 1:
			pk = t.schema.PrimaryFields[0].DBName
		case t.timing == "AFTER":
			return nil, fmt.Errorf("trigger: AFTER trigger with Set() requires exactly one primary key column on table %q, found %d",
				t.schema.Table, len(t.schema.PrimaryFields))
		}
	}

	return &Definition{
		Table:      t.schema.Table,
		Timing:     t.timing,
		Event:      t.event,
		Sets:       t.sets,
		Body:       t.body,
		Name:       t.name,
		PrimaryKey: pk,
	}, nil
}


func (t *triggerBuilder) Kind() string {
	return "trigger"
}

func (t *triggerBuilder) Name(n string) Trigger {
	t.name = n
	return t
}