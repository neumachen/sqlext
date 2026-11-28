package sqlext

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertColumnValue[T ColumnType](t *testing.T, src T) {
	t.Helper()
	value := ColumnValue[T]{}
	err := value.Scan(src)
	require.NoError(t, err)
	require.False(t, value.IsNull())
	require.Equal(t, src, value.Value())
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
	value := NewColumnValue(int64(42))
	err := value.Scan(nil)
	require.NoError(t, err)
	require.Equal(t, ColumnValue[int64]{}, value)
}

func TestColumnValueNilBytesAreValid(t *testing.T) {
	value := NewColumnValue([]byte("previous"))
	err := value.Scan([]byte(nil))
	require.NoError(t, err)
	require.False(t, value.IsNull())
	require.Nil(t, value.Value())
}

func TestColumnValueOwnsBytes(t *testing.T) {
	src := []byte("data")
	var value ColumnValue[[]byte]
	err := value.Scan(src)
	src[0] = '!'
	require.NoError(t, err)
	require.Equal(t, []byte("data"), value.Value())
}

func TestColumnValueRejectsMismatchedTypes(t *testing.T) {
	type customerID int64
	for _, src := range []any{int(42), "42", float64(42), new(int64), customerID(42), []byte(nil)} {
		t.Run(fmt.Sprintf("%T", src), func(t *testing.T) {
			value := NewColumnValue(int64(7))
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

func TestColumnValueZeroValueIsNull(t *testing.T) {
	var value ColumnValue[string]
	assert.True(t, value.IsNull())
	assert.Empty(t, value.Value())
}

func assertNewColumnValue[T ColumnType](t *testing.T, src T) {
	t.Helper()
	value := NewColumnValue(src)
	assert.False(t, value.IsNull(), "NewColumnValue(%#v) must not be NULL", src)
	assert.Equal(t, src, value.Value())
	assert.IsType(t, src, value.Value())
}

func TestNewColumnValueIsValid(t *testing.T) {
	assertNewColumnValue(t, int64(42))
	assertNewColumnValue(t, 3.5)
	assertNewColumnValue(t, true)
	assertNewColumnValue(t, "Ada")
	assertNewColumnValue(t, []byte("data"))
	assertNewColumnValue(t, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC))
}

func TestNewColumnValueZeroValuesAreValid(t *testing.T) {
	assertNewColumnValue(t, int64(0))
	assertNewColumnValue(t, float64(0))
	assertNewColumnValue(t, false)
	assertNewColumnValue(t, "")
	assertNewColumnValue(t, []byte{})
	assertNewColumnValue(t, time.Time{})
}

func TestNewColumnValueTypedNilBytesAreValid(t *testing.T) {
	value := NewColumnValue([]byte(nil))
	assert.False(t, value.IsNull())
	assert.Nil(t, value.Value())
	assert.NotEqual(t, ColumnValue[[]byte]{}, value)
}

func TestNewColumnValueSharesBytes(t *testing.T) {
	src := []byte("data")
	value := NewColumnValue(src)
	src[0] = '!'
	assert.Equal(t, []byte("!ata"), value.Value(), "NewColumnValue does not copy bytes")
}

func TestColumnValueFailedScanIsNull(t *testing.T) {
	value := NewColumnValue("previous")
	err := value.Scan(int64(1))
	require.ErrorIs(t, err, ErrColumnType)
	assert.True(t, value.IsNull())
	assert.Empty(t, value.Value())
}
