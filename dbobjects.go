package dbobjects

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bg12345/gorm-dbobjects/trigger"
	"github.com/bg12345/gorm-dbobjects/view"
	"gorm.io/gorm"
)

// compensationTimeout bounds how long the best-effort rollback pass
// (compensate) is allowed to run when a non-transactional-DDL engine's
// Register call fails partway through a batch. It survives the
// original ctx's cancellation/expiry (that's the whole point -- ctx
// may be exactly what just caused the failure), but still can't hang
// forever if the DB connection itself is in a bad state.
const compensationTimeout = 10 * time.Second

type DBObject interface {
	Kind() string
}

type Client struct {
	db *gorm.DB
}

func NewClient(conn *gorm.DB) *Client {
	return &Client{db: conn}
}

// resolveDialect returns the dialect for the Client's configured DB, or
// an error naming op ("Register"/"Drop"/"Render") for a clearer message.
func (c *Client) resolveDialect(op string) (dialect, error) {
	if c.db == nil {
		return nil, fmt.Errorf("dbobjects: Client has no *gorm.DB set; construct one via NewClient before calling %s", op)
	}
	d, ok := dialectFor(c.db.Name())
	if !ok {
		return nil, fmt.Errorf("dbobjects: unsupported dialect %q", c.db.Name())
	}
	return d, nil
}

// ddlOp selects which dialect method resolveDDL calls for a kind: its
// idempotent create/replace method (opRender), its bare-CREATE
// structural-DDL method (opRenderDeclarative, see RenderMode), or its
// drop method (opDrop).
type ddlOp int

const (
	opRender ddlOp = iota
	opRenderDeclarative
	opDrop
)

// resolveDDL builds obj and renders the DDL for it against d, per op.
// desc is a short, kind-formatted description of obj (e.g. `trigger on
// "user_master"`) for the caller's own error wrapping -- each kind case
// formats desc from whatever fields its concrete Definition actually
// has (trigger has Table; a future procedure has no table at all), so
// nothing forces a field across kinds that don't share one.
func (c *Client) resolveDDL(d dialect, obj DBObject, op ddlOp) (stmts []string, desc, name string, err error) {
	switch o := obj.(type) {
	case trigger.Trigger:
		def, err := o.Build()
		if err != nil {
			return nil, "", "", err
		}
		td, ok := d.(triggerDialect)
		if !ok {
			return nil, "", "", fmt.Errorf("dbobjects: dialect %q does not support triggers", d.Name())
		}
		_, trgName := triggerNames(def)
		desc = fmt.Sprintf("trigger on %q", def.Table)
		switch op {
		case opDrop:
			stmts, err = td.dropTrigger(def)
		case opRenderDeclarative:
			stmts, err = td.renderTriggerDeclarative(def)
		default:
			stmts, err = td.renderTrigger(def)
		}
		return stmts, desc, trgName, err
	case view.View:
		def, err := o.Build()
		if err != nil {
			return nil, "", "", err
		}
		vd, ok := d.(viewDialect)
		if !ok {
			return nil, "", "", fmt.Errorf("dbobjects: dialect %q does not support views", d.Name())
		}
		desc = fmt.Sprintf("view %q", def.Name)
		switch op {
		case opDrop:
			stmts, err = vd.dropView(def)
		case opRenderDeclarative:
			stmts, err = vd.renderViewDeclarative(c.db, def)
		default:
			stmts, err = vd.renderView(c.db, def)
		}
		return stmts, desc, def.Name, err
	default:
		return nil, "", "", fmt.Errorf("dbobjects: unsupported object kind %q", obj.Kind())
	}
}

type resolved struct {
	stmts     []string
	dropStmts []string
	desc      string
}

// compensate best-effort drops each of applied's objects, in reverse
// order, after a Register call failed partway through on a
// non-transactional-DDL engine (§2.5) -- creation isn't atomic there,
// so this is the closest approximation available. Uses a context
// derived from ctx that ignores its cancellation/deadline (ctx may be
// exactly what just expired) but is bounded by its own short timeout.
// Never stops early -- one object's compensation failing doesn't
// predict whether the others will too. Returns one error per object
// whose compensation itself failed.
func (c *Client) compensate(ctx context.Context, applied []resolved) []error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
	defer cancel()

	var errs []error
	for _, r := range slices.Backward(applied) {
		for _, stmt := range r.dropStmts {
			if err := c.db.WithContext(cleanupCtx).Exec(stmt).Error; err != nil {
				errs = append(errs, fmt.Errorf("compensating %s: %w", r.desc, err))
				break // this object's cleanup failed; move to the next one
			}
		}
	}
	return errs
}

// Register builds each object's definition and executes the resulting
// DDL against the Client's DB, using the dialect that matches that DB's
// driver (see dialect.go).
func (c *Client) Register(ctx context.Context, objects ...DBObject) error {
	d,err := c.resolveDialect("Register")
	if err != nil {
		return err
	}
	results:=make([]resolved, len(objects))
	seen:=make(map[string]int, len(objects))
	for i, obj := range objects {
		stmts, desc, name, err := c.resolveDDL(d, obj, opRender)
		if err != nil {
			return err
		}
		if first,ok:=seen[name]; ok {
			return fmt.Errorf("dbobjects: objects %d and %d both resolve to name %q", first, i, name)
		}
		seen[name] = i

		dropStmts, _, _, err := c.resolveDDL(d, obj, opDrop)
		if err != nil {
			return err
		}

		results[i] = resolved{stmts: stmts, dropStmts: dropStmts, desc: desc}
	}

	// Transactional on engines with transactional DDL (Postgres, SQL
	// Server, SQLite) -- a ctx cancellation or a later statement's error
	// rolls back everything already applied in this call. On engines
	// that implicitly commit DDL (MySQL, Oracle), this wrapper can't
	// undo a statement that's already forced its own commit.
	if d.transactional() {
		return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, r := range results {
				for _, stmt := range r.stmts {
					if err := tx.Exec(stmt).Error; err != nil {
						return fmt.Errorf("dbobjects: applying %s: %w", r.desc, err)
					}
				}
			}
			return nil
		})
	}

	for i, r := range results {
		for _, stmt := range r.stmts {
			if err := c.db.WithContext(ctx).Exec(stmt).Error; err != nil {
				applyErr := fmt.Errorf("dbobjects: applying %s: %w", r.desc, err)
				compErrs := c.compensate(ctx, results[:i+1])
				if len(compErrs) == 0 {
					return applyErr
				}
				return fmt.Errorf("%w; additionally, best-effort rollback failed for %d object(s): %w",
					applyErr, len(compErrs), errors.Join(compErrs...))
			}
		}
	}
	return nil
}


// Drop removes each object's corresponding DB-side definition (trigger,
// and later view/procedure) from the Client's DB. Safe to
// call for objects that were never registered -- dialect DropTrigger
// implementations use DROP ... IF EXISTS.
func (c *Client) Drop(ctx context.Context, objects ...DBObject) error {
	d, err := c.resolveDialect("Drop")
	if err != nil {
		return err
	}
	for _, obj := range objects {
		stmts, desc, _, err := c.resolveDDL(d, obj, opDrop)
		if err != nil {
			return err
		}
		for _, stmt := range stmts {
			if err := c.db.WithContext(ctx).Exec(stmt).Error; err != nil {
				return fmt.Errorf("dbobjects: dropping %s: %w", desc, err)
			}
		}
	}
	return nil
}

// RenderMode selects the DDL flavor Render produces.
type RenderMode int

const (
	// Idempotent is safe to execute directly and repeatedly -- CREATE OR
	// REPLACE, DROP ... IF EXISTS. The same DDL Register/Drop execute.
	Idempotent RenderMode = iota
	// Declarative strips conditionals down to bare CREATE statements (no
	// OR REPLACE, no DROP), for tools that parse and diff DDL
	// structurally themselves (e.g. Atlas as an external schema source)
	// rather than executing conditional DDL directly.
	// Not meant for repeated direct execution: re-running the same
	// Declarative output twice against a live DB will error on the
	// second run (object already exists) -- that's correct, expected
	// behavior for this mode, not a bug. Use Idempotent for anything
	// that needs to be safely re-runnable.
	Declarative
)

// Render returns the DDL for each object without executing it.
func (c *Client) Render(objects []DBObject, mode RenderMode) ([]string, error) {
	d, err := c.resolveDialect("Render")
	if err != nil {
		return nil, err
	}

	op := opRender
	if mode == Declarative {
		op = opRenderDeclarative
	}

	out := make([]string, 0, len(objects))
	for _, obj := range objects {
		stmts, _,_, err := c.resolveDDL(d, obj, op)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.Join(stmts, "\n\n"))
	}
	return out, nil
}

