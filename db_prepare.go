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
// returns the function to run when the call ends. Callers defer it before
// checking the error.
//
// For a statement from PrepareStatement it does nothing: directly bound values
// are defaults that persist until ResetParametersValues, and per-call binders
// never touch the statement. For any other implementation it resets the
// statement's bound values after every call, including one rejected for a nil
// executor, as earlier versions did for all statements. Nothing is reset for a
// nil statement, whose methods cannot be called.
//
// executor is whichever single-method executor interface the caller accepts;
// it is only checked for nil here, including a typed nil pointer inside the
// interface, so it is taken as any.
func startExecution(executor any, statement PreparedStatement) (reset func(), err error) {
	reset = func() {}
	if !internal.IsNil(statement) {
		if _, builtIn := statement.(*preparedStatement); !builtIn {
			reset = statement.ResetParametersValues
		}
	}
	if internal.IsNil(executor) {
		return reset, errNilExecutor
	}
	if internal.IsNil(statement) {
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
//
// For a statement from PrepareStatement the binders run on a private copy of
// the defaults taken after the statement check, so the defaults are never
// changed by a call. Other implementations are bound in place as before. The
// returned arguments never share storage with the statement.
func prepareExecution(
	statement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	query string,
	args []any,
	err error,
) {
	query = statement.Revised()
	if err := checkSingleStatement(query); err != nil {
		return "", nil, err
	}

	if builtIn, ok := statement.(*preparedStatement); ok {
		execution := builtIn.newExecution()
		if err := bindParameterValues(execution, binderFuncs); err != nil {
			return "", nil, err
		}
		return query, execution.arguments(), nil
	}

	if err := bindParameterValues(statement, binderFuncs); err != nil {
		return "", nil, err
	}
	return query, copyBoundValues(statement.BoundParameterValues()), nil
}

func bindParameterValues(target PreparedStatement, binderFuncs []BindParameterValueFunc) error {
	if !bindValuesGiven(binderFuncs) {
		return nil
	}
	if err := target.BindParameterValues(binderFuncs...); err != nil {
		return fmt.Errorf("bind parameter values: %w", err)
	}
	return nil
}

func bindValuesGiven(binderFuncs []BindParameterValueFunc) bool {
	return !internal.IsNilOrZeroValue(binderFuncs)
}

func contextOrBackground(ctx context.Context) context.Context {
	return internal.InitIfNilContext(ctx)
}
