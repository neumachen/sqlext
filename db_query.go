package sqlext

import (
	"context"
	"database/sql"
)

// Query executes the prepared SQL statement as a query with the bound parameters.
// It uses the default background context. See QueryContext for details.
func Query(
	executor DBQueryerContext,
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	*sql.Rows,
	error,
) {
	return QueryContext(
		context.Background(),
		executor,
		preparedStatement,
		binderFuncs...,
	)
}

// QueryContext binds the parameter values and runs the rewritten statement
// with executor.QueryContext, the only executor method it calls. executor may
// be a *sql.DB, a *sql.Tx or a *sql.Conn.
//
// The caller owns the returned rows and must close them, either by reading
// them until Next returns false or by calling Close. Inside a transaction,
// finish or close the rows before Commit or Rollback; database/sql cancels rows
// that are still open when the transaction ends. No server-side prepared
// statement outlives the rows.
//
// The single-statement rule, the pre-execution errors, the binding rules, the
// handling of defaults and per-call values, and the concurrency rules are the
// same as for ExecContext.
func QueryContext(
	ctx context.Context,
	executor DBQueryerContext,
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	*sql.Rows,
	error,
) {
	reset, err := startExecution(executor, preparedStatement)
	defer reset()
	if err != nil {
		return nil, err
	}

	query, args, err := prepareExecution(preparedStatement, binderFuncs...)
	if err != nil {
		return nil, err
	}

	return executor.QueryContext(contextOrBackground(ctx), query, args...)
}
