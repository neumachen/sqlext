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
// For a statement from PrepareStatement, values bound directly on it are
// defaults that every call sends until ResetParametersValues clears them; a
// call never resets them, whether it succeeds or fails. binderFuncs override
// the defaults for this call only: the call copies the defaults after the
// statement check and applies its binders to that copy, without holding the
// statement's lock, so overlapping calls on a shared statement never see each
// other's values. The binders therefore receive this per-call state, not the
// statement itself. The copy of the defaults and the arguments given to the
// executor are shallow copies: the slice is copied, the values in it are not.
// A bound pointer, map, slice or []byte is shared with the caller and with
// other calls, so do not modify it while a call that uses it may be running.
//
// Any other PreparedStatement implementation is handled as in earlier
// versions: the binders are applied to the statement itself, and its
// ResetParametersValues is called after every call, whether it succeeds or
// not. sqlext does not synchronize such statements; whether one may be shared
// between goroutines depends on the implementation.
//
// Parameter names that are not bound are sent as NULL, a parameter bound to
// nil is sent as NULL even when a default is set, and names that do not occur
// in the statement are ignored.
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
