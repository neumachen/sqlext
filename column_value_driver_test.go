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
		{"scanned NULL drops previous string", scannedNull(t, sqlext.NewColumnValue("stale")).DriverValue(), nil},
		{"scanned NULL drops previous bytes", scannedNull(t, sqlext.NewColumnValue([]byte("stale"))).DriverValue(), nil},
		{"int64", sqlext.NewColumnValue(int64(42)).DriverValue(), int64(42)},
		{"int64 zero", sqlext.NewColumnValue(int64(0)).DriverValue(), int64(0)},
		{"float64 zero", sqlext.NewColumnValue(float64(0)).DriverValue(), float64(0)},
		{"bool false", sqlext.NewColumnValue(false).DriverValue(), false},
		{"string empty", sqlext.NewColumnValue("").DriverValue(), ""},
		{"bytes", sqlext.NewColumnValue([]byte("data")).DriverValue(), []byte("data")},
		{"bytes empty", sqlext.NewColumnValue([]byte{}).DriverValue(), []byte{}},
		{"bytes typed nil", sqlext.NewColumnValue([]byte(nil)).DriverValue(), []byte(nil)},
		{"scanned bytes typed nil", scanned(t, []byte(nil)).DriverValue(), []byte(nil)},
		{"time", sqlext.NewColumnValue(at).DriverValue(), at},
		{"time zero", sqlext.NewColumnValue(zeroTime).DriverValue(), zeroTime},
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
	value := sqlext.NewColumnValue([]byte("data"))
	got, ok := value.DriverValue().([]byte)
	require.True(t, ok)
	got[0] = '!'
	assert.Equal(t, []byte("!ata"), value.Value(), "DriverValue does not copy bytes")
}

// scannedNull returns value after scanning SQL NULL into it. With private
// fields, a scan is the only way an earlier value can precede NULL.
func scannedNull[T sqlext.ColumnType](t *testing.T, value sqlext.ColumnValue[T]) sqlext.ColumnValue[T] {
	t.Helper()
	require.NoError(t, value.Scan(nil))
	return value
}

func scanned(t *testing.T, src []byte) sqlext.ColumnValue[[]byte] {
	t.Helper()
	var value sqlext.ColumnValue[[]byte]
	require.NoError(t, value.Scan(src))
	return value
}
