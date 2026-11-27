package sqlext

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func assertColumnValue[T ColumnType](t *testing.T, src T) {
	t.Helper()
	value := ColumnValue[T]{}
	err := value.Scan(src)
	require.NoError(t, err)
	require.True(t, value.Valid)
	require.Equal(t, src, value.Value)
}

func TestColumnValueSupportedTypes(t *testing.T) {
	assertColumnValue(t, int64(42))
	assertColumnValue(t, 3.5)
	assertColumnValue(t, true)
	assertColumnValue(t, "Ada")
	assertColumnValue(t, []byte("data"))
	assertColumnValue(t, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC))
}

func TestColumnValueZeroValues(t *testing.T) {
	assertColumnValue(t, int64(0))
	assertColumnValue(t, float64(0))
	assertColumnValue(t, false)
	assertColumnValue(t, "")
	assertColumnValue(t, []byte{})
	assertColumnValue(t, time.Time{})
}

func TestColumnValueNullResetsValue(t *testing.T) {
	value := ColumnValue[int64]{Value: 42, Valid: true}
	err := value.Scan(nil)
	require.NoError(t, err)
	require.Equal(t, ColumnValue[int64]{}, value)
}

func TestColumnValueNilBytesAreValid(t *testing.T) {
	value := ColumnValue[[]byte]{Value: []byte("previous"), Valid: true}
	err := value.Scan([]byte(nil))
	require.NoError(t, err)
	require.True(t, value.Valid)
	require.Nil(t, value.Value)
}

func TestColumnValueOwnsBytes(t *testing.T) {
	src := []byte("data")
	var value ColumnValue[[]byte]
	err := value.Scan(src)
	src[0] = '!'
	require.NoError(t, err)
	require.Equal(t, []byte("data"), value.Value)
}

func TestColumnValueRejectsMismatchedTypes(t *testing.T) {
	type customerID int64
	for _, src := range []any{int(42), "42", float64(42), new(int64), customerID(42), []byte(nil)} {
		t.Run(fmt.Sprintf("%T", src), func(t *testing.T) {
			value := ColumnValue[int64]{Value: 7, Valid: true}
			err := value.Scan(src)
			require.ErrorIs(t, err, ErrColumnType)
			require.Equal(t, ColumnValue[int64]{}, value)
		})
	}
}

func TestColumnValueNilDestination(t *testing.T) {
	var value *ColumnValue[int64]
	err := value.Scan(int64(1))
	require.ErrorIs(t, err, ErrColumnMapping)
}
