package dbobjects

import (
	"context"
	"fmt"

	"github.com/bg12345/gorm-dbobjects/trigger"
	"gorm.io/gorm"
)

type DBObject interface {
	Kind() string
}

var db *gorm.DB

// Init registers the *gorm.DB that Register applies trigger DDL against.
func Init(conn *gorm.DB) {
	db = conn
}

// resolveDialect returns the dialect configured via Init, or an error
// naming op ("Register"/"Drop"/"Render") for a clearer message.
func resolveDialect(op string) (dialect, error) {
	if db == nil {
		return nil, fmt.Errorf("dbobjects: Init must be called before %s", op)
	}
	d, ok := dialectFor(db.Name())
	if !ok {
		return nil, fmt.Errorf("dbobjects: unsupported dialect %q", db.Name())
	}
	return d, nil
}

// ddlOp selects which dialect method resolveDDL calls for a kind --
// its create/replace method (opRender) or its drop method (opDrop).
type ddlOp int

const (
	opRender ddlOp = iota
	opDrop
)

// resolveDDL builds obj and renders the DDL for it against d, per op.
// desc is a short, kind-formatted description of obj (e.g. `trigger on
// "user_master"`) for the caller's own error wrapping -- each kind case
// formats desc from whatever fields its concrete Definition actually
// has (trigger has Table; a future procedure has no table at all, per
// docs/PLAN.md §3.3), so nothing forces a field across kinds that don't
// share one.
func resolveDDL(d dialect, obj DBObject, op ddlOp) (sql, desc, name string, err error) {
	switch o := obj.(type) {
	case trigger.Trigger:
		def, err := o.Build()
		if err != nil {
			return "", "", "", err
		}
		td, ok := d.(triggerDialect)
		if !ok {
			return "", "", "", fmt.Errorf("dbobjects: dialect %q does not support triggers", d.Name())
		}
		_, trgName := triggerNames(def)
		desc = fmt.Sprintf("trigger on %q", def.Table)
		if op == opDrop {
			sql, err = td.dropTrigger(def)
		} else {
			sql, err = td.renderTrigger(def)
		}
		return sql, desc, trgName, err
	default:
		return "", "", "", fmt.Errorf("dbobjects: unsupported object kind %q", obj.Kind())
	}
}

type resolved struct {
	sql  string
	desc string
}

// Register builds each object's definition and executes the resulting
// DDL against the DB configured via Init, using the dialect that matches
// that DB's driver (see dialect.go).
func Register(ctx context.Context, objects ...DBObject) error {
	d,err := resolveDialect("Register")
	if err != nil {
		return err
	}
	results:=make([]resolved, len(objects))
	seen:=make(map[string]int, len(objects))
	for i, obj := range objects {
		sql, desc, name, err := resolveDDL(d, obj, opRender)
		if err != nil {
			return err
		}
		if first,ok:=seen[name]; ok {
			return fmt.Errorf("dbobjects: objects %d and %d both resolve to name %q", first, i, name)
		}
		seen[name] = i
		results[i] = resolved{sql: sql, desc: desc}
	}
	// Transactional on engines with transactional DDL (Postgres, SQL
	// Server, SQLite) -- a ctx cancellation or a later statement's error
	// rolls back everything already applied in this call. On engines
	// that implicitly commit DDL (MySQL, Oracle), this wrapper can't
	// undo a statement that's already forced its own commit.
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, r := range results {
			if err := tx.Exec(r.sql).Error; err != nil {
				return fmt.Errorf("dbobjects: applying %s: %w", r.desc, err)
			}
		}
		return nil
	})
}


// Drop removes each object's corresponding DB-side definition (trigger,
// and later view/procedure) from the DB configured via Init. Safe to
// call for objects that were never registered -- dialect DropTrigger
// implementations use DROP ... IF EXISTS.
func Drop(ctx context.Context, objects ...DBObject) error {
	d, err := resolveDialect("Drop")
	if err != nil {
		return err
	}
	for _, obj := range objects {
		sql, desc, _, err := resolveDDL(d, obj, opDrop)
		if err != nil {
			return err
		}
		if err := db.WithContext(ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("dbobjects: dropping %s: %w", desc, err)
		}
	}
	return nil
}

// RenderMode selects the DDL flavor Render produces (see docs/PLAN.md §3.5).
type RenderMode int

const (
	// Idempotent is safe to execute directly and repeatedly -- CREATE OR
	// REPLACE, DROP ... IF EXISTS. The same DDL Register/Drop execute.
	Idempotent RenderMode = iota
	// Declarative strips conditionals down to bare CREATE statements, for
	// tools that parse and diff DDL structurally (e.g. Atlas). Reserved
	// for the migration-tool interop work in docs/PLAN.md §3.6 -- not
	// implemented for any dialect yet.
	Declarative
)

// Render returns the DDL for each object without executing it.
func Render(objects []DBObject, mode RenderMode) ([]string, error) {
	d, err := resolveDialect("Render")
	if err != nil {
		return nil, err
	}
	if mode == Declarative {
		return nil, fmt.Errorf("dbobjects: Declarative render mode is not implemented yet")
	}

	out := make([]string, 0, len(objects))
	for _, obj := range objects {
		sql, _,_, err := resolveDDL(d, obj, opRender)
		if err != nil {
			return nil, err
		}
		out = append(out, sql)
	}
	return out, nil
}

