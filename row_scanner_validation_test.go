package sqlext_test

import (
	"testing"

	"github.com/neumachen/sqlext"
	"github.com/stretchr/testify/require"
)

type nilScanner struct{ sqlext.ColumnScanner }

func TestTypedRowInvalidMapping(t *testing.T) {
	bind := func(name sqlext.Column) sqlext.ColumnScanner {
		return sqlext.BindColumn(name, func(int64) error { t.Fatal("constructor invoked callback"); return nil })
	}
	var typedNil *nilScanner
	for _, tc := range []struct {
		name     string
		columns  []string
		bindings sqlext.ColumnBinders
	}{
		{"nil binding", []string{idColumn}, sqlext.ColumnBinders{nil}},
		{"typed nil binding", []string{idColumn}, sqlext.ColumnBinders{typedNil}},
		{"nil setter", []string{idColumn}, sqlext.ColumnBinders{sqlext.BindColumn[int64](idColumn, nil)}},
		{"empty name", []string{""}, sqlext.ColumnBinders{bind("")}},
		{"duplicate bindings", []string{idColumn, otherColumn}, sqlext.ColumnBinders{bind(idColumn), bind(idColumn)}},
		{"duplicate results", []string{idColumn, idColumn}, sqlext.ColumnBinders{bind(idColumn), bind(otherColumn)}},
		{"unbound result", []string{otherColumn}, sqlext.ColumnBinders{bind(idColumn)}},
		{"missing binding", []string{idColumn, otherColumn}, sqlext.ColumnBinders{bind(idColumn)}},
		{"extra binding", []string{idColumn}, sqlext.ColumnBinders{bind(idColumn), bind(otherColumn)}},
		{"legacy binder", []string{idColumn}, sqlext.DefineColumnBinders(sqlext.DefineColumnBinding(idColumn, sqlext.BindColumnToField(func(int64) error { return nil })))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &scanFixture{columns: tc.columns}
			rows := openScanRows(t, fixture)
			scanner, err := sqlext.NewRowScanner(rows, tc.bindings)
			require.ErrorIs(t, err, sqlext.ErrColumnMapping)
			require.Nil(t, scanner)
			require.Zero(t, fixture.nextCalls)
			require.Zero(t, fixture.closeCalls)
		})
	}
}

func TestTypedRowNilAndClosedRows(t *testing.T) {
	scanner, err := sqlext.NewRowScanner(nil, nil)
	require.ErrorIs(t, err, sqlext.ErrColumnMapping)
	require.Nil(t, scanner)
	require.ErrorIs(t, scanner.Scan(), sqlext.ErrColumnMapping)
	var zero sqlext.RowScanner
	require.ErrorIs(t, zero.Scan(), sqlext.ErrColumnMapping)

	rows := openScanRows(t, &scanFixture{columns: []string{idColumn}})
	require.NoError(t, rows.Close())
	_, err = sqlext.NewRowScanner(rows, nil)
	require.Error(t, err)
}
