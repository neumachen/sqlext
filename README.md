# sqlext

The `sqlext` (SQL extension) package provides a set of utilities and abstractions for working with SQL databases in Go. It aims to simplify the process of managing prepared statements, mapping query results to struct fields, and handling named parameters.


## Features

- **Prepared Statements**: The `PreparedStatement` interface and `preparedStatement` struct simplify the creation, execution, and management of prepared SQL statements with named parameters.
- **Column Mapping**: The `ColumnMapperFunc` and `ColumnMapper` types allow you to define custom mapping functions to map SQL query results to struct fields.
- **Row Mapping**: The `MapRow` and `MapRows` functions provide a convenient way to map SQL query results to `MappedRow` and `MappedRows` data structures.

## Typed column callbacks

The typed API keeps column mappings on caller-owned structs. It accepts exactly
`int64`, `float64`, `bool`, `string`, `[]byte`, and `time.Time` as source types.
It does not coerce driver values: an `int`, a distinct named integer type, or a
string will not be accepted as an `int64`. Convert to application-specific types
inside the callback. Decode JSON explicitly into your own struct there too.

```go
type Customer struct {
    ID      int64
    Name    sqlext.ColumnValue[string]
    Contact Contact
}

func (c *Customer) ColumnBinders() sqlext.ColumnBinders {
    return sqlext.DefineColumnBinders(
        sqlext.BindColumn("id", func(v int64) error {
            c.ID = v
            return nil
        }),
        sqlext.BindNullableColumn("name", func(v sqlext.ColumnValue[string]) error {
            c.Name = v
            return nil
        }),
        sqlext.BindColumn("contact", func(v []byte) error {
            return json.Unmarshal(v, &c.Contact)
        }),
    )
}
```

Here `sqlext` is `github.com/neumachen/sqlext`, `json` is `encoding/json`, and
`Contact` is your application-defined JSON result type. For query results whose
column names are `id`, `name`, and `contact`, build one scanner and reuse it:

```go
var customer Customer
scanner, err := sqlext.NewRowScanner(rows, customer.ColumnBinders())
if err != nil {
    return nil, err
}
var customers []Customer
for rows.Next() {
    customer = Customer{}
    if err := scanner.Scan(); err != nil {
        return nil, err
    }
    customers = append(customers, customer)
}
return customers, rows.Err()
```

The caller opens and closes `rows`, supplies the query context, and checks
`rows.Err()`. The scanner does not advance or close results. Query projections,
joins, and database-function results all use the same mapping path. A complete,
database-free executable example is in `typed_example_test.go`.

- `BindColumn` skips SQL NULL, leaving the destination unchanged; reset a reused
  record before each row as shown above. It delivers valid zero values.
- `BindNullableColumn` also delivers NULL as `ColumnValue[T]{Valid: false}`.
  This preserves the difference between NULL and an ordinary zero value.
- Only a nil driver source denotes SQL NULL. A typed nil or empty `[]byte` is
  valid byte data. Retained bytes are copied before the callback receives them.
- All result columns and bindings must match one-to-one by exact name. Order may
  differ. Duplicate names, missing columns, extra bindings, and nil setters fail.
- Callbacks run in result-column order. A failure can leave earlier fields
  assigned; there is no rollback. Scanning the same current row again repeats
  callbacks. Use a new scanner after `NextResultSet` and do not share a scanner
  or its destination record between concurrent goroutines.
- `errors.Is(err, sqlext.ErrColumnType)` identifies runtime type mismatches;
  `sqlext.ErrColumnMapping` identifies invalid mapping configuration. Callback
  errors remain available through `errors.Is` and `errors.As`.

The existing `BindColumnToField` and mapped-row APIs retain their behavior. The
new helpers also satisfy `ColumnBinder`, so they can be used in existing
`ColumnBinders` collections. Direct `NewRowScanner` use requires every binder to
also satisfy `ColumnScanner`; legacy-only binders continue to use mapped rows.

Breaking change: column binding rules are removed. `ColumnBindingRules`,
`DefineColumnBindingRulesFunc` and `ColumnRequired` no longer exist,
`ColumnBinder` no longer has a `BindingRules` method, and `DefineColumnBinding`
takes only a column and a binder function.

- Bindings created with `DefineColumnBinding` (whose default rules required
  every column) and the typed `BindColumn` and `BindNullableColumn` bindings
  already required their column, so they behave as before.
  `BindColumnToField` still returns an error when the column is missing from
  the mapped row and still skips NULL and the zero values it skipped before.
- Optional columns are no longer supported. A custom `ColumnBinder` whose
  `BindingRules` reported a column as optional (`RequiredColumn` returning
  false) used to have a missing column skipped by `BindColumnToField`; that
  rule is no longer consulted, and a missing column is now an error. Remove
  bindings for columns a query does not select before binding, for example
  with `FilterUsingFunc` (see below).
- To migrate, drop any `ColumnRequired()` argument and any `BindingRules()`
  call. A custom `ColumnBinder` may keep its `BindingRules` method, but nothing
  calls it any more.

`FilterUsingFunc` now returns an empty collection instead of the original when
nothing matches, and an error for a nil callback.

### Selecting and replacing bindings

A record usually describes all of its columns once, while a query may select
only some of them. Because `NewRowScanner` requires an exact match, narrow the
bindings to the projection first, and swap a single binding when one query
needs a different conversion:

```go
selected := sqlext.Columns{"id", "name"}
bindings, err := customer.ColumnBinders().FilterUsingFunc(
    func(i int, b sqlext.ColumnBinders) (bool, error) {
        return selected.HasColumn(b[i].Column()), nil
    },
)
if err != nil {
    return nil, err
}
bindings, err = bindings.Replace(sqlext.BindColumn("name", func(v string) error {
    customer.Name = sqlext.ColumnValue[string]{Value: strings.TrimSpace(v), Valid: true}
    return nil
}))
if err != nil {
    return nil, err
}
scanner, err := sqlext.NewRowScanner(rows, bindings)
```

- `FilterUsingFunc` returns a new collection with the kept binders in their
  original order; when nothing matches it is empty, never the original. The
  callback gets a snapshot of the collection taken before filtering starts,
  which filtering itself never changes; as long as the callback treats it as
  read-only, every call sees the original collection. Writes to the snapshot
  are visible to later calls but change neither the receiver nor which
  binders are returned. A nil callback is an error, and a callback error stops
  filtering and is returned unchanged with a nil collection.
- `Replace` returns a new collection in which the one binder whose column name
  equals the replacement's is swapped, keeping the order. A nil (or typed nil)
  replacement, an empty name, no match, more than one match or a nil binder in
  the collection is an error wrapping `ErrColumnMapping`.
- Both work with typed and legacy binders, never modify the receiver and are
  shallow: the result holds the same binders, so their setters still write to
  the same record.

Run the isolated typed tests and example without a database:

```sh
go test . -run '^(TestColumnValue.*|TestTyped.*|ExampleNewRowScanner)$' -count=1
```

These tests are part of the default, database-free `go test ./...` run. The
typed tests use an in-memory driver to exercise `database/sql`; they do not
verify PostgreSQL server behavior. The generic constraints enforce allowed Go types at compile
time; SQL result types and JSON contents are checked at runtime.

## Installation

To use the `sqlext` package, you can install it using the following command:

```
go get github.com/neumachen/sqlext
```

The module was previously named `github.com/neumachen/dbsql`. To migrate,
replace that import path with `github.com/neumachen/sqlext` and the `dbsql.`
qualifier with `sqlext.`. The API is otherwise unchanged; sentinel errors keep
their identity for `errors.Is`, but their messages now start with `sqlext:`.
The development database in `.dev/docker-compose.yml` and the default
connection of the database-backed tests now use the `sqlext_dev` database and
the `sqlext` user. An existing database created with the old names is not
migrated.

The optional helper package `github.com/neumachen/sqlext/pkg/sqlrepo`
(`QueryContext`, `BindMappedRow`, `BindMappedRows` and `RowBinder`) has been
removed; this breaks code that imports it. Use the root package instead:
`NewRowScanner` for typed rows, or `MapRows` and each binder's `BindColumn`
for mapped rows.

## Usage

### Prepared Statements

Here's an example of how to use the `PreparedStatement` interface:

```go
import "github.com/neumachen/sqlext"

// Prepare a statement with named parameters
stmt, err := sqlext.PrepareStatement("SELECT * FROM users WHERE name = @name AND age > @age")
if err != nil {
    // Handle error
}

// Bind the named parameters and execute the statement
rows, err := sqlext.QueryContext(ctx, db, stmt,
    sqlext.BindParameterValue("name", "John"),
    sqlext.BindParameterValue("age", 30),
)
if err != nil {
    // Handle error
}
defer rows.Close()
```

Named parameters are rewritten to PostgreSQL placeholders (`$1`, `$2`, ...) in
order of appearance. Text inside string constants, quoted identifiers, comments,
and dollar-quoted strings is never rewritten. `PrepareStatement` returns an error
for unterminated literals or comments, and for statements that mix named
parameters with native placeholders such as `$1`.

PostgreSQL reads adjacent operator characters as one operator, so `x<@name`
could mean the `<@` operator applied to the column `name` or `<` applied to the
parameter `@name`. When the characters before a parameter combine with its `@`
into the built-in operators `<@`, `@@`, `@@@`, or `@-@`, `PrepareStatement`
returns an error. Add whitespace to choose: `x <@ name` uses the operator,
`x < @name` uses the parameter, and `x <@ @name` uses both. Other operators
directly before a parameter, as in `x=@name` or `x||@name`, bind the parameter.

### Executing statements

`Exec`, `Query`, `QueryRow` and their `Context` variants bind the values and
execute the rewritten SQL through the executor's `ExecContext`, `QueryContext`
or `QueryRowContext` method. The executor can be a `*sql.DB` or a `*sql.Tx`.

- No explicit `Prepare` is issued, so no server-side prepared statement is left
  behind per call. `database/sql` and the driver manage any statement they use
  and release it with the result or the rows.
- The caller owns transactions and returned rows. Read rows until `Next`
  returns false or call `Close`; inside a transaction, do this before `Commit`
  or `Rollback`.
- Each call accepts exactly one SQL statement. Trailing semicolons, whitespace
  and comments are allowed, and so are semicolons inside string constants,
  quoted identifiers, comments and dollar-quoted strings. Anything after a
  statement-ending semicolon is rejected with `ErrMultipleStatements` before the
  database is contacted, with or without parameters.
- The check does not depend on the server's `standard_conforming_strings`
  setting. Whether a backslash escapes a quote in a plain `'...'` string depends
  on that setting, so the SQL is read both ways and rejected when either reading
  finds more than one statement. Text whose statement count depends on the
  setting is therefore rejected; use `E'...'` strings, doubled quotes or
  dollar quotes, whose meaning does not depend on it. Literals that are valid
  under only one setting, such as `'C:\temp\'`, are still sent; the server
  rejects them under the other setting.
- SQL-standard function bodies (`CREATE FUNCTION ... BEGIN ATOMIC ...; END`)
  are a single statement, but they are rejected because their inner semicolons
  cannot be told apart from statement separators without a SQL parser. This is
  a known compatibility limitation; use a dollar-quoted body or run such
  statements through `database/sql` directly.
- `QueryRow` and `QueryRowContext` follow `database/sql`: database errors are
  reported by the row's `Scan`. Their error result only reports problems found
  before the database is contacted, such as nil arguments, a rejected statement
  or a binder error.
- Bound values are reset after every call. Unbound parameter names are sent as
  NULL and names that do not occur in the statement are ignored.

Compatibility notes: earlier versions prepared a statement for every call and
never closed it. Executors no longer receive `Prepare` calls, preparation errors
from `QueryRow` now surface at `Scan`, and SQL containing several statements is
now rejected. With lib/pq, statements without parameters use the simple query
protocol.

### Column Mapping

To map SQL query results to struct fields, you can use the `ColumnMapperFunc` and `ColumnMapper` types:

```go
type User struct {
    ID        int64
    Name      string
    Age       int
}

// Define the column mapping functions
columnMappers := sqlext.ColumnMapperMap{
    "id":   sqlext.MapColumn[int64](func(v int64) error { u.ID = v; return nil }),
    "name": sqlext.MapColumn[string](func(v string) error { u.Name = v; return nil }),
    "age":  sqlext.MapColumn[int](func(v int) error { u.Age = v; return nil }),
}

// Use the column mappers to map the query result to a User struct
row, err := stmt.QueryRow(db)
user := User{}
if err := user.mapRow(row, columnMappers); err != nil {
    // Handle error
}
```

### Row Mapping

You can use the `MapRow` and `MapRows` functions to map SQL query results to `MappedRow` and `MappedRows` data structures:

```go
rows, err := stmt.Query(db)
if err != nil {
    // Handle error
}

mappedRows, err := sqlext.MapRows(rows)
if err != nil {
    // Handle error
}

for _, row := range mappedRows {
    // Access the column-value pairs in the mapped row
    name := row["name"].(string)
    age := row["age"].(int)
    // ...
}
```

## Testing

`go test ./...` runs the database-free tests: parsing and rewriting, the
single-statement check, execution through `database/sql` with an in-memory
driver, typed scanning and the compile-time contract. It never opens a
database or contacts another service, whatever runs on localhost. Add `-race`
to check for data races.

### Integration tests

Tests that talk to PostgreSQL are compiled only with the `integration` build
tag and run only against a disposable instance created for the run:

```sh
state=$(.dev/test-postgres.sh up)   # starts a labelled container on a loopback port
set -a; . "$state"; set +a          # exports SQLEXT_TEST_POSTGRES_DSN and _CLUSTER
go test -tags integration -count=1 ./...
.dev/test-postgres.sh down "$state" # removes that container
```

`.dev/test-postgres.sh` needs Docker. It starts a digest-pinned
`postgres:16-alpine` container with a random password, data in tmpfs and
`cluster_name` `sqlext-test-<random id>`, and checks the instance before it
prints the state file.

Safety expectations:

- Both `SQLEXT_TEST_POSTGRES_DSN` and `SQLEXT_TEST_POSTGRES_CLUSTER` are
  required. A missing, partial or mismatched configuration fails the tests; it
  never falls back to a local default.
- The cluster name must start with `sqlext-test-`, the server must report that
  `cluster_name`, and the `public` schema may only hold the test fixtures and
  tables the tests create.
- These checks guard against pointing the tests at the wrong server, but they
  cannot prove that an instance is disposable. The tests create, change and
  delete data: only point them at an instance created for the run, never at a
  database you care about. The development database in
  `.dev/docker-compose.yml` is not a test target.
- Setting the variables without `-tags integration` fails the default run, so
  a run meant to be an integration run cannot pass without one.

The older database tests (`TestMapColumn`, `TestMapRow`, `TestMapRows` and the
`*Postgres` variants of the execution tests) also need the fixtures in
`.dev/migrations`, which require PostGIS. Run them with both tags against an
instance from a pinned PostGIS image:

```sh
state=$(SQLEXT_TEST_POSTGRES_IMAGE=<postgis image pinned by digest> .dev/test-postgres.sh up --fixtures)
set -a; . "$state"; set +a
go test -tags "integration pgfixtures" -count=1 ./...
.dev/test-postgres.sh down "$state"
```

CI runs the database-free tests on the minimum Go version from `go.mod` and on
the current release, and the `integration` tests against a disposable
PostgreSQL. The fixture-based tests are not run in CI.

### Formatting and linting

CI checks formatting and lint with golangci-lint v2.12.2 and `.golangci.yml`,
the version and configuration `.dev/lint.sh check` uses locally; CI reads the
version from `EXPECTED_VERSION` in `.dev/lint.sh`. The lint step runs
`golangci-lint run` through `golangci-lint-action` (with inline annotations on
pull requests). A separate step downloads the official prebuilt golangci-lint
release binary, verifies it against the release checksums and runs
`golangci-lint fmt --diff ./...` (the `gofmt` and `goimports` settings in
`.golangci.yml`); it fails on any formatting difference and also runs when lint
fails. Locally `.dev/lint.sh` needs golangci-lint v2.12.2 on `PATH`, warns
about other versions and stops with installation instructions when the tool is
missing. It never runs tests or opens a database.

```sh
.dev/lint.sh check            # whole repository, formatting plus lint, as CI
.dev/lint.sh check --changed  # only problems in Go changes since HEAD
.dev/lint.sh check --staged   # only problems in the staged change
.dev/lint.sh fmt              # format the Go files changed since HEAD
```

`check --changed` and `check --staged` report only issues in the changed
lines, so findings that already exist elsewhere do not block unrelated work;
`check` still reports them, as CI does. `check --staged` checks a copy of the
index, so it sees exactly what will be committed. `fmt` rewrites only Go files
changed since HEAD, only in the working tree, and never stages anything;
review and stage the result yourself (with `git add -p` if a file is only
partly staged).

Automatic checks:

- Git commits (opt-in, per clone): `.dev/install-git-hooks.sh` installs a
  `pre-commit` hook in this clone's `.git/hooks` that runs
  `.dev/lint.sh check --staged` and refuses the commit on problems. It does not
  replace an existing `pre-commit` hook or work when `core.hooksPath` is set; in
  those cases call `.dev/lint.sh check --staged` from your own hook. Remove it
  with `.dev/install-git-hooks.sh --uninstall`.
- Claude Code turns (opt-in, per clone): `.dev/claude-stop-check.sh` is a
  `Stop` hook that runs `.dev/lint.sh check --changed` once when Claude
  finishes a turn with Go changes since HEAD and reports problems back to
  Claude. It does not run after individual tool calls and does not change
  files. The repository does not enable it and does not track `.claude/`, so
  Claude Code settings are local to each clone and are not distributed. To
  enable the hook, add it to your own
  `.claude/settings.json` (or `.claude/settings.local.json`):

  ```json
  {
    "hooks": {
      "Stop": [
        {
          "hooks": [
            {
              "type": "command",
              "command": "\"$CLAUDE_PROJECT_DIR\"/.dev/claude-stop-check.sh",
              "timeout": 180
            }
          ]
        }
      ]
    }
  }
  ```

Neither hook replaces CI: commits made without the `pre-commit` hook and
changes made outside Claude Code are still checked there.

## Contributing

If you'd like to contribute to the `sqlext` package, please feel free to submit a pull request or open an issue. Contributions are welcome and appreciated!

## License

The `sqlext` package is licensed under the [MIT License](LICENSE).
