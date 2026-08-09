package view

import (
	"fmt"

	"gorm.io/gorm"
)

type Definition struct {
	Name    string
	QueryFn func(*gorm.DB) *gorm.DB
	RawSQL  string
}

type View interface {
	Kind() string
	Query(fn func(*gorm.DB) *gorm.DB) View
	Raw(sql string) View
	Build() (*Definition, error)
}

type viewBuilder struct {
	name    string
	queryFn func(*gorm.DB) *gorm.DB
	rawSQL  string
}

// New starts building a view named name. Unlike trigger.BeforeUpdate,
// there's no gorm model to derive a table/name from -- a view isn't
// attached to one table the way a trigger is, so the name has to be
// given explicitly.
func New(name string) View {
	return &viewBuilder{name: name}
}

func (v *viewBuilder) Query(fn func(*gorm.DB) *gorm.DB) View {
	v.queryFn = fn
	return v
}

// Raw overrides Query with sql verbatim, for the one engine (or query
// shape) gorm's query builder can't express -- e.g. Oracle's ROWNUM
// instead of LIMIT. If set, it always wins at render time regardless of
// which engine is connected; Query stays the portable default when Raw
// is never called. Callers should only build this from literals they
// wrote themselves, not untrusted input -- same trust boundary as
// trigger.Raw().
func (v *viewBuilder) Raw(sql string) View {
	v.rawSQL = sql
	return v
}

func (v *viewBuilder) Build() (*Definition, error) {
	if v.name == "" {
		return nil, fmt.Errorf("view: name must not be empty")
	}
	if v.queryFn == nil && v.rawSQL == "" {
		return nil, fmt.Errorf("view: %q has no Query or Raw set", v.name)
	}
	return &Definition{
		Name:    v.name,
		QueryFn: v.queryFn,
		RawSQL:  v.rawSQL,
	}, nil
}

func (v *viewBuilder) Kind() string {
	return "view"
}

