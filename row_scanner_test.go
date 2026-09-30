package dbsql_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/neumachen/dbsql"
	"github.com/stretchr/testify/require"
)

func TestTypedRowOrderedReusableMapping(t *testing.T) {
	fixture := &scanFixture{
		columns: []string{"raw", "name", "id"},
		values:  [][]driver.Value{{[]byte("first"), "Ada", int64(1)}, {[]byte("later"), nil, int64(0)}},
	}
	rows := openScanRows(t, fixture)
	var id int64
	var name dbsql.ColumnValue[string]
	var raw []byte
	bindings := dbsql.DefineColumnBinders(
		dbsql.BindColumn("id", func(v int64) error { id = v; return nil }),
		dbsql.BindNullableColumn("name", func(v dbsql.ColumnValue[string]) error { name = v; return nil }),
		dbsql.BindColumn("raw", func(v []byte) error { raw = v; return nil }),
	)
	scanner, err := dbsql.NewRowScanner(rows, bindings)
	require.NoError(t, err)
	bindings[0] = dbsql.BindColumn("id", func(int64) error { return errors.New("replacement must not run") })
	require.Zero(t, fixture.nextCalls)
	require.True(t, rows.Next())
	require.NoError(t, scanner.Scan())
	require.Equal(t, int64(1), id)
	require.Equal(t, dbsql.ColumnValue[string]{Value: "Ada", Valid: true}, name)
	first := raw
	require.Equal(t, 1, fixture.nextCalls)
	require.Zero(t, fixture.closeCalls)

	require.True(t, rows.Next())
	require.NoError(t, scanner.Scan())
	require.Zero(t, id)
	require.Equal(t, dbsql.ColumnValue[string]{}, name)
	require.Equal(t, []byte("first"), first)
	require.Equal(t, []byte("later"), raw)
	require.Equal(t, 2, fixture.nextCalls)
	require.Zero(t, fixture.closeCalls)
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
}

func TestTypedRowPartialAssignment(t *testing.T) {
	want := errors.New("second setter failed")
	fixture := &scanFixture{columns: []string{"first", "second", "third"}, values: [][]driver.Value{{int64(1), int64(2), int64(3)}}}
	rows := openScanRows(t, fixture)
	var assigned []int64
	bindings := dbsql.DefineColumnBinders(
		dbsql.BindColumn("third", func(v int64) error { assigned = append(assigned, v); return nil }),
		dbsql.BindColumn("second", func(int64) error { return want }),
		dbsql.BindColumn("first", func(v int64) error { assigned = append(assigned, v); return nil }),
	)
	scanner, err := dbsql.NewRowScanner(rows, bindings)
	require.NoError(t, err)
	require.True(t, rows.Next())
	err = scanner.Scan()
	require.ErrorIs(t, err, want)
	require.Equal(t, []int64{1}, assigned)
	require.Equal(t, 1, fixture.nextCalls)
	require.Zero(t, fixture.closeCalls)
}

func TestTypedRowLifecycle(t *testing.T) {
	fixture := &scanFixture{columns: []string{"id"}, values: [][]driver.Value{{int64(7)}}}
	rows := openScanRows(t, fixture)
	calls := 0
	scanner, err := dbsql.NewRowScanner(rows, dbsql.DefineColumnBinders(dbsql.BindColumn("id", func(int64) error { calls++; return nil })))
	require.NoError(t, err)
	require.Error(t, scanner.Scan())
	require.Zero(t, calls)
	require.True(t, rows.Next())
	require.NoError(t, scanner.Scan())
	require.NoError(t, scanner.Scan())
	require.Equal(t, 2, calls)
	require.Equal(t, 1, fixture.nextCalls)
	require.Zero(t, fixture.closeCalls)
	require.NoError(t, rows.Close())
	require.Error(t, scanner.Scan())
}

func TestTypedRowEmptyAndIterationError(t *testing.T) {
	want := errors.New("iteration failed")
	for name, iterationErr := range map[string]error{"empty": nil, "iteration error": want} {
		t.Run(name, func(t *testing.T) {
			rows := openScanRows(t, &scanFixture{columns: []string{"id"}, iterationErr: iterationErr})
			scanner, err := dbsql.NewRowScanner(rows, dbsql.DefineColumnBinders(dbsql.BindColumn("id", func(int64) error { return nil })))
			require.NoError(t, err)
			require.NotNil(t, scanner)
			require.False(t, rows.Next())
			require.ErrorIs(t, rows.Err(), iterationErr)
		})
	}
}

func TestTypedRowRuntimeTypeError(t *testing.T) {
	rows := openScanRows(t, &scanFixture{columns: []string{"id"}, values: [][]driver.Value{{"7"}}})
	calls := 0
	scanner, err := dbsql.NewRowScanner(rows, dbsql.DefineColumnBinders(dbsql.BindColumn("id", func(int64) error { calls++; return nil })))
	require.NoError(t, err)
	require.True(t, rows.Next())
	err = scanner.Scan()
	require.ErrorIs(t, err, dbsql.ErrColumnType)
	require.Zero(t, calls)
}
