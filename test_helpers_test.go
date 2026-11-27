//go:build integration && pgfixtures

package sqlext

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lib/pq"
	"github.com/neumachen/sqlext/internal"
	"github.com/stretchr/testify/require"
)

// legacyFixtures are the objects created by .dev/migrations that the legacy
// database tests read and write.
var legacyFixtures = []string{
	"testing_datatypes", "customers", "email_addresses", "addresses", "customer_addresses", "create_customer",
}

// ConnectToDatabase opens the disposable PostgreSQL instance selected by the
// integration configuration and fails the test unless the legacy fixtures
// from .dev/migrations are installed. There is no default database.
func ConnectToDatabase(t *testing.T) interface {
	DBPreparerExecutor
	DBCloser
} {
	t.Helper()
	db := OpenDisposablePostgres(t)
	rows, err := db.QueryContext(context.Background(), `
		SELECT name FROM unnest($1::text[]) AS name
		WHERE to_regclass('public.' || name) IS NULL
		  AND to_regproc('public.' || name) IS NULL`, pq.Array(legacyFixtures))
	require.NoError(t, err)
	var missing []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		missing = append(missing, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Empty(t, missing, "legacy fixtures from .dev/migrations are not installed in the test database")
	return db
}

// testingDataTypeColumns are the result columns of the testing_datatypes queries.
func testingDataTypeColumns() Columns {
	return Columns{"testing_datatype_id", "testing_datatype_uuid", "word", "paragraph", "metadata", "created_at"}
}

// customerColumns are the result columns of the customer queries.
func customerColumns() Columns {
	return Columns{"customer_id", "last_name", "first_name", "contact_info", "address"}
}

// CloseDB closes the provided SQL database connection, if it's not nil.
func CloseDB(
	t *testing.T,
	db interface {
		DBPreparerExecutor
		DBCloser
	},
) {
	if !internal.IsNil(db) {
		require.NoError(t, db.Close())
	}
}

// assertMappedCustomer checks that the mapped customer data matches the expected customer.
func assertMappedCustomer(t *testing.T, expectedData Customer, mappedRow MappedRow) {
	require.NotEmpty(t, mappedRow["customer_id"].(int64))
	require.Equal(t, expectedData.LastName, mappedRow["last_name"].(string))
	require.Equal(t, expectedData.FirstName, mappedRow["first_name"].(string))

	cInfo := ContactInfo{}
	err := json.Unmarshal(mappedRow["contact_info"].([]byte), &cInfo)
	require.NoError(t, err)
	require.NotEmpty(t, cInfo.EmailAddressID)
	require.Equal(t, expectedData.ContactInfo.EmailAddress, cInfo.EmailAddress)

	addr := Address{}
	err = json.Unmarshal(mappedRow["address"].([]byte), &addr)
	require.NoError(t, err)
	require.NotEmpty(t, addr.AddressID)
	require.Equal(t, expectedData.Address.StreetNumber, addr.StreetNumber)
	require.Equal(t, expectedData.Address.Route, addr.Route)
	require.Equal(t, expectedData.Address.Locality, addr.Locality)
	require.Equal(t, expectedData.Address.AdministrativeAreaLevel1, addr.AdministrativeAreaLevel1)
	require.Equal(t, expectedData.Address.PostalCode, addr.PostalCode)
	// The coordinates round-trip through NUMERIC and JSON without loss, so they
	// must match exactly.
	require.InDelta(t, expectedData.Address.Latitude.ToFloat64(), addr.Latitude.ToFloat64(), 0)
	require.InDelta(t, expectedData.Address.Longitude.ToFloat64(), addr.Longitude.ToFloat64(), 0)
}

func DeleteRecords(
	t *testing.T,
	db DBPreparerExecutor,
	expectedRowsAffected int,
	query string,
	binderFuncs ...BindParameterValueFunc,
) {
	preparedStatement, err := PrepareStatement(query)
	require.NoError(t, err)
	result, err := ExecContext(
		context.TODO(),
		db,
		preparedStatement,
		binderFuncs...,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	count, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(expectedRowsAffected), count)
}

func CreateNewCustomerForTesting(t *testing.T) *Customer {
	t.Helper()
	db := ConnectToDatabase(t)
	defer CloseDB(t, db)

	createCustomer := NewFakeCustomerData(t)

	preparedStatement, err := PrepareStatement(createCustomerQuery)
	require.NoError(t, err, "failed to convert to positional params")

	rows, err := QueryContext(
		context.TODO(),
		db,
		preparedStatement,
		createCustomer.ParameterValues(t)...,
	)
	require.NoError(t, err, "failed to query creating customer")
	require.NotNil(t, rows)
	mappedRows, err := MapRows(rows)
	require.NoError(t, err, "failed to map rows")
	require.NotNil(t, mappedRows)
	require.Len(t, mappedRows, 1)

	addr := Address{}
	err = json.Unmarshal(mappedRows[0]["address"].([]uint8), &addr)
	require.NoError(t, err)

	createdCustomer := &Customer{
		CustomerID: mappedRows[0]["customer_id"].(int64),
		LastName:   mappedRows[0]["last_name"].(string),
		FirstName:  mappedRows[0]["first_name"].(string),
		Address:    &addr,
	}

	b, err := json.Marshal(createCustomer)
	require.NoError(t, err)

	err = json.Unmarshal(b, &createdCustomer)
	require.NoError(t, err)

	return createdCustomer
}
