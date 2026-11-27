package sqlext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		assertion func(*testing.T, string)
	}{
		{
			name: "returns the error from the executor",
			assertion: func(t *testing.T, desc string) {
				preparedStatement, err := PrepareStatement(insertTestingDataTypeQuery)
				require.NoError(t, err, desc)
				result, err := ExecContext(
					context.Background(),
					&mockDB{
						PrepareOk: false,
					},
					preparedStatement,
					nil,
				)
				require.Error(t, err, desc)
				require.Nil(t, result)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.assertion(t, test.name)
		})
	}
}
