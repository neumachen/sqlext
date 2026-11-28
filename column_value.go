package sqlext

import (
	"bytes"
	"database/sql/driver"
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
// Its zero value represents NULL. Scan fills it from a query result and
// NewColumnValue builds a valid value; IsNull and Value read it back.
type ColumnValue[T ColumnType] struct {
	value    T
	hasValue bool
}

// NewColumnValue returns a valid, non-NULL ColumnValue holding value. Zero
// values are valid data, and so is a typed nil []byte: NewColumnValue([]byte(nil))
// is not NULL. Use the zero ColumnValue for NULL.
//
// Bytes are not copied: the result shares its backing array with value, and so
// do Value and DriverValue. The caller keeps ownership of value; do not modify
// it while the ColumnValue, or a call that uses its DriverValue, may still read
// it. Scan, by contrast, always copies the bytes it retains.
func NewColumnValue[T ColumnType](value T) ColumnValue[T] {
	return ColumnValue[T]{value: value, hasValue: true}
}

// IsNull reports whether v represents SQL NULL.
func (v ColumnValue[T]) IsNull() bool {
	return !v.hasValue
}

// Value returns the held value, or the zero value of T when v is NULL; use
// IsNull to tell NULL apart from a valid zero value. Bytes are not copied.
func (v ColumnValue[T]) Value() T {
	return v.value
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
		return fmt.Errorf("%w: got %T, want %T", ErrColumnType, src, v.value)
	}
	v.value = value
	v.hasValue = true
	return nil
}

// DriverValue converts v explicitly for use as a query argument. ColumnValue
// does not implement driver.Valuer: its Value method returns T rather than
// (driver.Value, error), so pass the result as the argument instead of v itself.
//
// NULL returns nil, which database/sql sends as SQL NULL. A valid value returns
// Value unchanged with its exact type, including zero values. A valid typed nil
// []byte, which Scan and NewColumnValue treat as valid data, is returned as a
// non-nil driver.Value holding []byte(nil). The default argument conversion of
// database/sql passes it on unchanged; whether it is encoded as empty bytes or
// as SQL NULL depends on the driver. Use the zero ColumnValue to send NULL.
//
// Bytes are not copied: the result shares its backing array with v, so do not
// modify it while a call that uses it may be running.
func (v ColumnValue[T]) DriverValue() driver.Value {
	if !v.hasValue {
		return nil
	}
	return v.value
}
