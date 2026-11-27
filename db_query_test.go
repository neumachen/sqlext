package sqlext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc      string
		assertion func(*testing.T, string)
	}{
		{
			desc: "returns the error from the executor",
			assertion: func(t *testing.T, desc string) {
				preparedStatement, err := PrepareStatement(createCustomerQuery)
				require.NoError(t, err, "failed to convert to positional params")
				rows, err := QueryContext(
					context.TODO(),
					&mockDB{PrepareOk: false},
					preparedStatement,
					nil,
				)
				require.Error(t, err, desc)
				require.Nil(t, rows)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			test.assertion(t, test.desc)
		})
	}
}
