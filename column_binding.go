package sqlext

import (
	"errors"

	"github.com/neumachen/sqlext/internal"
)

// DefineColumnBinding creates a new ColumnBinder that binds column from a
// MappedRow through binderFunc.
//
// Example:
//
//	binder := DefineColumnBinding("id", BindColumnToField(func(v int64) error {
//		record.ID = v
//		return nil
//	}))
func DefineColumnBinding(
	column Column,
	binderFunc ColumnBinderFunc,
) ColumnBinder {
	return &columnBinding{
		column:     column,
		binderFunc: binderFunc,
	}
}

type columnBinding struct {
	column     Column
	binderFunc ColumnBinderFunc
}

func (c columnBinding) Column() Column {
	return c.column
}

func (c columnBinding) BindColumn(mappedRow MappedRow) error {
	if internal.IsNil(c.binderFunc) {
		return errors.New("column binding has nil binder func")
	}
	return c.binderFunc(mappedRow, c)
}
