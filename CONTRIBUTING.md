# Contributing to gorm-dbobjects

Thanks for considering a contribution. This document covers how to get
set up locally, how the test suite is organized, and how to add
support for a new database engine — the most common kind of
contribution this project expects.

## Getting started

```sh
git clone https://github.com/bg12345/gorm-dbobjects.git
cd gorm-dbobjects
go build ./...
go vet ./...
go test ./...
```

Requires the Go version in [go.mod](go.mod). `go test ./...` with no
setup runs every unit test and dialect-level golden-SQL test — none of
those touch a real database.

## Running the integration tests

Integration tests connect to a real database and skip themselves
automatically if it isn't reachable, with one exception (SQLite, below).
Copy `.env.example` to `.env` and point it at whichever engines you want
to exercise locally — you don't need all of them running to contribute;
CI runs all four on every push regardless.

- **Postgres / MySQL** — any local or Dockerized instance matching
  `.env`'s values works. Quick one-liners matching what CI uses:
  ```sh
  docker run -d --name pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=dbobjects_test -p 5432:5432 postgres:16
  docker run -d --name mysql -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=dbobjects_test -p 3306:3306 mysql:8
  ```
- **SQLite** — nothing to start. `internal/testutil.NewSQLite(dsn)`
  takes its DSN as a parameter (a fresh `t.TempDir()` file per test) and
  these tests never self-skip except on a genuine CGO/driver failure.
- **SQL Server**:
  ```sh
  docker run -d --name mssql -e ACCEPT_EULA=Y \
    -e "MSSQL_SA_PASSWORD=yourStrong(!)Password1" -p 1433:1433 \
    mcr.microsoft.com/mssql/server:latest
  ```
  The image provisions no named database on startup (unlike the
  Postgres/MySQL images) — create one before running tests:
  ```sh
  docker exec mssql /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa \
    -P 'yourStrong(!)Password1' -Q "CREATE DATABASE dbobjects_test;"
  ```

Then:

```sh
go test -race ./...
```

CI (`.github/workflows/tests.yml`) runs the same command with all four
engines available — match it locally with `-race` before opening a PR,
not just a bare `go test`.

## How the test suite is organized

Two distinct kinds of test, kept deliberately separate:

- **`dialect_test.go`** (package `dbobjects`, white-box) — golden-SQL
  tests asserting the *exact* DDL a dialect renders, with no database
  involved. This is where you prove a dialect's rendering logic is
  correct in isolation. Lives in the internal `dbobjects` package
  (not `tests/`) because the dialect interfaces and render methods are
  all unexported.
- **`tests/`** (package `tests`, black-box) — integration tests against
  a real, running database, using only the public API. This is where
  you prove the rendered DDL actually does what it claims against a
  live engine — this project's standing bar is "verified against a real
  database," not just "compiles and looks right."

A change to rendering logic should usually come with both: a golden
test proving the exact SQL, and (if it's new behavior, not just a
refactor) an integration test proving it works for real.

## Adding a new database engine

The dialect layer is intentionally segregated so a new engine is additive,
not a rewrite. In `dialect.go`:

1. Add a `<engine>Dialect struct{}` implementing `Name() string` and
   `transactional() bool` (does DDL there roll back inside a
   transaction, or implicitly commit?).
2. Implement `triggerDialect` and/or `viewDialect` for it — only the
   kinds the engine actually supports; there's no requirement to
   implement both. `var _ triggerDialect = <engine>Dialect{}` at the
   bottom of the file turns a missing method into a compile error
   instead of a runtime surprise.
3. Register it in the `dialects` map, keyed by whatever
   `gorm.DB.Name()` returns for that engine's driver.
4. Add `internal/testutil.New<Engine>()` (or, if the engine needs no
   external service the way SQLite doesn't, a DSN-parameter variant —
   see `NewSQLite`).

**Before writing any rendering code**, work out how the engine actually
handles triggers — don't assume it mirrors Postgres/MySQL. Every engine
added so far has had a real, structural difference from the others
(MySQL requires `SET NEW.col = expr`, not bare `NEW.col = expr`; SQLite
can't assign to `NEW`/`OLD` on *any* timing; SQL Server has no `BEFORE`
DML trigger at all and is set-based over `inserted`/`deleted`, not
row-based). Check the engine's own documentation directly for anything
you're not certain of — recursion behavior and default settings in
particular have burned this project's own assumptions more than once.
If your engine has an open design question you can't resolve from
documentation alone (e.g. "does X actually work across a connection
pool"), write a live-database test that proves it, not just a
golden-SQL test that assumes it.

Existing dialects in `dialect.go` are the best reference for the shape
this should take — in particular, look at how each one's
`triggerBody`/`createTriggerStmt` are extracted and shared between the
idempotent and `Declarative` render paths, so the two can never
silently diverge.

## Opening a PR

- Branch off `main`, one feature or fix per branch.
- Make sure `go build ./...`, `go vet ./...`, and `go test -race ./...`
  are clean.
- `gofmt` new/changed code.
- Describe *why*, not just *what*, in the PR description if the change
  involves a real design decision — this project's history is full of
  "the obvious approach doesn't actually work because X," and that
  reasoning is worth keeping even if it doesn't make it into a doc.

## License

By contributing, you agree your contribution is licensed under this
project's [MIT license](LICENSE).
