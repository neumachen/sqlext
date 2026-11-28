package sqlext_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/neumachen/sqlext"
)

type exampleContact struct {
	Email string `json:"email"`
}

type exampleCustomer struct {
	ID      int64
	Name    sqlext.ColumnValue[string]
	Contact exampleContact
}

func (c *exampleCustomer) ColumnBinders() sqlext.ColumnBinders {
	return sqlext.DefineColumnBinders(
		sqlext.BindColumn("id", func(v int64) error { c.ID = v; return nil }),
		sqlext.BindNullableColumn("name", func(v sqlext.ColumnValue[string]) error { c.Name = v; return nil }),
		sqlext.BindColumn("contact", func(v []byte) error { return json.Unmarshal(v, &c.Contact) }),
	)
}

func readExampleCustomers(db *sql.DB) (result []exampleCustomer, err error) {
	rows, err := db.QueryContext(context.Background(), "fixture")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var customer exampleCustomer
	scanner, err := sqlext.NewRowScanner(rows, customer.ColumnBinders())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		customer = exampleCustomer{}
		if err := scanner.Scan(); err != nil {
			return nil, err
		}
		result = append(result, customer)
	}
	return result, rows.Err()
}

func ExampleNewRowScanner() {
	db := sql.OpenDB(scanConnector{fixture: &scanFixture{
		columns: []string{"contact", "id", "name"},
		values: [][]driver.Value{
			{[]byte(`{"email":"ada@example.test"}`), int64(7), "Ada"},
			{nil, int64(8), nil},
		},
	}})
	customers, err := readExampleCustomers(db)
	if err = errors.Join(err, db.Close()); err != nil {
		fmt.Println(err)
		return
	}
	for _, customer := range customers {
		fmt.Printf("id=%d name=%q null=%t email=%q\n", customer.ID, customer.Name.Value(), customer.Name.IsNull(), customer.Contact.Email)
	}
	// Output:
	// id=7 name="Ada" null=false email="ada@example.test"
	// id=8 name="" null=true email=""
}
