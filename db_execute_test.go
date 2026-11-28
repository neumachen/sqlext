package sqlext

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	methodExec     = "ExecContext"
	methodQuery    = "QueryContext"
	methodQueryRow = "QueryRowContext"
)

type executorCall struct {
	method string
	ctx    context.Context
	query  string
	args   []any
}

// recordingExecutor records every call. Prepare and the non-context methods
// are recorded too, so tests can assert that the helpers never use them.
type recordingExecutor struct {
	calls []executorCall
	err   error
	row   *sql.Row
}

func (r *recordingExecutor) record(method string, ctx context.Context, query string, args []any) {
	r.calls = append(r.calls, executorCall{method: method, ctx: ctx, query: query, args: args})
}

func (r *recordingExecutor) Prepare(query string) (*sql.Stmt, error) {
	r.record("Prepare", nil, query, nil)
	return nil, errors.New("unexpected Prepare")
}

func (r *recordingExecutor) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	r.record("PrepareContext", ctx, query, nil)
	return nil, errors.New("unexpected PrepareContext")
}

func (r *recordingExecutor) Exec(query string, args ...any) (sql.Result, error) {
	r.record("Exec", nil, query, args)
	return nil, r.err
}

func (r *recordingExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.record(methodExec, ctx, query, args)
	if r.err != nil {
		return nil, r.err
	}
	return driverResult{}, nil
}

func (r *recordingExecutor) Query(query string, args ...any) (*sql.Rows, error) {
	r.record("Query", nil, query, args)
	return nil, r.err
}

func (r *recordingExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.record(methodQuery, ctx, query, args)
	return nil, r.err
}

func (r *recordingExecutor) QueryRow(query string, args ...any) *sql.Row {
	r.record("QueryRow", nil, query, args)
	return r.row
}

func (r *recordingExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.record(methodQueryRow, ctx, query, args)
	return r.row
}

type driverResult struct{}

func (driverResult) LastInsertId() (int64, error) { return 0, nil }
func (driverResult) RowsAffected() (int64, error) { return 1, nil }

// runner calls one execution helper and reports only its error.
type runner struct {
	name   string
	method string
	run    func(context.Context, DBPreparerExecutor, PreparedStatement, ...BindParameterValueFunc) error
}

func runners() []runner {
	return []runner{
		{"Exec", methodExec, func(_ context.Context, db DBPreparerExecutor, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := Exec(db, ps, b...)
			return err
		}},
		{methodExec, methodExec, func(ctx context.Context, db DBPreparerExecutor, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := ExecContext(ctx, db, ps, b...)
			return err
		}},
		{"Query", methodQuery, func(_ context.Context, db DBPreparerExecutor, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := Query(db, ps, b...)
			return err
		}},
		{methodQuery, methodQuery, func(ctx context.Context, db DBPreparerExecutor, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := QueryContext(ctx, db, ps, b...)
			return err
		}},
		{"QueryRow", methodQueryRow, func(_ context.Context, db DBPreparerExecutor, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := QueryRow(db, ps, b...)
			return err
		}},
		{methodQueryRow, methodQueryRow, func(ctx context.Context, db DBPreparerExecutor, ps PreparedStatement, b ...BindParameterValueFunc) error {
			_, err := QueryRowContext(ctx, db, ps, b...)
			return err
		}},
	}
}

// newExecutor returns an executor whose QueryRow methods return a usable row,
// so that the nil-row guard does not mask the behavior under test.
func newExecutor() *recordingExecutor {
	return &recordingExecutor{row: &sql.Row{}}
}

func mustPrepare(t *testing.T, query string) PreparedStatement {
	t.Helper()
	ps, err := PrepareStatement(query)
	require.NoError(t, err)
	return ps
}

func TestExecutionHelpersExecuteDirectlyWithBoundValues(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "SELECT @a, @b, @a, @c, @z, @f, @s;")

			err := r.run(context.Background(), db, ps,
				BindParameterValue("a", 0),
				BindParameterValue("b", "x"),
				BindParameterValue("c", nil),
				BindParameterValue("f", false),
				BindParameterValue("s", ""),
				BindParameterValue("unknown", 99),
			)
			require.NoError(t, err)
			require.Len(t, db.calls, 1, "exactly one executor call and no Prepare")
			call := db.calls[0]
			assert.Equal(t, r.method, call.method)
			assert.Equal(t, "SELECT $1, $2, $3, $4, $5, $6, $7;", call.query)
			assert.Equal(t, []any{0, "x", 0, nil, nil, false, ""}, call.args,
				"repeated names bound everywhere, zero values kept, explicit nil and missing names sent as nil")
			assert.Equal(t, BoundParameterValues{nil, nil, nil, nil, nil, nil, nil}, ps.BoundParameterValues(),
				"per-call values do not become defaults")
		})
	}
}

func TestExecutionHelpersWithoutParameters(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			require.NoError(t, r.run(context.Background(), db, mustPrepare(t, "SELECT ';' AS a, $$;$$ AS b; -- done")))
			require.Len(t, db.calls, 1)
			assert.Equal(t, "SELECT ';' AS a, $$;$$ AS b; -- done", db.calls[0].query)
			assert.Empty(t, db.calls[0].args)
		})
	}
}

func TestExecutionHelpersRejectMultipleStatementsBeforeExecuting(t *testing.T) {
	statements := map[string]string{
		"without parameters": "INSERT INTO t VALUES (1); INSERT INTO t VALUES (2)",
		"with parameters":    "INSERT INTO t VALUES (@a); DELETE FROM t",
		"hidden after quote": separatorHiddenWhenOff,
	}
	for _, r := range runners() {
		for name, query := range statements {
			t.Run(r.name+"/"+name, func(t *testing.T) {
				db := newExecutor()
				ps := mustPrepare(t, query)
				if ps.ParameterPositions() != nil {
					require.NoError(t, ps.BindParameterValue("a", 1))
				}
				binderCalled := false
				err := r.run(context.Background(), db, ps, func(PreparedStatement) error {
					binderCalled = true
					return nil
				})
				require.ErrorIs(t, err, ErrMultipleStatements)
				assert.Empty(t, db.calls, "nothing sent to the executor")
				assert.False(t, binderCalled, "binders do not run for a rejected statement")
				if ps.ParameterPositions() != nil {
					assert.Equal(t, BoundParameterValues{1}, ps.BoundParameterValues(), "defaults kept after a rejected call")
				}
			})
		}
	}
}

func TestExecutionHelpersBinderErrorStopsBeforeExecuting(t *testing.T) {
	boom := errors.New("binder failed")
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "SELECT @a, @b")
			require.NoError(t, ps.BindParameterValue("a", 1))

			err := r.run(context.Background(), db, ps,
				BindParameterValue("b", 2),
				func(PreparedStatement) error { return fmt.Errorf("wrapped: %w", boom) },
			)
			require.ErrorIs(t, err, boom)
			assert.Empty(t, db.calls)
			assert.Equal(t, BoundParameterValues{1, nil}, ps.BoundParameterValues(), "defaults kept, override not stored")

			require.NoError(t, r.run(context.Background(), db, ps))
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{1, nil}, db.calls[0].args, "failed binding does not leak into the next call")
		})
	}
}

// nilExecutors returns untyped and typed nil executors. Calling any method on
// the typed nil *recordingExecutor would panic, so a test that does not panic
// also shows that no executor method was called.
func nilExecutors() map[string]DBPreparerExecutor {
	var (
		typedNilDB       *sql.DB
		typedNilExecutor *recordingExecutor
	)
	return map[string]DBPreparerExecutor{
		"nil":                nil,
		"typed nil *sql.DB":  typedNilDB,
		"typed nil executor": typedNilExecutor,
	}
}

func nilStatements() map[string]PreparedStatement {
	var typedNilStmt *preparedStatement
	return map[string]PreparedStatement{
		"nil":       nil,
		"typed nil": typedNilStmt,
	}
}

func TestExecutionHelpersRejectNilInputs(t *testing.T) {
	for _, r := range runners() {
		for name, db := range nilExecutors() {
			t.Run(r.name+"/executor "+name, func(t *testing.T) {
				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), db, mustPrepare(t, "SELECT 1"))
				})
				require.ErrorIs(t, err, errNilExecutor)
			})
		}
		for name, ps := range nilStatements() {
			t.Run(r.name+"/statement "+name, func(t *testing.T) {
				db := newExecutor()
				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), db, ps)
				})
				require.ErrorIs(t, err, errNilStatement)
				assert.Empty(t, db.calls)
			})
		}
	}
}

// A call rejected for a nil executor keeps the defaults of a statement from
// PrepareStatement and runs none of its binders.
func TestExecutionHelpersKeepDefaultsWhenExecutorIsNil(t *testing.T) {
	for _, r := range runners() {
		for name, nilExecutor := range nilExecutors() {
			t.Run(r.name+"/executor "+name, func(t *testing.T) {
				ps := mustPrepare(t, "SELECT @a, @b, @a")
				require.NoError(t, ps.BindParameterValue("a", 1))
				require.NoError(t, ps.BindParameterValue("b", 2))
				binderCalled := false

				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), nilExecutor, ps, func(PreparedStatement) error {
						binderCalled = true
						return nil
					})
				})
				require.ErrorIs(t, err, errNilExecutor)
				assert.False(t, binderCalled, "binders do not run without an executor")
				assert.Equal(t, BoundParameterValues{1, 2, 1}, ps.BoundParameterValues(), "defaults kept")

				db := newExecutor()
				require.NoError(t, r.run(context.Background(), db, ps))
				require.NoError(t, r.run(context.Background(), db, ps, BindParameterValue("b", 3)))
				require.Len(t, db.calls, 2)
				assert.Equal(t, []any{1, 2, 1}, db.calls[0].args, "defaults sent after the failed call")
				assert.Equal(t, []any{1, 3, 1}, db.calls[1].args)
			})
		}
		for name, ps := range nilStatements() {
			t.Run(r.name+"/nil executor and "+name+" statement", func(t *testing.T) {
				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), nil, ps)
				})
				require.ErrorIs(t, err, errNilExecutor, "the executor is checked first, as before")
			})
		}
	}
}

// Statements not created by PrepareStatement keep the earlier behavior: a
// failed call still resets their bound values, including when the executor is
// nil.
func TestExecutionHelpersResetForeignStatementWhenExecutorIsNil(t *testing.T) {
	for _, r := range runners() {
		for name, nilExecutor := range nilExecutors() {
			t.Run(r.name+"/executor "+name, func(t *testing.T) {
				base := mustPrepare(t, "SELECT @a, @b, @a")
				ps := foreignStatement{PreparedStatement: base, revised: base.Revised()}
				require.NoError(t, ps.BindParameterValue("a", 1))
				require.NoError(t, ps.BindParameterValue("b", 2))

				var err error
				require.NotPanics(t, func() {
					err = r.run(context.Background(), nilExecutor, ps)
				})
				require.ErrorIs(t, err, errNilExecutor)
				assert.Equal(t, BoundParameterValues{nil, nil, nil}, ps.BoundParameterValues(), "pre-bound values reset")

				db := newExecutor()
				require.NoError(t, r.run(context.Background(), db, ps))
				require.Len(t, db.calls, 1)
				assert.Equal(t, []any{nil, nil, nil}, db.calls[0].args, "values from before the failed call are not sent")
			})
		}
	}
}

func TestExecutionHelpersPropagateExecutorErrors(t *testing.T) {
	driverErr := errors.New("driver failure")
	for _, r := range runners() {
		if r.method == methodQueryRow {
			continue
		}
		t.Run(r.name, func(t *testing.T) {
			db := &recordingExecutor{err: fmt.Errorf("conn: %w", driverErr)}
			err := r.run(context.Background(), db, mustPrepare(t, "SELECT @a"), BindParameterValue("a", 1))
			assert.ErrorIs(t, err, driverErr)
		})
	}
}

func TestQueryRowRejectsNilRowFromExecutor(t *testing.T) {
	db := &recordingExecutor{}
	row, err := QueryRowContext(context.Background(), db, mustPrepare(t, "SELECT 1"))
	require.ErrorIs(t, err, errNilRow)
	assert.Nil(t, row)
	assert.Len(t, db.calls, 1)
}

type ctxKey struct{}

func TestExecutionHelpersPassContext(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
			require.NoError(t, r.run(ctx, db, mustPrepare(t, "SELECT 1")))
			require.Len(t, db.calls, 1)
			got := db.calls[0].ctx
			require.NotNil(t, got)
			if r.name == r.method {
				assert.Equal(t, "marker", got.Value(ctxKey{}), "caller context reaches the executor")
			} else {
				assert.Nil(t, got.Value(ctxKey{}), "non-context variant uses a background context")
			}
		})
	}

	t.Run("nil context becomes background", func(t *testing.T) {
		db := newExecutor()
		var nilCtx context.Context
		_, err := ExecContext(nilCtx, db, mustPrepare(t, "SELECT 1"))
		require.NoError(t, err)
		require.Len(t, db.calls, 1)
		assert.NotNil(t, db.calls[0].ctx)
	})

	t.Run("canceled context is passed through", func(t *testing.T) {
		db := newExecutor()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := QueryContext(ctx, db, mustPrepare(t, "SELECT 1"))
		require.NoError(t, err, "the executor decides how to handle cancellation")
		require.Len(t, db.calls, 1)
		assert.ErrorIs(t, db.calls[0].ctx.Err(), context.Canceled)
	})
}

func TestExecutionHelpersDefaultsPersistAcrossCalls(t *testing.T) {
	db := newExecutor()
	ps := mustPrepare(t, "UPDATE t SET v = @v WHERE id = @id")
	require.NoError(t, ps.BindParameterValue("v", 5))

	_, err := Exec(db, ps, BindParameterValue("id", 1))
	require.NoError(t, err)
	_, err = Exec(db, ps, BindParameterValue("id", 2))
	require.NoError(t, err)
	_, err = Exec(db, ps, BindParameterValue("v", 7), BindParameterValue("id", 3))
	require.NoError(t, err)
	_, err = Exec(db, ps)
	require.NoError(t, err)

	require.Len(t, db.calls, 4)
	assert.Equal(t, []any{5, 1}, db.calls[0].args, "default used by the next call")
	assert.Equal(t, []any{5, 2}, db.calls[1].args, "default still used by later calls")
	assert.Equal(t, []any{7, 3}, db.calls[2].args, "override replaces the default for one call")
	assert.Equal(t, []any{5, nil}, db.calls[3].args, "overrides are not kept")
	assert.Equal(t, BoundParameterValues{5, nil}, ps.BoundParameterValues())
}

// foreignStatement is a PreparedStatement implemented outside the rewriter.
type foreignStatement struct {
	PreparedStatement
	revised string
}

func (f foreignStatement) Revised() string { return f.revised }

func TestExecutionHelpersCheckTheSQLThatIsSent(t *testing.T) {
	base := mustPrepare(t, "SELECT 1")
	for name, revised := range map[string]string{
		"multiple statements": twoStatements,
		"unterminated quote":  "SELECT 'x; SELECT 2",
	} {
		t.Run(name, func(t *testing.T) {
			db := newExecutor()
			_, err := ExecContext(context.Background(), db, foreignStatement{PreparedStatement: base, revised: revised})
			require.Error(t, err)
			assert.Empty(t, db.calls)
		})
	}
}
