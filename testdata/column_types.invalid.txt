package contract

import "github.com/neumachen/sqlext"

type CustomID int64

var _ = sqlext.BindColumn("id", func(int) error { return nil })
var _ = sqlext.BindColumn("score", func(float32) error { return nil })
var _ sqlext.ColumnValue[*int64]
var _ = sqlext.BindColumn("data", func(map[string]string) error { return nil })
var _ = sqlext.BindColumn("object", func(struct{}) error { return nil })
var _ = sqlext.BindColumn("id", func(CustomID) error { return nil })
var _ = sqlext.BindNullableColumn[any]("data", nil)
