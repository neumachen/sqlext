package sqlext_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/neumachen/sqlext"
)

const dataColumn = "data"

func TestMapRowsMapsEveryRow(t *testing.T) {
	rows := openScanRows(t, &scanFixture{
		columns: []string{idColumn, nameColumn, dataColumn},
		values: [][]driver.Value{
			{int64(1), adaName, []byte("first")},
			{int64(2), nil, []byte("second")},
		},
	})

	mapped, err := sqlext.MapRows(rows)

	require.NoError(t, err)
	assert.Equal(t, sqlext.MappedRows{
		{idColumn: int64(1), nameColumn: adaName, dataColumn: []byte("first")},
		{idColumn: int64(2), nameColumn: nil, dataColumn: []byte("second")},
	}, mapped)
}

func TestMapRowsWithoutRowsReturnsEmptyResult(t *testing.T) {
	rows := openScanRows(t, &scanFixture{columns: []string{idColumn}})

	mapped, err := sqlext.MapRows(rows)

	require.NoError(t, err)
	assert.NotNil(t, mapped, "an empty result is an empty, non-nil MappedRows")
	assert.Empty(t, mapped)
}

// A driver error from Next ends iteration early; database/sql reports it only
// through rows.Err, so MapRows must not hand back the rows read before it as a
// complete result.
func TestMapRowsReturnsIterationError(t *testing.T) {
	errIteration := errors.New("iteration failed")
	tests := []struct {
		name   string
		values [][]driver.Value
	}{
		{name: "after rows", values: [][]driver.Value{{int64(1)}, {int64(2)}}},
		{name: "before any row", values: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &scanFixture{columns: []string{idColumn}, values: tt.values, iterationErr: errIteration}
			rows := openScanRows(t, fixture)

			mapped, err := sqlext.MapRows(rows)

			require.ErrorIs(t, err, errIteration)
			assert.Nil(t, mapped)
			assert.Equal(t, len(tt.values)+1, fixture.nextCalls, "iteration stops at the failing Next")
		})
	}
}
