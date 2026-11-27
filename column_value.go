package sqlext

import (
	"bytes"
	"errors"
	"fmt"
	"time"
)

// ColumnType is the exact set of source types accepted by typed column bindings.
// Named types require an explicit conversion in the caller's callback.
type ColumnType interface {
	int64 | float64 | bool | string | []byte | time.Time
}

var (
	ErrColumnType    = errors.New("sqlext: column value has an unexpected type")
	ErrColumnMapping = errors.New("sqlext: invalid column mapping")
)

// ColumnValue preserves SQL NULL separately from valid zero values.
// Its zero value represents NULL.
type ColumnValue[T ColumnType] struct {
	Value T
	Valid bool
}

// Scan implements sql.Scanner without implicit type conversion. A nil source
// represents SQL NULL; a typed nil []byte is a valid byte value. Retained bytes
// are copied. NULL and errors clear any previously scanned value.
func (v *ColumnValue[T]) Scan(src any) error {
	if v == nil {
		return fmt.Errorf("nil column value destination: %w", ErrColumnMapping)
	}
	*v = ColumnValue[T]{}
	if src == nil {
		return nil
	}
	if raw, ok := src.([]byte); ok {
		src = bytes.Clone(raw)
	}
	value, ok := src.(T)
	if !ok {
		return fmt.Errorf("%w: got %T, want %T", ErrColumnType, src, v.Value)
	}
	v.Value = value
	v.Valid = true
	return nil
}
