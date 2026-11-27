//go:build integration && pgfixtures

package sqlext

// Database-backed tests that need the legacy fixtures in .dev/migrations
// (PostGIS, the customer tables and the create_customer function). They run
// only with -tags "integration pgfixtures" against a disposable server; see
// README.md, "Integration tests".

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaswdr/faker"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestMapColumn(t *testing.T) {
	db := ConnectToDatabase(t)
	defer CloseDB(t, db)

	preparedStatement, err := PrepareStatement(
		insertTestingDataTypeQuery,
	)
	require.NoError(t, err)
	require.NotNil(t, preparedStatement)

	testData := NewFakeTestingDataType(t)

	result, execErr := ExecContext(
		context.Background(),
		db,
		preparedStatement,
		testData.ParameterValues()...,
	)
	require.NoError(t, execErr)
	require.NotNil(t, result)

	t.Run("valid types", func(t *testing.T) {
		selectStatement, err := PrepareStatement(selectTestingDataTypeQuery)
		require.NoError(t, err)
		require.NotNil(t, selectStatement)

		row, err := QueryRowContext(
			context.TODO(),
			db,
			selectStatement,
			BindParameterValue("uuids", pq.Array([]string{testData.UUID})),
		)
		require.NoError(t, err)
		require.NotNil(t, row)
		columns := testingDataTypeColumns()
		mappedRow, err := MapRow(row, columns)
		require.NoError(t, err)
		require.NotNil(t, mappedRow)

		td := TestingDataType{}
		err = td.BindMappedRow(mappedRow)
		require.NoError(t, err)

		require.NotEmpty(t, td.ID)
		require.Equal(t, testData.UUID, td.UUID)
		require.Equal(t, testData.Word, td.Word)
		require.Equal(t, testData.Paragraph, td.Paragraph)
	})

	t.Run("invalid type", func(t *testing.T) {
		invalidDataType := `
			SELECT
				CAST(td.testing_datatype_id AS VARCHAR) AS test_data_type_id,
				td.testing_datatype_uuid,
				td.word,
				td.paragraph,
				td.metadata,
				td.created_at
			FROM testing_datatypes td
			WHERE (nullif(@id, NULL) IS NULL OR td.testing_datatype_id = @id)
			AND (nullif(@ids, '{}') IS NULL OR td.testing_datatype_id = ANY(@ids))
			AND (nullif(@uuid, NULL) IS NULL OR td.testing_datatype_uuid = @uuid)
			AND (nullif(@uuids, '{}') IS NULL OR td.testing_datatype_uuid = ANY(@uuids))
			ORDER BY td.created_at
		`

		invalidStatement, err := PrepareStatement(invalidDataType)
		require.NoError(t, err)
		require.NotNil(t, invalidStatement)

		row, err := QueryRowContext(
			context.TODO(),
			db,
			invalidStatement,
			BindParameterValue(
				"uuids",
				pq.Array([]string{testData.UUID}),
			),
		)
		require.NoError(t, err)
		require.NotNil(t, row)
		columns := testingDataTypeColumns()
		mappedRow, err := MapRow(row, columns)
		require.NoError(t, err)
		require.NotNil(t, mappedRow)

		td := TestingDataType{}
		err = td.BindMappedRow(mappedRow)
		require.Error(t, err)
		require.Equal(
			t,
			"column testing_datatype_id has a type of string and does not match asserted type: int64",
			err.Error(),
		)
	})
}

func TestExecContextPostgres(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		assertion func(*testing.T, string)
	}{
		{
			name: "Insert and delete records with the expected affected rows",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)

				fake := faker.New()
				genUUID := uuid.New()

				setters := []BindParameterValueFunc{
					BindParameterValue("uuid", genUUID.String()),
					BindParameterValue("word", fake.Lorem().Text(10)),
					BindParameterValue("paragraph", fake.Lorem().Text(1000)),
					BindParameterValue("metadata", []byte(`{"test": "foo"}`)),
					BindParameterValue("created_at", time.Now().UTC()),
				}

				preparedStatement, err := PrepareStatement(insertTestingDataTypeQuery)
				require.NoError(t, err, desc)

				result, err := ExecContext(
					context.Background(),
					db,
					preparedStatement,
					setters...,
				)
				require.NoError(t, err, desc)
				affectedRows, err := result.RowsAffected()
				require.NoError(t, err, desc)
				require.Equal(t, int64(1), affectedRows)

				preparedStatement, err = PrepareStatement(deleteTestingDataTypeQuery)
				require.NoError(t, err, desc)
				result, err = ExecContext(
					context.Background(),
					db,
					preparedStatement,
					BindParameterValue("uuid", genUUID.String()),
				)
				require.NoError(t, err, desc)
				require.NotNil(t, result, desc)
				affectedRows, err = result.RowsAffected()
				require.NoError(t, err, desc)
				require.Equal(t, int64(1), affectedRows)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.assertion(t, test.name)
		})
	}
}

func TestQueryContextPostgres(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc      string
		assertion func(*testing.T, string)
	}{
		{
			desc: "Create a customer and map the returned rows",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)

				fakeCustomer := NewFakeCustomerData(t)

				preparedStatement, err := PrepareStatement(createCustomerQuery)
				require.NoError(t, err, "failed to convert to positional params")

				rows, err := QueryContext(
					context.TODO(),
					db,
					preparedStatement,
					fakeCustomer.ParameterValues(t)...,
				)
				require.NoError(t, err, desc)
				require.NotNil(t, rows)
				mappedRows, err := MapRows(rows)
				require.NoError(t, err, desc)
				require.NotNil(t, mappedRows)
				require.Len(t, mappedRows, 1)

				customerID, ok := mappedRows[0]["customer_id"]
				require.True(t, ok)
				require.NotEmpty(t, customerID)

				DeleteRecords(
					t,
					db,
					1,
					deleteCustomerQuery,
					BindParameterValue("customer_id", customerID),
					BindParameterValue("email_address", fakeCustomer.ContactInfo.EmailAddress),
				)
			},
		},
		{
			desc: "Query for records without any parameters",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)
				// The query must return at least one customer; create one
				// instead of relying on data left by other tests.
				CreateNewCustomerForTesting(t)

				preparedStatement, err := PrepareStatement(selectCustomerQuery)
				require.NoError(t, err, desc)
				rows, err := QueryContext(
					context.TODO(),
					db,
					preparedStatement,
					nil,
				)
				require.NoError(t, err, desc)
				require.NotNil(t, rows)

				// Assert that the returned rows contain the expected data
				mappedRows, err := MapRows(rows)
				require.NoError(t, err, desc)
				require.NotNil(t, mappedRows)
				require.NotEmpty(t, mappedRows, "expected at least one customer record to be returned")

				// Assert the specific values in the first returned row
				firstRow := mappedRows[0]
				require.NotEmpty(t, firstRow["customer_id"], "customer_id should not be empty")
				require.NotEmpty(t, firstRow["last_name"], "last_name should not be empty")
				require.NotEmpty(t, firstRow["first_name"], "first_name should not be empty")
				require.NotEmpty(t, firstRow["contact_info"], "contact_info should not be empty")
				require.NotEmpty(t, firstRow["address"], "address should not be empty")
			},
		},
		{
			desc: "Query for records with parameters",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)

				createdCustomer := CreateNewCustomerForTesting(t)

				preparedStatement, err := PrepareStatement(selectCustomerQuery)
				require.NoError(t, err, desc)
				rows, err := QueryContext(
					context.TODO(),
					db,
					preparedStatement,
					BindParameterValue("customer_id", createdCustomer.CustomerID),
					BindParameterValue("email_address", createdCustomer.ContactInfo.EmailAddress),
				)
				require.NoError(t, err, desc)
				require.NotNil(t, rows)
				mappedRows, err := MapRows(rows)
				require.NoError(t, err, desc)
				require.NotNil(t, mappedRows)
				require.NotEmpty(t, mappedRows)
				mappedRow := mappedRows[0]
				assertMappedCustomer(t, *createdCustomer, mappedRow)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			test.assertion(t, test.desc)
		})
	}
}

func TestQueryRowContextPostgres(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc      string
		assertion func(*testing.T, string)
	}{
		{
			desc: "Create a record and expect the result",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)

				fakeCustomer := NewFakeCustomerData(t)

				preparedStatement, err := PrepareStatement(createCustomerQuery)
				require.NoError(t, err, "failed to convert to positional params")

				row, err := QueryRowContext(
					context.TODO(),
					db,
					preparedStatement,
					fakeCustomer.ParameterValues(t)...,
				)
				require.NoError(t, err, desc)
				require.NotNil(t, row)

				columns := customerColumns()

				mappedRow, err := MapRow(row, columns)
				require.NoError(t, err)
				require.NotNil(t, mappedRow)

				assertMappedCustomer(t, fakeCustomer, mappedRow)

				DeleteRecords(
					t,
					db,
					1,
					deleteCustomerQuery,
					BindParameterValue("customer_id", mappedRow["customer_id"].(int64)),
					BindParameterValue("email_address", fakeCustomer.ContactInfo.EmailAddress),
				)
			},
		},
		{
			desc: "Query a record without any parameters",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)

				preparedStatement, err := PrepareStatement(selectCustomerQuery)
				require.NoError(t, err, desc)
				row, err := QueryRowContext(
					context.TODO(),
					db,
					preparedStatement,
				)
				require.NoError(t, err, desc)
				require.NotNil(t, row)
			},
		},
		{
			desc: "Query a record with parameters",
			assertion: func(t *testing.T, desc string) {
				db := ConnectToDatabase(t)
				defer CloseDB(t, db)

				createdCustomer := CreateNewCustomerForTesting(t)

				preparedStatement, err := PrepareStatement(selectCustomerQuery)
				require.NoError(t, err, desc)
				row, err := QueryRowContext(
					context.TODO(),
					db,
					preparedStatement,
					BindParameterValue("customer_id", createdCustomer.CustomerID),
					BindParameterValue("email_address", createdCustomer.ContactInfo.EmailAddress),
				)
				require.NoError(t, err, desc)
				require.NotNil(t, row)
				mappedRow, err := MapRow(row, customerColumns())
				require.NoError(t, err, desc)
				require.NotNil(t, mappedRow)
				require.NotEmpty(t, mappedRow)
				assertMappedCustomer(t, *createdCustomer, mappedRow)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			test.assertion(t, test.desc)
		})
	}
}
