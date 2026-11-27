package sqlext_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/neumachen/sqlext"
)

// These cases pin the legacy BindColumnToField semantics, which the removal
// of column binding rules must not change: a missing column is an error, while
// NULL and the zero values that internal.IsNilOrZeroValue reports skip the
// callback without error.
func TestBindColumnToFieldLegacySemantics(t *testing.T) {
	tests := []struct {
		name      string
		bind      func(calls *int) sqlext.ColumnBinderFunc
		row       sqlext.MappedRow
		wantCalls int
		wantErr   string
	}{
		{
			name:    "missing column",
			bind:    countCalls[int64],
			row:     sqlext.MappedRow{otherColumn: int64(1)},
			wantErr: "required column id not found",
		},
		{
			name:    "missing column in empty row",
			bind:    countCalls[int64],
			row:     sqlext.MappedRow{},
			wantErr: "required column id not found",
		},
		{name: "NULL skips callback", bind: countCalls[int64], row: sqlext.MappedRow{idColumn: nil}},
		{name: "int zero skips callback", bind: countCalls[int], row: sqlext.MappedRow{idColumn: 0}},
		{name: "nil bytes skip callback", bind: countCalls[[]byte], row: sqlext.MappedRow{idColumn: []byte(nil)}},
		{name: "empty bytes are delivered", bind: countCalls[[]byte], row: sqlext.MappedRow{idColumn: []byte{}}, wantCalls: 1},
		{name: "int64 zero is delivered", bind: countCalls[int64], row: sqlext.MappedRow{idColumn: int64(0)}, wantCalls: 1},
		{name: "empty string is delivered", bind: countCalls[string], row: sqlext.MappedRow{idColumn: ""}, wantCalls: 1},
		{name: "value is delivered", bind: countCalls[int64], row: sqlext.MappedRow{idColumn: int64(7)}, wantCalls: 1},
		{
			name:    "type mismatch",
			bind:    countCalls[int64],
			row:     sqlext.MappedRow{idColumn: "7"},
			wantErr: "does not match asserted type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			binder := sqlext.DefineColumnBinding(idColumn, tt.bind(&calls))

			err := binder.BindColumn(tt.row)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantCalls, calls)
		})
	}
}

func countCalls[T any](calls *int) sqlext.ColumnBinderFunc {
	return sqlext.BindColumnToField(func(T) error {
		*calls++
		return nil
	})
}
