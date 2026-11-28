package sqlext

import (
	"database/sql"
	"fmt"
)

// ColumnScanner supports both legacy MappedRow binding and direct Rows scanning.
type ColumnScanner interface {
	ColumnBinder
	sql.Scanner
}

// BindColumn calls set for an exact, non-NULL value. SQL NULL leaves the
// destination unchanged. All columns are required, including nullable columns.
func BindColumn[T ColumnType](column Column, set func(T) error) ColumnScanner {
	if set == nil {
		return &typedColumn[T]{column: column}
	}
	return BindNullableColumn(column, func(value ColumnValue[T]) error {
		if value.IsNull() {
			return nil
		}
		return set(value.Value())
	})
}

// BindNullableColumn calls set for every value, including SQL NULL
// (IsNull reports true).
func BindNullableColumn[T ColumnType](column Column, set func(ColumnValue[T]) error) ColumnScanner {
	return &typedColumn[T]{column: column, set: set}
}

type typedColumn[T ColumnType] struct {
	column Column
	set    func(ColumnValue[T]) error
}

func (c *typedColumn[T]) Column() Column { return c.column }

func (c *typedColumn[T]) validate() error {
	if c == nil || c.set == nil {
		return fmt.Errorf("nil typed column or setter: %w", ErrColumnMapping)
	}
	if c.column == "" {
		return fmt.Errorf("empty column name: %w", ErrColumnMapping)
	}
	return nil
}

func (c *typedColumn[T]) Scan(src any) error {
	if err := c.validate(); err != nil {
		return err
	}
	var value ColumnValue[T]
	if err := value.Scan(src); err != nil {
		return fmt.Errorf("column %q: %w", c.column, err)
	}
	if err := c.set(value); err != nil {
		return fmt.Errorf("column %q setter: %w", c.column, err)
	}
	return nil
}

func (c *typedColumn[T]) BindColumn(row MappedRow) error {
	if err := c.validate(); err != nil {
		return err
	}
	value, found := row.Get(c.column)
	if !found {
		return fmt.Errorf("required column %q missing: %w", c.column, ErrColumnMapping)
	}
	return c.Scan(value)
}
