package sqlext_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/neumachen/sqlext"
	"github.com/neumachen/sqlext/pkg/sqlrepo"
	"github.com/stretchr/testify/require"
)

func TestTypedColumnNullModes(t *testing.T) {
	t.Run("ordinary setter preserves destination", func(t *testing.T) {
		got, calls := int64(42), 0
		column := sqlext.BindColumn("id", func(v int64) error { got = v; calls++; return nil })
		err := column.Scan(nil)
		require.NoError(t, err)
		require.Equal(t, int64(42), got)
		require.Zero(t, calls)
	})
	t.Run("nullable setter observes null", func(t *testing.T) {
		got := sqlext.ColumnValue[string]{Value: "previous", Valid: true}
		calls := 0
		column := sqlext.BindNullableColumn("name", func(v sqlext.ColumnValue[string]) error {
			got = v
			calls++
			return nil
		})
		err := column.Scan(nil)
		require.NoError(t, err)
		require.Equal(t, sqlext.ColumnValue[string]{}, got)
		require.Equal(t, 1, calls)
	})
}

func TestTypedColumnZeroInvokesSetter(t *testing.T) {
	got, calls := int64(42), 0
	column := sqlext.BindColumn("id", func(v int64) error { got = v; calls++; return nil })
	err := column.Scan(int64(0))
	require.NoError(t, err)
	require.Zero(t, got)
	require.Equal(t, 1, calls)
}

func TestTypedColumnRejectsMismatchBeforeSetter(t *testing.T) {
	calls := 0
	column := sqlext.BindColumn("id", func(int64) error { calls++; return nil })
	err := column.Scan(int(42))
	require.ErrorIs(t, err, sqlext.ErrColumnType)
	require.Zero(t, calls)
}

func TestTypedColumnPreservesSetterError(t *testing.T) {
	want := errors.New("setter failed")
	column := sqlext.BindColumn("id", func(int64) error { return want })
	err := column.Scan(int64(42))
	require.ErrorIs(t, err, want)
}

func TestTypedColumnJSONTypeError(t *testing.T) {
	var contact struct {
		Email string `json:"email"`
	}
	column := sqlext.BindColumn("contact", func(value []byte) error {
		return json.Unmarshal(value, &contact)
	})
	err := column.Scan([]byte(`{"email":42}`))
	var mismatch *json.UnmarshalTypeError
	require.ErrorAs(t, err, &mismatch)
	require.Equal(t, "email", mismatch.Field)
}

func TestTypedColumnInvalidBinding(t *testing.T) {
	for name, column := range map[string]sqlext.ColumnScanner{
		"nil ordinary": sqlext.BindColumn[int64]("id", nil),
		"nil nullable": sqlext.BindNullableColumn[int64]("id", nil),
		"empty name":   sqlext.BindColumn("", func(int64) error { return nil }),
	} {
		t.Run(name, func(t *testing.T) {
			err := column.Scan(nil)
			require.ErrorIs(t, err, sqlext.ErrColumnMapping)
		})
	}
}

func TestTypedColumnLegacyComposition(t *testing.T) {
	var id int64
	var name string
	columns := sqlext.DefineColumnBinders(
		sqlext.BindColumn("id", func(v int64) error { id = v; return nil }),
		sqlext.DefineColumnBinding("name", sqlext.BindColumnToField(func(v string) error { name = v; return nil })),
	)
	err := sqlrepo.BindMappedRow(columns, sqlext.MappedRow{"id": int64(7), "name": "Ada"})
	require.NoError(t, err)
	require.Equal(t, int64(7), id)
	require.Equal(t, "Ada", name)
}

func TestTypedColumnMissingDiffersFromNull(t *testing.T) {
	calls := 0
	column := sqlext.BindNullableColumn("id", func(sqlext.ColumnValue[int64]) error { calls++; return nil })
	err := column.BindColumn(sqlext.MappedRow{})
	require.ErrorIs(t, err, sqlext.ErrColumnMapping)
	require.Zero(t, calls)
	require.True(t, column.BindingRules().RequiredColumn())
}
