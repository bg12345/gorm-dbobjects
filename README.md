# gorm-dbobjects

[![Tests](https://github.com/bg12345/gorm-dbobjects/actions/workflows/tests.yml/badge.svg)](https://github.com/bg12345/gorm-dbobjects/actions/workflows/tests.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/bg12345/gorm-dbobjects.svg)](https://pkg.go.dev/github.com/bg12345/gorm-dbobjects)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Manage database-side objects — triggers, views, stored procedures —
as Go code, across multiple database engines, on top of [gorm](https://gorm.io).**

## Why

gorm gets you most of the way to a working schema: `AutoMigrate` handles
tables, columns, indexes. It has nothing for the things that live
*inside* the database itself — triggers, views, stored procedures. Most
teams end up hand-writing that SQL, checking it into a `migrations/`
folder, and hoping it stays in sync with the Go structs it's built
around. `gorm-dbobjects` treats those objects as first-class,
type-checked Go values instead: build them against your existing gorm
models, and let a dialect layer generate the correct DDL for whichever
engine you're actually connected to.

```go
tr := trigger.BeforeUpdate(&User{}).
    Set("updated_at", trigger.Now())

if err := dbobjects.Register(ctx, tr); err != nil {
    log.Fatal(err)
}
```

`Set("updated_at", ...)` is checked against `User`'s real gorm schema at
build time — a typo'd column name fails before any SQL is generated, not
as a runtime error against a live database.

## Status

Triggers and views are both implemented and tested end-to-end on
Postgres and MySQL — including a real, verified rollback path for
engines where DDL isn't transactional, and a view built from a live
gorm query callback, not just a raw SQL string. Procedures are designed
but not yet built — see the design notes below.

| | Postgres | MySQL | SQL Server | SQLite | Oracle |
|---|:---:|:---:|:---:|:---:|:---:|
| Triggers | ✅ | ✅ | planned | planned | planned |
| Views | ✅ | ✅ | planned | planned | planned |
| Procedures | planned | planned | planned | planned | planned |

## Install

```sh
go get github.com/bg12345/gorm-dbobjects
```

## Quickstart

```go
package main

import (
    "context"
    "log"

    dbobjects "github.com/bg12345/gorm-dbobjects"
    "github.com/bg12345/gorm-dbobjects/trigger"
    "github.com/bg12345/gorm-dbobjects/view"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
)

type User struct {
    gorm.Model
    Name   string
    Email  string
    Active bool
}

func main() {
    db, err := gorm.Open(postgres.Open("your-dsn"), &gorm.Config{})
    if err != nil {
        log.Fatal(err)
    }

    // Init once, wherever you already set up your *gorm.DB.
    dbobjects.Init(db)

    tr := trigger.BeforeUpdate(&User{}).
        Set("updated_at", trigger.Now())

    if err := dbobjects.Register(context.Background(), tr); err != nil {
        log.Fatal(err)
    }

    // Tear it back down if you ever need to.
    _ = dbobjects.Drop(context.Background(), tr)

    // Or just see the generated SQL without touching the DB.
    stmts, _ := dbobjects.Render([]dbobjects.DBObject{tr}, dbobjects.Idempotent)
    for _, s := range stmts {
        log.Println(s)
    }
}
```

`Register`/`Drop` work the same way regardless of which engine `db` is
connected to — the dialect that generates the actual DDL is resolved
from the connection itself.

Views work the same way, built from a gorm query callback instead of a
raw SQL string — `dbobjects` reuses gorm's own dialect layer to resolve
it, so the same view definition renders correctly on whichever engine
it's registered against:

```go
v := view.New("active_users").
    Query(func(tx *gorm.DB) *gorm.DB {
        return tx.Model(&User{}).Where("active = ?", true)
    })

if err := dbobjects.Register(context.Background(), v); err != nil {
    log.Fatal(err)
}
```

For the one query gorm's builder can't express — Oracle's `ROWNUM`
instead of `LIMIT`, say — `Raw(sql)` overrides `Query` with a literal
string when you need the escape hatch:

```go
v := view.New("recent_signups").
    Query(func(tx *gorm.DB) *gorm.DB {
        return tx.Model(&User{}).Order("created_at desc").Limit(100)
    }).
    Raw("SELECT * FROM (SELECT * FROM users ORDER BY created_at DESC) WHERE ROWNUM <= 100")
```

## How it's built

Each object kind (`trigger`, and eventually `view`, `procedure`) is its
own small package with a fluent builder and zero knowledge of SQL
dialects — `trigger.BeforeUpdate(&User{}).Set(...)` only knows about
gorm schemas. A separate dialect layer, resolved from the connected
`*gorm.DB`'s driver name, turns that dialect-agnostic definition into
the actual `CREATE TRIGGER` (or `CREATE VIEW`, `CREATE PROCEDURE`)
statement for whichever engine is connected — Postgres's separate
trigger function vs. MySQL's inline trigger body vs. SQL Server's
`inserted`/`deleted` pseudo-tables are all real, structural differences
the dialect layer accounts for, not just find-and-replaced SQL syntax.

A real example of what that buys you: `Register` batches multiple
objects into one call. On engines with transactional DDL (Postgres, SQL
Server, SQLite), the whole batch is wrapped in one transaction — a
failure partway through rolls everything back for free. On engines that
implicitly commit DDL statements (MySQL, Oracle), a real transaction
isn't possible at all, so `Register` instead falls back to a best-effort
compensating rollback — reverse-order cleanup of whatever already
succeeded, using a context that survives the original call's
cancellation. Both paths are covered by integration tests against real
databases, not just unit tests against mocked SQL.

## Migration-tool interop

`Register`/`Drop` apply DDL directly — the right tool for local dev,
tests, and CI, or for projects with no separate migration tool. Teams
already using a schema-diffing tool like [Atlas](https://atlasgo.io)
shouldn't call `Register` at boot the same way they shouldn't run
`AutoMigrate` there: it mutates the database outside of Atlas's own
diffed, versioned migration history, and the next `atlas migrate diff`
would see the trigger/view/procedure as unmanaged drift. `Render` exists
for that case instead — it returns the generated DDL as plain strings
without touching the database, meant to feed an external-schema source
the same way [`atlas-provider-gorm`](https://github.com/ariga/atlas-provider-gorm)
already does for gorm's own table schema, so Atlas can diff and version
triggers/views/procedures alongside the rest of your schema instead of
around it.

## Testing

Unit tests run with no setup:

```sh
go test ./...
```

Integration tests connect to real Postgres and MySQL instances,
configured via a `.env` file at the repo root (see `.env.example`) —
they skip themselves automatically if no database is reachable. CI runs
both, via service containers, on every push.

## License

[MIT](LICENSE)
