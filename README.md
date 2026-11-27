# dbsql

The `dbsql` package provides a set of utilities and abstractions for working with SQL databases in Go. It aims to simplify the process of managing prepared statements, mapping query results to struct fields, and handling named parameters.


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
    Name    dbsql.ColumnValue[string]
    Contact Contact
}

func (c *Customer) ColumnBinders() dbsql.ColumnBinders {
    return dbsql.DefineColumnBinders(
        dbsql.BindColumn("id", func(v int64) error {
            c.ID = v
            return nil
        }),
        dbsql.BindNullableColumn("name", func(v dbsql.ColumnValue[string]) error {
            c.Name = v
            return nil
        }),
        dbsql.BindColumn("contact", func(v []byte) error {
            return json.Unmarshal(v, &c.Contact)
        }),
    )
}
```

Here `dbsql` is `github.com/neumachen/dbsql`, `json` is `encoding/json`, and
`Contact` is your application-defined JSON result type. For query results whose
column names are `id`, `name`, and `contact`, build one scanner and reuse it:

```go
var customer Customer
scanner, err := dbsql.NewRowScanner(rows, customer.ColumnBinders())
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
- `errors.Is(err, dbsql.ErrColumnType)` identifies runtime type mismatches;
  `dbsql.ErrColumnMapping` identifies invalid mapping configuration. Callback
  errors remain available through `errors.Is` and `errors.As`.

The existing `BindColumnToField` and mapped-row APIs retain their behavior. The
new helpers also satisfy `ColumnBinder`, so they can be used in existing
`ColumnBinders` collections. Direct `NewRowScanner` use requires every binder to
also satisfy `ColumnScanner`; legacy-only binders continue to use mapped rows.

Run the isolated typed tests and example without a database:

```sh
go test . -run '^(TestColumnValue.*|TestTyped.*|ExampleNewRowScanner)$' -count=1
```

The existing full test suite requires its PostgreSQL fixtures. The typed tests
use an in-memory driver to exercise `database/sql`; they do not verify PostgreSQL
server behavior. The generic constraints enforce allowed Go types at compile
time; SQL result types and JSON contents are checked at runtime.

## Installation

To use the `dbsql` package, you can install it using the following command:

```
go get github.com/your-username/dbsql
```

## Usage

### Prepared Statements

Here's an example of how to use the `PreparedStatement` interface:

```go
import "github.com/your-username/dbsql"

// Prepare a statement with named parameters
stmt, err := dbsql.PrepareStatement("SELECT * FROM users WHERE name = @name AND age > @age")
if err != nil {
    // Handle error
}

// Bind the named parameters
if err := stmt.BindNamedParameterValue("name", "John"); err != nil {
    // Handle error
}
if err := stmt.BindNamedParameterValue("age", 30); err != nil {
    // Handle error
}

// Execute the prepared statement
rows, err := stmt.Query(db)
if err != nil {
    // Handle error
}
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

### Column Mapping

To map SQL query results to struct fields, you can use the `ColumnMapperFunc` and `ColumnMapper` types:

```go
type User struct {
    ID        int64
    Name      string
    Age       int
}

// Define the column mapping functions
columnMappers := dbsql.ColumnMapperMap{
    "id":   dbsql.MapColumn[int64](func(v int64) error { u.ID = v; return nil }),
    "name": dbsql.MapColumn[string](func(v string) error { u.Name = v; return nil }),
    "age":  dbsql.MapColumn[int](func(v int) error { u.Age = v; return nil }),
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

mappedRows, err := dbsql.MapRows(rows)
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

The `dbsql` package includes a comprehensive test suite to ensure the reliability of its features. You can run the tests using the following command:

```
go test -v ./...
```

The tests cover a range of scenarios, including error handling, data mapping, and prepared statement execution.

## Contributing

If you'd like to contribute to the `dbsql` package, please feel free to submit a pull request or open an issue. Contributions are welcome and appreciated!

## License

The `dbsql` package is licensed under the [MIT License](LICENSE).
