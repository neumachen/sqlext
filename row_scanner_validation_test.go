package dbsql_test

import (
	"testing"

	"github.com/neumachen/dbsql"
	"github.com/stretchr/testify/require"
)

type nilScanner struct{ dbsql.ColumnScanner }

func TestTypedRowInvalidMapping(t *testing.T) {
	bind := func(name dbsql.Column) dbsql.ColumnScanner {
		return dbsql.BindColumn(name, func(int64) error { t.Fatal("constructor invoked callback"); return nil })
	}
	var typedNil *nilScanner
	for _, tc := range []struct {
		name     string
		columns  []string
		bindings dbsql.ColumnBinders
	}{
		{"nil binding", []string{"id"}, dbsql.ColumnBinders{nil}},
		{"typed nil binding", []string{"id"}, dbsql.ColumnBinders{typedNil}},
		{"nil setter", []string{"id"}, dbsql.ColumnBinders{dbsql.BindColumn[int64]("id", nil)}},
		{"empty name", []string{""}, dbsql.ColumnBinders{bind("")}},
		{"duplicate bindings", []string{"id", "other"}, dbsql.ColumnBinders{bind("id"), bind("id")}},
		{"duplicate results", []string{"id", "id"}, dbsql.ColumnBinders{bind("id"), bind("other")}},
		{"unbound result", []string{"other"}, dbsql.ColumnBinders{bind("id")}},
		{"missing binding", []string{"id", "other"}, dbsql.ColumnBinders{bind("id")}},
		{"extra binding", []string{"id"}, dbsql.ColumnBinders{bind("id"), bind("other")}},
		{"legacy binder", []string{"id"}, dbsql.DefineColumnBinders(dbsql.DefineColumnBinding("id", dbsql.BindColumnToField(func(int64) error { return nil })))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &scanFixture{columns: tc.columns}
			rows := openScanRows(t, fixture)
			scanner, err := dbsql.NewRowScanner(rows, tc.bindings)
			require.ErrorIs(t, err, dbsql.ErrColumnMapping)
			require.Nil(t, scanner)
			require.Zero(t, fixture.nextCalls)
			require.Zero(t, fixture.closeCalls)
		})
	}
}

func TestTypedRowNilAndClosedRows(t *testing.T) {
	scanner, err := dbsql.NewRowScanner(nil, nil)
	require.ErrorIs(t, err, dbsql.ErrColumnMapping)
	require.Nil(t, scanner)
	require.ErrorIs(t, scanner.Scan(), dbsql.ErrColumnMapping)
	var zero dbsql.RowScanner
	require.ErrorIs(t, zero.Scan(), dbsql.ErrColumnMapping)

	rows := openScanRows(t, &scanFixture{columns: []string{"id"}})
	require.NoError(t, rows.Close())
	_, err = dbsql.NewRowScanner(rows, nil)
	require.Error(t, err)
}
