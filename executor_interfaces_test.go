package sqlext

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The helpers accept exactly these signatures; a change to them breaks callers
// that store a helper as a function value, so it must be deliberate.
var (
	_ func(DBExecerContext, PreparedStatement, ...BindParameterValueFunc) (sql.Result, error)                    = Exec
	_ func(context.Context, DBExecerContext, PreparedStatement, ...BindParameterValueFunc) (sql.Result, error)   = ExecContext
	_ func(DBQueryerContext, PreparedStatement, ...BindParameterValueFunc) (*sql.Rows, error)                    = Query
	_ func(context.Context, DBQueryerContext, PreparedStatement, ...BindParameterValueFunc) (*sql.Rows, error)   = QueryContext
	_ func(DBQueryRowerContext, PreparedStatement, ...BindParameterValueFunc) (*sql.Row, error)                  = QueryRow
	_ func(context.Context, DBQueryRowerContext, PreparedStatement, ...BindParameterValueFunc) (*sql.Row, error) = QueryRowContext
)

// Every database/sql executor, including a reserved *sql.Conn, and every
// existing composite interface satisfies the narrow interfaces, so callers
// that passed them before still compile.
var (
	_ DBExecerContext     = (*sql.DB)(nil)
	_ DBExecerContext     = (*sql.Tx)(nil)
	_ DBExecerContext     = (*sql.Conn)(nil)
	_ DBExecerContext     = DBExecutor(nil)
	_ DBExecerContext     = DBPreparerExecutor(nil)
	_ DBExecerContext     = DB(nil)
	_ DBQueryerContext    = (*sql.DB)(nil)
	_ DBQueryerContext    = (*sql.Tx)(nil)
	_ DBQueryerContext    = (*sql.Conn)(nil)
	_ DBQueryerContext    = DBExecutor(nil)
	_ DBQueryerContext    = DBPreparerExecutor(nil)
	_ DBQueryerContext    = DB(nil)
	_ DBQueryRowerContext = (*sql.DB)(nil)
	_ DBQueryRowerContext = (*sql.Tx)(nil)
	_ DBQueryRowerContext = (*sql.Conn)(nil)
	_ DBQueryRowerContext = DBExecutor(nil)
	_ DBQueryRowerContext = DBPreparerExecutor(nil)
	_ DBQueryRowerContext = DB(nil)
)

type callLog struct{ calls []executorCall }

func (l *callLog) add(method string, ctx context.Context, query string, args []any) {
	l.calls = append(l.calls, executorCall{method: method, ctx: ctx, query: query, args: args})
}

// execOnly, queryOnly and queryRowOnly each have nothing but the one method
// their helpers call. Calling a method on a typed nil pointer of these types
// panics, so a test that does not panic also shows the method was not called.
type execOnly struct{ log *callLog }

func (e *execOnly) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	e.log.add(methodExec, ctx, query, args)
	return driverResult{}, nil
}

type queryOnly struct{ log *callLog }

func (q *queryOnly) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.log.add(methodQuery, ctx, query, args)
	return nil, nil
}

type queryRowOnly struct{ log *callLog }

func (q *queryRowOnly) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.log.add(methodQueryRow, ctx, query, args)
	return &sql.Row{}
}

// narrowExecutors holds one executor per helper family, so a runner can pick
// the one its helper accepts.
type narrowExecutors struct {
	exec     DBExecerContext
	query    DBQueryerContext
	queryRow DBQueryRowerContext
}

func singleMethodExecutors() (narrowExecutors, *callLog) {
	log := &callLog{}
	return narrowExecutors{
		exec:     &execOnly{log: log},
		query:    &queryOnly{log: log},
		queryRow: &queryRowOnly{log: log},
	}, log
}

func nilNarrowExecutors() map[string]narrowExecutors {
	var conn *sql.Conn
	return map[string]narrowExecutors{
		"untyped nil": {},
		"typed nil single-method executor": {
			exec:     (*execOnly)(nil),
			query:    (*queryOnly)(nil),
			queryRow: (*queryRowOnly)(nil),
		},
		"typed nil *sql.Conn": {exec: conn, query: conn, queryRow: conn},
	}
}

type narrowRunner struct {
	name   string
	method string
	run    func(context.Context, narrowExecutors, PreparedStatement, ...BindParameterValueFunc) error
}

func narrowRunners() []narrowRunner {
	return []narrowRunner{
		{"Exec", methodExec, func(_ context.Context, ex narrowExecutors, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := Exec(ex.exec, ps, b...)
			return err
		}},
		{methodExec, methodExec, func(ctx context.Context, ex narrowExecutors, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := ExecContext(ctx, ex.exec, ps, b...)
			return err
		}},
		{"Query", methodQuery, func(_ context.Context, ex narrowExecutors, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := Query(ex.query, ps, b...)
			return err
		}},
		{methodQuery, methodQuery, func(ctx context.Context, ex narrowExecutors, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := QueryContext(ctx, ex.query, ps, b...)
			return err
		}},
		{"QueryRow", methodQueryRow, func(_ context.Context, ex narrowExecutors, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := QueryRow(ex.queryRow, ps, b...)
			return err
		}},
		{methodQueryRow, methodQueryRow, func(ctx context.Context, ex narrowExecutors, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := QueryRowContext(ctx, ex.queryRow, ps, b...)
			return err
		}},
	}
}

func TestExecutionHelpersAcceptSingleMethodExecutors(t *testing.T) {
	for _, r := range narrowRunners() {
		t.Run(r.name, func(t *testing.T) {
			executors, log := singleMethodExecutors()
			ps := mustPrepare(t, "SELECT @a, @b")
			require.NoError(t, ps.BindParameterValue("a", 1))
			ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

			require.NoError(t, r.run(ctx, executors, ps, BindParameterValue("b", 2)))
			require.NoError(t, r.run(ctx, executors, ps))

			require.Len(t, log.calls, 2, "one call to the helper's own method per call")
			for _, call := range log.calls {
				assert.Equal(t, r.method, call.method)
				assert.Equal(t, "SELECT $1, $2", call.query)
			}
			assert.Equal(t, []any{1, 2}, log.calls[0].args, "default and per-call value")
			assert.Equal(t, []any{1, nil}, log.calls[1].args, "default persists, override not kept")
			assert.Equal(t, BoundParameterValues{1, nil}, ps.BoundParameterValues())
			if r.name == r.method {
				assert.Equal(t, "marker", log.calls[0].ctx.Value(ctxKey{}), "caller context reaches the executor")
			} else {
				assert.Nil(t, log.calls[0].ctx.Value(ctxKey{}), "non-context variant uses a background context")
			}
		})
	}
}

// Nil checks, kept defaults and the reset of foreign statements behave for
// narrow executors, including a typed nil *sql.Conn, as for the composites.
func TestExecutionHelpersRejectNilSingleMethodExecutors(t *testing.T) {
	for _, r := range narrowRunners() {
		for name, executors := range nilNarrowExecutors() {
			t.Run(r.name+"/"+name+"/built-in statement", func(t *testing.T) {
				ps := mustPrepare(t, "SELECT @a, @b")
				require.NoError(t, ps.BindParameterValue("a", 1))
				binderCalled := false

				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), executors, ps, func(PreparedStatement) error {
						binderCalled = true
						return nil
					})
				})
				require.ErrorIs(t, err, errNilExecutor)
				assert.False(t, binderCalled, "binders do not run without an executor")
				assert.Equal(t, BoundParameterValues{1, nil}, ps.BoundParameterValues(), "defaults kept")
			})

			t.Run(r.name+"/"+name+"/foreign statement", func(t *testing.T) {
				base := mustPrepare(t, "SELECT @a, @b")
				ps := foreignStatement{PreparedStatement: base, revised: base.Revised()}
				require.NoError(t, ps.BindParameterValue("a", 1))

				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), executors, ps)
				})
				require.ErrorIs(t, err, errNilExecutor)
				assert.Equal(t, BoundParameterValues{nil, nil}, ps.BoundParameterValues(), "foreign statement reset")
			})
		}

		for name, ps := range nilStatements() {
			t.Run(r.name+"/"+name+" statement", func(t *testing.T) {
				executors, log := singleMethodExecutors()
				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), executors, ps)
				})
				require.ErrorIs(t, err, errNilStatement)
				assert.Empty(t, log.calls)
			})
		}
	}
}

func TestQueryRowRejectsNilRowFromSingleMethodExecutor(t *testing.T) {
	row, err := QueryRowContext(context.Background(), nilRowExecutor{}, mustPrepare(t, "SELECT 1"))
	require.ErrorIs(t, err, errNilRow)
	assert.Nil(t, row)
}

type nilRowExecutor struct{}

func (nilRowExecutor) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
