package sqlext

import (
	"context"
	"database/sql"
)

// Exec executes the prepared SQL statement with the bound parameters.
// It uses the default background context. See ExecContext for details.
func Exec(
	dbPrepExec DBPreparerExecutor,
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	sql.Result,
	error,
) {
	return ExecContext(
		context.Background(),
		dbPrepExec,
		preparedStatement,
		binderFuncs...,
	)
}

// ExecContext binds the parameter values and executes the rewritten statement
// with dbPrepExec.ExecContext. dbPrepExec may be a *sql.DB or a *sql.Tx; the
// caller owns any transaction and commits or rolls it back.
//
// The statement is executed directly rather than through an explicit Prepare,
// so no server-side prepared statement is left behind per call; database/sql
// and the driver manage whatever statement they use for the execution.
//
// The SQL must be a single statement. Trailing semicolons, whitespace and
// comments are allowed, as are semicolons inside string constants, quoted
// identifiers, comments and dollar-quoted strings. Anything else after a
// statement-ending semicolon is rejected with ErrMultipleStatements before the
// database is contacted. The check holds whatever the server's
// standard_conforming_strings setting is: SQL that would be one statement under
// one setting and several under the other is rejected. SQL-standard routine
// bodies (BEGIN ATOMIC ... END) are single statements but are rejected, because
// their inner semicolons cannot be told apart from separators lexically.
//
// An error is returned without contacting the database when dbPrepExec or
// preparedStatement is nil, when the statement check fails, or when a binder
// returns an error; binder errors are wrapped and remain matchable with
// errors.Is and errors.As. Database errors are returned unchanged.
//
// Bound values are reset after every call, whether it succeeds or not, so
// values bound before the call are used by that call only. Parameter names
// that are not bound are sent as NULL, and names that do not occur in the
// statement are ignored.
func ExecContext(
	ctx context.Context,
	dbPrepExec DBPreparerExecutor,
	preparedStatement PreparedStatement,
	binderFuncs ...BindParameterValueFunc,
) (
	sql.Result,
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

	return dbPrepExec.ExecContext(contextOrBackground(ctx), query, args...)
}
