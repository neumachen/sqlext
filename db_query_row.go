package sqlext

import (
	"context"
	"database/sql"
)

// QueryRow executes the prepared SQL statement as a query with the bound parameters.
// It uses the default background context. See QueryRowContext for details.
func QueryRow(
	dbPrepExec DBPreparerExecutor,
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	*sql.Row,
	error,
) {
	return QueryRowContext(
		context.Background(),
		dbPrepExec,
		preparedStatement,
		binderFuncs...,
	)
}

// QueryRowContext binds the parameter values and runs the rewritten statement
// with dbPrepExec.QueryRowContext. dbPrepExec may be a *sql.DB or a *sql.Tx.
//
// As with database/sql, database errors, including errors that previously
// came from preparing the statement, are reported by the returned row's Scan
// method, and Scan returns sql.ErrNoRows when there is no row. The error
// result is non-nil only for problems found before the database is contacted:
// nil arguments, a statement that is not a single statement, or a binder
// error. Inside a transaction, call Scan before Commit or Rollback.
//
// The single-statement rule, the binding rules and the reset of bound values
// are the same as for ExecContext.
func QueryRowContext(
	ctx context.Context,
	dbPrepExec DBPreparerExecutor,
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	*sql.Row,
	error,
) {
	reset, err := startExecution(dbPrepExec, preparedStatement)
	defer reset()
	if err != nil {
		return nil, err
	}

	query, args, err := prepareExecution(preparedStatement, binderFuncs...)
	if err != nil {
		return nil, err
	}

	row := dbPrepExec.QueryRowContext(contextOrBackground(ctx), query, args...)
	if row == nil {
		// database/sql never returns a nil row, but other executors can, and
		// Scan on a nil *sql.Row panics.
		return nil, errNilRow
	}

	return row, nil
}
