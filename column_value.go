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

// DriverValue converts v explicitly for use as a query argument. ColumnValue
// cannot implement driver.Valuer because its Value field takes the method name,
// so pass the result as the argument instead of v itself.
//
// An invalid value returns nil, which database/sql sends as SQL NULL, whatever
// Value holds. A valid value returns Value unchanged with its exact type,
// including zero values. A valid typed nil []byte, which Scan treats as valid
// data, is returned as a non-nil driver.Value holding []byte(nil). The default
// argument conversion of database/sql passes it on unchanged; whether it is
// encoded as empty bytes or as SQL NULL depends on the driver. Use Valid false
// to send NULL.
//
// Bytes are not copied: the result shares its backing array with Value, so do
// not modify it while a call that uses it may be running.
func (v ColumnValue[T]) DriverValue() driver.Value {
	if !v.Valid {
		return nil
	}
	return v.Value
}
