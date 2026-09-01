# Changelog

All notable changes to this project are documented here. Format is
loosely based on [Keep a Changelog](https://keepachangelog.com/), dates
are release-tag dates.

## [Unreleased]

### Added
- Stored procedures on Postgres, MySQL, and SQL Server —
  `procedure.New(name).Param(...).Body(...)`, or `Params(...)` to add
  several at once (a variadic slice, not a map — a procedure's params
  are positional, unlike a trigger's order-independent `SET` clause).
  `dbobjects` generates the
  signature/wrapper ceremony per engine (`CREATE OR REPLACE PROCEDURE`,
  `DROP IF EXISTS` + `CREATE`, `CREATE OR ALTER PROCEDURE`); the body
  itself stays a single caller-authored SQL string, since control flow
  (loops, cursors, exception handling) can't be abstracted portably the
  way a trigger's flat column assignment can. `Param` types are a small
  portable set (`Int`/`Text`/`Bool`/`Time`/`Varchar(n)`/`Char(n)`/
  `Decimal(p,s)`/`Float`/`Bytes`/`JSON`), a `Raw(sqlType)` escape hatch,
  or `TypeOf(&Model{}, "Field")` deriving one from an existing gorm
  field — including recognizing `gorm.io/datatypes.JSON` fields (a real
  dependency now, matched by exact type) and `json.RawMessage`
  specifically as `JSON` (renders as Postgres `JSONB`, MySQL's native
  `JSON`, SQL Server's `NVARCHAR(MAX)` — no native JSON type there at
  all; `json.RawMessage` needed its own case since it's plain `[]byte`
  to gorm with no gorm-awareness, which would otherwise silently
  resolve to `Bytes` instead). Errors
  for a `Uint` field specifically — Postgres/SQL Server have no native
  unsigned integer type at all. Not supported on
  SQLite, which has no stored procedure concept at all — rejected via
  the same dialect-capability mechanism trigger/view already use, no
  SQLite-specific code needed.

## [v0.5.0] - 2026-08-31

### Added
- SQL Server trigger + view dialect. SQL Server has no `BEFORE` DML
  trigger at all (only `AFTER`/`FOR` and `INSTEAD OF`, deliberately
  unused) and triggers are set-based over `inserted`/`deleted`
  pseudo-tables, not row-based `NEW`/`OLD` — `Set()` always renders
  `AFTER` via a set-based `UPDATE ... FROM inserted` join on
  `Definition.PrimaryKey`, recursion is guarded by the real
  `TRIGGER_NESTLEVEL()` engine builtin (no guard table needed, unlike
  SQLite), and `Body()` on a `BEFORE`-declared trigger is a render-time
  error rather than a silent timing coercion, since SQL Server has no
  mechanism that makes that safe.
- `trigger.Build()`'s `PrimaryKey` resolution widened to run whenever
  `Set()` is used, on any declared timing (previously `AFTER`-only) —
  additive, required by SQL Server's forced-`AFTER` translation, and
  behavior-preserving for every existing dialect.

## [v0.4.0] - 2026-08-17

### Added
- SQLite trigger + view dialect. SQLite can't assign to `NEW.col` on
  *any* timing (not just `AFTER`, unlike Postgres/MySQL) — `Set()`
  always renders as a literal `AFTER INSERT`/`AFTER UPDATE` trigger
  targeting the row via `rowid` (not a resolved primary key), with
  recursion protection routed through a permanent `dbobjects_guard`
  table checked in the trigger's `WHEN` clause (SQLite trigger bodies
  have no procedural control flow at all). Verified end-to-end, not
  just designed: the guard survives a concurrent multi-connection pool
  and recovers cleanly from a failed corrective `UPDATE`, both proven
  live under the race detector.

### Changed
- **Breaking:** `Render`'s signature changed from `Render(objects,
  mode)` to `Render(mode, objects...)` to match `Register`/`Drop`'s
  shape (fixed parameter first, variadic objects last).

## [v0.3.0] - 2026-08-17

### Changed
- **Breaking:** `dbobjects.Init(db)` plus package-level functions
  replaced with `dbobjects.NewClient(db)` returning a `*Client`;
  `Register`/`Drop`/`Render` are now methods on `*Client`. Removes the
  global mutable package state that previously forced every integration
  test to mutate shared state via `Init`, which is why none of them
  could use `t.Parallel()`. Does not change the project's multi-DB
  scope (still one `Client` per connection, same as `Init` was meant to
  be called once) — only how that one connection is held.

## [v0.2.0] - 2026-08-14

### Added
- Full trigger event coverage: `BeforeInsert`/`BeforeUpdate`/
  `BeforeDelete`/`AfterInsert`/`AfterUpdate`/`AfterDelete` — all six
  `BEFORE`/`AFTER` × `INSERT`/`UPDATE`/`DELETE` combinations, up from
  just `BeforeInsert`/`BeforeUpdate`.
- `Body(sql)` — a raw-SQL escape hatch on any trigger, required for
  `DELETE` triggers (no `NEW` row to assign into) and available
  anywhere `Set()` isn't expressive enough.
- `SetColumns()` now reachable through the public `Trigger` interface —
  previously implemented but unexposed.
- `Set()` on `AfterInsert`/`AfterUpdate`, rendered as a follow-up
  `UPDATE` rather than a `NEW` assignment (not valid on an `AFTER`
  trigger), with a per-engine recursion guard: Postgres's
  `pg_trigger_depth()`, MySQL's session variable + `EXIT HANDLER`.
- `Render`'s `Declarative` mode — bare `CREATE` statements, no `OR
  REPLACE`/`IF EXISTS`, for structural DDL consumers like an Atlas
  external schema source. Previously stubbed.

## [v0.1.0] - 2026-08-09

### Added
- Triggers on Postgres and MySQL — `trigger.BeforeUpdate`/
  `BeforeInsert`, column assignments validated against your gorm schema
  at build time, custom naming via `.Name()`.
- Views on Postgres and MySQL — built from a gorm query callback
  (`view.Query(fn)`) so the same definition renders correctly per
  engine, with a `Raw(sql)` escape hatch for queries gorm can't
  express.
- `Register`/`Drop`/`Render` — apply, tear down, or preview the
  generated DDL without touching the database.
- Naming-collision detection, transactional DDL rollback on Postgres
  and best-effort compensating rollback on MySQL (where DDL implicitly
  commits), `context.Context` threading throughout.
