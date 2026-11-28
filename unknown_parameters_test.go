package sqlext

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindParameterValueRejectsUnknownName(t *testing.T) {
	const amountAndID = "SELECT @amount, @id"
	tests := []struct {
		name      string
		statement string
		bind      string
		defaults  BoundParameterValues
	}{
		{name: "typo", statement: amountAndID, bind: "amuont", defaults: BoundParameterValues{1, nil}},
		{name: "case differs", statement: amountAndID, bind: "Amount", defaults: BoundParameterValues{1, nil}},
		{name: "prefix of a name", statement: amountAndID, bind: "amoun", defaults: BoundParameterValues{1, nil}},
		{name: "with the @ prefix", statement: amountAndID, bind: "@amount", defaults: BoundParameterValues{1, nil}},
		{name: "empty name", statement: amountAndID, bind: "", defaults: BoundParameterValues{1, nil}},
		{name: "only inside a literal", statement: "SELECT @amount, '@note', @id", bind: "note", defaults: BoundParameterValues{1, nil}},
		{name: "no named parameters", statement: "SELECT now()", bind: "amount"},
		{name: "only native placeholders", statement: "SELECT $1", bind: "amount"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := mustPrepare(t, tt.statement)
			if tt.defaults != nil {
				require.NoError(t, ps.BindParameterValue("amount", 1))
			}

			err := ps.BindParameterValue(tt.bind, 99)
			require.ErrorIs(t, err, ErrUnknownParameter)
			require.ErrorContains(t, err, `"`+tt.bind+`"`, "the error names the parameter")
			assert.Equal(t, tt.defaults, ps.BoundParameterValues(), "defaults unchanged")

			err = ps.BindParameterValues(BindParameterValue(tt.bind, 99))
			require.ErrorIs(t, err, ErrUnknownParameter)
			assert.Equal(t, tt.defaults, ps.BoundParameterValues(), "defaults unchanged")
		})
	}
}

func TestBindParameterValueKnownNamesStillBind(t *testing.T) {
	ps := mustPrepare(t, "SELECT @a, @b, @a")

	require.NoError(t, ps.BindParameterValue("a", 1), "repeated name")
	require.NoError(t, ps.BindParameterValue("b", nil), "explicit nil is a valid value")
	assert.Equal(t, BoundParameterValues{1, nil, 1}, ps.BoundParameterValues())

	err := ps.BindParameterValues(
		BindParameterValue("b", 2),
		BindParameterValue("c", 3),
		BindParameterValue("a", 4),
	)
	require.ErrorIs(t, err, ErrUnknownParameter)
	assert.Equal(t, BoundParameterValues{1, 2, 1}, ps.BoundParameterValues(),
		"binders before the unknown name are kept, later ones do not run")
}

// A per-call typo must not let the call run with the persistent default, or
// NULL, in place of the value the caller meant to send.
func TestExecutionHelpersRejectUnknownParameterBeforeExecuting(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "UPDATE t SET amount = @amount WHERE id = @id OR parent = @id")
			require.NoError(t, ps.BindParameterValue("amount", 10))

			err := r.run(context.Background(), db, ps,
				BindParameterValue("id", 7),
				BindParameterValue("amuont", 20),
			)
			require.ErrorIs(t, err, ErrUnknownParameter)
			require.ErrorContains(t, err, `"amuont"`)
			assert.Empty(t, db.calls, "nothing sent to the executor")
			assert.Equal(t, BoundParameterValues{10, nil, nil}, ps.BoundParameterValues(), "defaults unchanged")

			require.NoError(t, r.run(context.Background(), db, ps,
				BindParameterValue("id", 7),
				BindParameterValue("amount", 20),
			))
			require.Len(t, db.calls, 1)
			assert.Equal(t, r.method, db.calls[0].method)
			assert.Equal(t, []any{20, 7, 7}, db.calls[0].args, "the next call is unaffected by the failed one")

			require.NoError(t, r.run(context.Background(), db, ps, BindParameterValue("id", 8)))
			require.Len(t, db.calls, 2)
			assert.Equal(t, []any{10, 8, 8}, db.calls[1].args, "the default is still used")
		})
	}
}

func TestExecutionHelpersRejectNamesForStatementWithoutNamedParameters(t *testing.T) {
	for _, r := range runners() {
		for _, statement := range []string{"SELECT now()", "SELECT $1"} {
			t.Run(r.name+"/"+statement, func(t *testing.T) {
				db := newExecutor()
				ps := mustPrepare(t, statement)

				err := r.run(context.Background(), db, ps, BindParameterValue("id", 1))
				require.ErrorIs(t, err, ErrUnknownParameter)
				assert.Empty(t, db.calls)

				require.NoError(t, r.run(context.Background(), db, ps), "without binders the call still runs")
				require.Len(t, db.calls, 1)
				assert.Empty(t, db.calls[0].args)
			})
		}
	}
}

func TestQueryRowReturnsUnknownParameterBeforeExecuting(t *testing.T) {
	db := newExecutor()
	ps := mustPrepare(t, "SELECT @id")

	row, err := QueryRowContext(context.Background(), db, ps, BindParameterValue("ID", 1))
	require.ErrorIs(t, err, ErrUnknownParameter)
	assert.Nil(t, row, "no row when the call never reaches the executor")
	assert.Empty(t, db.calls)

	row, err = QueryRow(db, ps, BindParameterValue("ID", 1))
	require.ErrorIs(t, err, ErrUnknownParameter)
	assert.Nil(t, row)
	assert.Empty(t, db.calls)
}

func TestExecutionHelpersStillSendNullForMissingAndNilValues(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "SELECT @missing, @explicit, @overridden, @kept")
			require.NoError(t, ps.BindParameterValue("overridden", 3))
			require.NoError(t, ps.BindParameterValue("kept", 4))

			require.NoError(t, r.run(context.Background(), db, ps,
				BindParameterValue("explicit", nil),
				BindParameterValue("overridden", nil),
			))
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{nil, nil, nil, 4}, db.calls[0].args,
				"missing and nil-bound parameters are sent as NULL, even over a default")
			assert.Equal(t, BoundParameterValues{nil, nil, 3, 4}, ps.BoundParameterValues())
		})
	}
}

// lenientStatement is a foreign implementation that ignores names it does not
// know; sqlext must leave that decision to it.
type lenientStatement struct {
	foreignStatement
}

func (l lenientStatement) BindParameterValue(name string, value any) error {
	err := l.foreignStatement.BindParameterValue(name, value)
	if errors.Is(err, ErrUnknownParameter) {
		return nil
	}
	return err
}

func (l lenientStatement) BindParameterValues(binderFuncs ...BindParameterValueFunc) error {
	return applyBinders(l, binderFuncs)
}

func TestExecutionHelpersLeaveUnknownNamesToForeignStatements(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			base := mustPrepare(t, "SELECT @a")
			ps := lenientStatement{foreignStatement{PreparedStatement: base, revised: base.Revised()}}

			require.NoError(t, r.run(context.Background(), db, ps,
				BindParameterValue("a", 1),
				BindParameterValue("unknown", 2),
			))
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{1}, db.calls[0].args)
		})
	}
}
