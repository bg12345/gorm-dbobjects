// Package trigger builds database triggers as type-checked Go values,
// independent of any SQL dialect. BeforeInsert/BeforeUpdate/
// BeforeDelete/AfterInsert/AfterUpdate/AfterDelete start a trigger on a
// gorm model; Set/SetColumns assign NEW columns, validated against the
// model's real gorm schema at Build time, and Body supplies raw SQL for
// anything Set can't express -- required for DELETE triggers, since
// there's no NEW row to assign into. InsertInto, UpdateInto and
// DeleteFrom change another table when the trigger fires, taking values
// from the firing row via Column and Previous.
//
//	tr := trigger.BeforeUpdate(&User{}).Set("updated_at", trigger.Now())
//
// A built Definition is dialect-agnostic; github.com/bg12345/gorm-dbobjects's
// Client resolves it to the connected engine's actual DDL.
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
	ExprColumn               // a column of the row that fired the trigger; Raw holds its name
	ExprPrevious             // a column's value before an UPDATE; Raw holds its name
)

// Expr represents a value assigned to a column inside the trigger body.
// Raw is written into the generated SQL verbatim, so callers should only
// build it via Now()/Raw() rather than embedding untrusted input.
type Expr struct {
	Kind ExprKind
	Raw  string
}

// ActionOp is what an Action does to its table.
type ActionOp int

const (
	OpInsert ActionOp = iota
	OpUpdate
	OpDelete
)

// Action describes a change to another table made when the trigger fires.
type Action struct {
	Op     ActionOp
	Table  string
	Values map[string]Expr // columns to insert, or to set; empty for OpDelete
	Match  map[string]Expr // columns that must equal these; empty for OpInsert
}

// Now renders as the Postgres NOW() function.
func Now() Expr {
	return Expr{Kind: ExprNow}
}

// Raw renders sql verbatim in the trigger body, e.g. Raw("version + 1").
func Raw(sql string) Expr {
	return Expr{Kind: ExprRaw, Raw: sql}
}

// Column refers to a column of the row that fired the trigger: the new
// values on INSERT and UPDATE, the removed row on DELETE. Only valid in
// InsertInto, UpdateInto and DeleteFrom.
//
// On a BeforeInsert trigger, a database-generated value such as an
// auto-increment id may not be assigned yet (MySQL, SQLite); use
// AfterInsert to record it.
func Column(name string) Expr {
	return Expr{Kind: ExprColumn, Raw: name}
}

// Previous refers to the value a column of the firing row had before an
// UPDATE. Only valid on an UPDATE trigger -- on DELETE,
// Column already refers to the removed row.
//
// On SQL Server, using Previous together with Column matches old and new
// rows by primary key, so a row whose primary key was itself changed by
// the update is not inserted.
func Previous(name string) Expr {
	return Expr{Kind: ExprPrevious, Raw: name}
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
	Action *Action // optional; a change to another table

	// PrimaryKey is the table's single primary key column, resolved
	// best-effort whenever Sets or Action is set (regardless of Timing) and
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
	InsertInto(model any, columns map[string]Expr) Trigger
	UpdateInto(model any, set, match map[string]Expr) Trigger
	DeleteFrom(model any, match map[string]Expr) Trigger
}

type triggerBuilder struct {
	schema *schema.Schema
	timing string
	event  string
	sets   map[string]Expr
	err    error
	name   string
	body   string
	action *Action
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
	if value.isRowRef() {
		t.err = errRowRefInSet(column)
		return t
	}
	t.sets[column] = value
	return t
}

func (e Expr) isRowRef() bool {
	return e.Kind == ExprColumn || e.Kind == ExprPrevious
}

func errRowRefInSet(column string) error {
	return fmt.Errorf("trigger: column %q: Column and Previous are only valid in InsertInto, UpdateInto and DeleteFrom", column)
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
		if value.isRowRef() {
			t.err = errRowRefInSet(column)
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
	if len(t.sets) == 0 && t.body == "" && t.action == nil {
		return nil, fmt.Errorf("trigger: no Set, Body, InsertInto, UpdateInto or DeleteFrom provided for table %q", t.schema.Table)
	}
	if t.action != nil && (len(t.sets) > 0 || t.body != "") {
		return nil, fmt.Errorf("trigger: InsertInto, UpdateInto and DeleteFrom cannot be combined with Set or Body on table %q", t.schema.Table)
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
	if len(t.sets) > 0 || t.action != nil {
		switch {
		case len(t.schema.PrimaryFields) == 1:
			pk = t.schema.PrimaryFields[0].DBName
		case t.timing == "AFTER" && len(t.sets) > 0:
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
		Action:     t.action,
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

// InsertInto makes the trigger add one row to model's table for each row
// that fires it. columns maps the target table's columns to values:
// Column or Previous for the firing row, Now, or Raw. Column names on
// both tables are validated against their gorm schemas; Raw is not.
// Only one of InsertInto, UpdateInto and DeleteFrom can be used, and not
// together with Set or Body.
func (t *triggerBuilder) InsertInto(model any, columns map[string]Expr) Trigger {
	return t.addAction(OpInsert, model, columns, nil)
}

// UpdateInto makes the trigger update the rows of model's table where
// every column in match equals its value. match must refer to the firing
// row through Column or Previous at least once.
//
// On SQL Server, if one statement changes several rows that match the
// same target row, only one of them is applied, so don't accumulate
// values such as Raw("total + 1") there.
func (t *triggerBuilder) UpdateInto(model any, set, match map[string]Expr) Trigger {
	return t.addAction(OpUpdate, model, set, match)
}

// DeleteFrom makes the trigger delete the rows of model's table where
// every column in match equals its value. match must refer to the firing
// row through Column or Previous at least once.
func (t *triggerBuilder) DeleteFrom(model any, match map[string]Expr) Trigger {
	return t.addAction(OpDelete, model, nil, match)
}

func (t *triggerBuilder) addAction(op ActionOp, model any, values, match map[string]Expr) Trigger {
	if t.err != nil {
		return t
	}
	if t.action != nil {
		t.err = fmt.Errorf("trigger: only one of InsertInto, UpdateInto and DeleteFrom is allowed on table %q", t.schema.Table)
		return t
	}
	sc, err := schema.Parse(model, schemaCache, schema.NamingStrategy{})
	if err != nil {
		t.err = err
		return t
	}
	if sc.Table == t.schema.Table {
		t.err = fmt.Errorf("trigger: cannot target the trigger's own table %q", sc.Table)
		return t
	}
	if op != OpDelete && len(values) == 0 {
		t.err = fmt.Errorf("trigger: no columns to write on table %q", sc.Table)
		return t
	}
	// Without a condition tied to the firing row, an update or delete
	// would hit unrelated rows, up to the whole table.
	if op != OpInsert && !hasRowRef(match) {
		t.err = fmt.Errorf("trigger: match on table %q needs at least one Column or Previous", sc.Table)
		return t
	}
	for _, columns := range []map[string]Expr{values, match} {
		for col, val := range columns {
			if _, ok := sc.FieldsByDBName[col]; !ok {
				t.err = fmt.Errorf("trigger: column %q not found on table %q", col, sc.Table)
				return t
			}
			if !val.isRowRef() {
				continue
			}
			if _, ok := t.schema.FieldsByDBName[val.Raw]; !ok {
				t.err = fmt.Errorf("trigger: column %q not found on table %q", val.Raw, t.schema.Table)
				return t
			}
			if val.Kind == ExprPrevious && t.event != "UPDATE" {
				t.err = fmt.Errorf("trigger: Previous is only valid on an UPDATE trigger, not %s", t.event)
				return t
			}
		}
	}
	t.action = &Action{Op: op, Table: sc.Table, Values: values, Match: match}
	return t
}

func hasRowRef(columns map[string]Expr) bool {
	for _, e := range columns {
		if e.isRowRef() {
			return true
		}
	}
	return false
}
