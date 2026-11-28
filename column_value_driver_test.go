package sqlext_test

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/neumachen/sqlext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cases go through sqlext.ExecContext and database/sql to the in-memory
// driver, so they check what a driver actually receives, not only what
// DriverValue returns. assert.Equal tells a nil interface (NULL) apart from a
// typed nil []byte, which is valid data.
func TestColumnValueDriverValueArguments(t *testing.T) {
	zeroTime := time.Time{}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		arg  driver.Value
		want driver.Value
	}{
		{"zero value is NULL", sqlext.ColumnValue[int64]{}.DriverValue(), nil},
		{"invalid ignores value", sqlext.ColumnValue[string]{Value: "stale"}.DriverValue(), nil},
		{"invalid bytes are NULL", sqlext.ColumnValue[[]byte]{Value: []byte("stale")}.DriverValue(), nil},
		{"int64", sqlext.ColumnValue[int64]{Value: 42, Valid: true}.DriverValue(), int64(42)},
		{"int64 zero", sqlext.ColumnValue[int64]{Valid: true}.DriverValue(), int64(0)},
		{"float64 zero", sqlext.ColumnValue[float64]{Valid: true}.DriverValue(), float64(0)},
		{"bool false", sqlext.ColumnValue[bool]{Valid: true}.DriverValue(), false},
		{"string empty", sqlext.ColumnValue[string]{Valid: true}.DriverValue(), ""},
		{"bytes", sqlext.ColumnValue[[]byte]{Value: []byte("data"), Valid: true}.DriverValue(), []byte("data")},
		{"bytes empty", sqlext.ColumnValue[[]byte]{Value: []byte{}, Valid: true}.DriverValue(), []byte{}},
		{"bytes typed nil", sqlext.ColumnValue[[]byte]{Valid: true}.DriverValue(), []byte(nil)},
		{"time", sqlext.ColumnValue[time.Time]{Value: at, Valid: true}.DriverValue(), at},
		{"time zero", sqlext.ColumnValue[time.Time]{Valid: true}.DriverValue(), zeroTime},
	}
	stmt := prepareStatement(t, "UPDATE t SET v = @v")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, d := openExecDB(t)
			_, err := sqlext.ExecContext(context.Background(), db, stmt, sqlext.BindParameterValue("v", tt.arg))
			require.NoError(t, err)
			args := d.argsAt(0)
			require.Len(t, args, 1)
			assert.Equal(t, tt.want, args[0])
			assert.IsType(t, tt.want, args[0])
		})
	}
}

func TestColumnValueDriverValueSharesBytes(t *testing.T) {
	value := sqlext.ColumnValue[[]byte]{Value: []byte("data"), Valid: true}
	got, ok := value.DriverValue().([]byte)
	require.True(t, ok)
	got[0] = '!'
	assert.Equal(t, []byte("!ata"), value.Value, "DriverValue does not copy bytes")
}
