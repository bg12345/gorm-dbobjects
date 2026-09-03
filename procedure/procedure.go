package procedure

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"gorm.io/datatypes"
	"gorm.io/gorm/schema"
)

// rawMessageType identifies a json.RawMessage field (or *json.RawMessage
// -- compared against IndirectFieldType, which gorm already
// pointer-dereferences) so TypeOf can special-case it to JSON. Unlike
// datatypes.JSON, json.RawMessage has no gorm-awareness at all -- it's
// plain []byte to gorm's own classification, which would otherwise
// silently resolve it to Bytes (a valid but semantically wrong column
// type, not an error) rather than something that flags the mismatch.
var rawMessageType = reflect.TypeFor[json.RawMessage]()

// datatypesJSONType identifies a gorm.io/datatypes.JSON field the same
// way rawMessageType identifies json.RawMessage -- a direct concrete
// type comparison against IndirectFieldType, rather than matching on
// the "json" string its GormDataType() method happens to return (gorm
// still resolves that field's schema.Field.DataType to that string
// internally; this package just doesn't rely on it).
var datatypesJSONType = reflect.TypeFor[datatypes.JSON]()

// schemaCache lets repeated TypeOf calls against the same model reuse
// gorm's parsed schema instead of re-parsing every time.
var schemaCache = &sync.Map{}

// ParamKind distinguishes what a ParamType represents, so a dialect can
// resolve it to its own concrete type name (see dialect.go's paramType)
// instead of ParamType baking in one dialect's spelling itself.
type ParamKind int

const (
	ParamInt ParamKind = iota
	ParamText
	ParamBool
	ParamTime
	ParamVarchar
	ParamChar
	ParamDecimal
	ParamFloat
	ParamBytes
	ParamJSON
	ParamRaw // Raw holds the literal SQL type name to emit verbatim
)

// ParamType is a portable SQL parameter type, resolved to each
// dialect's concrete type name at render time. Built via the
// package-level vars/funcs below, or TypeOf to derive one from an
// existing model field -- never constructed directly.
type ParamType struct {
	Kind  ParamKind
	Size  int    // Varchar/Char length, or Decimal precision
	Scale int    // Decimal only
	Raw   string // ParamRaw only

	err error // set by TypeOf for a field kind with no portable mapping; surfaced at Build()
}

var (
	Int   = ParamType{Kind: ParamInt}
	Text  = ParamType{Kind: ParamText}
	Bool  = ParamType{Kind: ParamBool}
	Time  = ParamType{Kind: ParamTime}
	Float = ParamType{Kind: ParamFloat}
	Bytes = ParamType{Kind: ParamBytes}
	// JSON renders as Postgres JSONB (the idiomatic default -- indexable,
	// supports containment operators; use Raw("JSON") instead if the
	// rare need to preserve exact key order/whitespace applies), MySQL's
	// native JSON, and SQL Server's NVARCHAR(MAX) -- SQL Server has no
	// native JSON type at all, storing and querying it (ISJSON,
	// JSON_VALUE, ...) over nvarchar the same way Microsoft's own docs
	// do.
	JSON = ParamType{Kind: ParamJSON}
)

// Varchar/Char are sized-string variants, Decimal is fixed-precision
// numeric -- kept separate from the zero-arg vars above since they take
// parameters. Size/precision/scale validation is deferred to render
// time (each dialect's paramType), same as everything else this
// package leaves to Build()/render rather than checking eagerly.
func Varchar(size int) ParamType { return ParamType{Kind: ParamVarchar, Size: size} }
func Char(size int) ParamType    { return ParamType{Kind: ParamChar, Size: size} }
func Decimal(precision, scale int) ParamType {
	return ParamType{Kind: ParamDecimal, Size: precision, Scale: scale}
}

// Raw is the escape hatch for a type with no portable equivalent
// (Postgres JSONB, SQL Server UNIQUEIDENTIFIER, ...) or a gorm field
// kind TypeOf doesn't map (Uint/Float/Bytes) -- rendered verbatim per
// dialect, caller's responsibility to get it right for whichever engine
// is connected, same trust boundary as Body().
func Raw(sqlType string) ParamType { return ParamType{Kind: ParamRaw, Raw: sqlType} }

// TypeOf derives a ParamType from an existing model field, so the
// param's type tracks the column instead of drifting if it changes
// later. field is the Go struct field name (e.g. "ID"), not the DB
// column name. Recognizes JSON two ways, both by exact concrete type
// rather than gorm's GormDataTypeInterface string: gorm.io/datatypes.JSON
// and json.RawMessage. Neither would otherwise map cleanly -- gorm
// resolves datatypes.JSON's schema.Field.DataType to the custom string
// "json" (not one of this package's own kinds), and json.RawMessage
// (stdlib, no gorm-awareness at all) resolves to plain Bytes, a valid
// but semantically wrong column type, not an error. Errors (surfaced at
// Build()) whenever gorm's own field classification doesn't map to this
// package's small portable set otherwise -- besides Uint (see its own
// case below), that also includes a relation/association field
// (schema.Field.DataType is left empty for those -- there's no single
// scalar column to type; target the FK field directly instead, e.g.
// "UserID" rather than "User") and any other custom gorm data type or
// array type; use Raw() explicitly with the right spelling for the
// engine being targeted.
func TypeOf(model any, field string) ParamType {
	sc, err := schema.Parse(model, schemaCache, schema.NamingStrategy{})
	if err != nil {
		return ParamType{err: fmt.Errorf("procedure: TypeOf: %w", err)}
	}
	f := sc.LookUpField(field)
	if f == nil {
		return ParamType{err: fmt.Errorf("procedure: TypeOf: field %q not found on table %q", field, sc.Table)}
	}
	if f.IndirectFieldType == datatypesJSONType || f.IndirectFieldType == rawMessageType {
		return JSON
	}
	switch f.DataType {
	case schema.Bool:
		return Bool
	case schema.Int:
		return Int
	case schema.String:
		return Text
	case schema.Time:
		return Time
	case schema.Float:
		return Float
	case schema.Bytes:
		return Bytes
	case schema.Uint:
		return ParamType{err: fmt.Errorf("procedure: TypeOf: field %q is Uint, which has no portable equivalent (Postgres and SQL Server have no native unsigned integer type) -- use Raw() directly for it", field)}
	default:
		return ParamType{err: fmt.Errorf("procedure: TypeOf: field %q has no portable equivalent -- this includes relation/association fields (target the FK field directly instead) and types with a custom gorm data type (other than datatypes.JSON) or an array type (use Raw() directly, with the right spelling for the engine being targeted)", field)}
	}
}

// Param is one procedure parameter.
type Param struct {
	Name string
	Type ParamType
}

// Definition is the DB-agnostic description of a procedure, produced
// by Build() and consumed by whatever applies it (e.g.
// dbobjects.Register).
type Definition struct {
	Name   string
	Params []Param
	Body   string // raw full procedure body; always required, no Set()-style alternative exists
}

type Procedure interface {
	Kind() string
	Param(name string, t ParamType) Procedure
	Params(params ...Param) Procedure
	Body(sql string) Procedure
	Build() (*Definition, error)
}

type procedureBuilder struct {
	name   string
	params []Param
	seen   map[string]bool
	body   string
	err    error
}

// New starts building a procedure named name. Unlike trigger/view,
// there's no gorm model to derive a table from -- a procedure isn't
// attached to one table, it may touch zero, one, or several across its
// body.
func New(name string) Procedure {
	return &procedureBuilder{name: name, seen: map[string]bool{}}
}

func (p *procedureBuilder) Param(name string, t ParamType) Procedure {
	if p.err != nil {
		return p
	}
	if p.seen[name] {
		p.err = fmt.Errorf("procedure: duplicate param name %q on procedure %q", name, p.name)
		return p
	}
	p.seen[name] = true
	p.params = append(p.params, Param{Name: name, Type: t})
	return p
}

// Params adds multiple params at once, in the order given -- a slice,
// not a map, since a procedure's params are positional (CALL/EXEC binds
// arguments by declaration order), unlike trigger.SetColumns' SET
// clause where column order carries no meaning; a map here would
// silently reorder the signature between runs. Delegates to Param so
// duplicate-name detection stays in one place.
func (p *procedureBuilder) Params(params ...Param) Procedure {
	for _, param := range params {
		p.Param(param.Name, param.Type)
	}
	return p
}

func (p *procedureBuilder) Body(sql string) Procedure {
	p.body = sql
	return p
}

func (p *procedureBuilder) Build() (*Definition, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.name == "" {
		return nil, fmt.Errorf("procedure: name must not be empty")
	}
	if p.body == "" {
		return nil, fmt.Errorf("procedure: %q has no Body set", p.name)
	}
	for _, param := range p.params {
		if param.Type.err != nil {
			return nil, param.Type.err
		}
	}
	return &Definition{Name: p.name, Params: p.params, Body: p.body}, nil
}

func (p *procedureBuilder) Kind() string {
	return "procedure"
}
