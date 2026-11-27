package sqlext_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/neumachen/sqlext"
)

// Map iteration order is unspecified, so these tests compare column sets with
// ElementsMatch rather than relying on any particular order.

func TestMappedRowColumns(t *testing.T) {
	tests := []struct {
		name string
		row  sqlext.MappedRow
		want sqlext.Columns
	}{
		{
			name: "single column",
			row:  sqlext.MappedRow{idColumn: int64(1)},
			want: sqlext.Columns{idColumn},
		},
		{
			name: "multiple columns",
			row: sqlext.MappedRow{
				idColumn:   int64(1),
				nameColumn: nil,
				dataColumn: []byte("payload"),
			},
			want: sqlext.Columns{idColumn, nameColumn, dataColumn},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.row.Columns()

			assert.ElementsMatch(t, tt.want, got,
				"Columns must return every key of the row exactly once")
		})
	}
}

func TestMappedRowColumnsWithoutColumnsIsNil(t *testing.T) {
	tests := []struct {
		name string
		row  sqlext.MappedRow
	}{
		{name: "nil row", row: nil},
		{name: "empty row", row: sqlext.MappedRow{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Nil(t, tt.row.Columns())
		})
	}
}

func TestMappedRowsColumnsReturnsFirstRowColumns(t *testing.T) {
	rows := sqlext.MappedRows{
		{idColumn: int64(1), nameColumn: adaName},
		{dataColumn: []byte("ignored")},
	}

	assert.ElementsMatch(t, sqlext.Columns{idColumn, nameColumn}, rows.Columns(),
		"MappedRows.Columns reports the columns of the first row only")
}

func TestMappedRowsColumnsWithoutRowsIsNil(t *testing.T) {
	tests := []struct {
		name string
		rows sqlext.MappedRows
	}{
		{name: "nil rows", rows: nil},
		{name: "empty rows", rows: sqlext.MappedRows{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Nil(t, tt.rows.Columns())
		})
	}
}
