package sqlext

import (
	"context"
	"errors"
	"fmt"

	"github.com/neumachen/sqlext/internal"
)

var (
	errNilExecutor  = errors.New("db connection is nil")
	errNilStatement = errors.New("prepared statement is nil")
	errNilRow       = errors.New("executor returned a nil *sql.Row")
)

// startExecution checks the inputs that would otherwise cause a panic and
// returns the function that resets the statement's bound values. Callers defer
// the reset before checking the error, so values are reset after every call,
// including one rejected for a nil executor. The reset does nothing for a nil
// statement, whose methods cannot be called.
func startExecution(dbExec DBExecutor, preparedStatement PreparedStatement) (reset func(), err error) {
	reset = func() {}
	if !internal.IsNil(preparedStatement) {
		reset = preparedStatement.ResetParametersValues
	}
	if internal.IsNil(dbExec) {
		return reset, errNilExecutor
	}
	if internal.IsNil(preparedStatement) {
		return reset, errNilStatement
	}
	return reset, nil
}

// prepareExecution checks that the rewritten SQL is a single statement and
// applies the binders, all before any database call, and returns the SQL and
// arguments to send. It does not create a server-side prepared statement: the
// helpers execute through the executor's context methods, so database/sql and
// the driver own any statement they need and release it with the result or
// rows.
func prepareExecution(
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	query string,
	args []any,
	err error,
) {
	query = preparedStatement.Revised()
	if err := checkSingleStatement(query); err != nil {
		return "", nil, err
	}

	if bindValuesGiven(binderFuncs) {
		if err := preparedStatement.BindParameterValues(binderFuncs...); err != nil {
			return "", nil, fmt.Errorf("bind parameter values: %w", err)
		}
	}

	return query, preparedStatement.BoundParameterValues(), nil
}

func bindValuesGiven(binderFuncs []BindParameterValueFunc) bool {
	return !internal.IsNilOrZeroValue(binderFuncs)
}

func contextOrBackground(ctx context.Context) context.Context {
	return internal.InitIfNilContext(ctx)
}
