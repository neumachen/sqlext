package sqlext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestColumnMapper_Columns(t *testing.T) {
	td := TestingDataType{}
	require.NotEmpty(t, td.ColumnBinders())
}
