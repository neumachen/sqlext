package sqlext

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedExecutor reports every call on entered and then waits until release is
// closed, so tests can hold several calls inside the executor at once. Only
// the context methods are implemented; the helpers must not use the others,
// and calling them panics on the nil embedded interface.
type gatedExecutor struct {
	DBPreparerExecutor
	entered     chan executorCall
	release     chan struct{}
	releaseOnce sync.Once
}

// newGatedExecutor releases held calls when the test ends, so a failed
// assertion or timeout does not leave goroutines blocked in the executor.
func newGatedExecutor(t *testing.T, capacity int) *gatedExecutor {
	t.Helper()
	g := &gatedExecutor{
		entered: make(chan executorCall, capacity),
		release: make(chan struct{}),
	}
	t.Cleanup(g.releaseAll)
	return g
}

func (g *gatedExecutor) releaseAll() {
	g.releaseOnce.Do(func() { close(g.release) })
}

func (g *gatedExecutor) hold(method string, ctx context.Context, query string, args []any) {
	g.entered <- executorCall{method: method, ctx: ctx, query: query, args: args}
	<-g.release
}

func (g *gatedExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	g.hold(methodExec, ctx, query, args)
	return driverResult{}, nil
}

func (g *gatedExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	g.hold(methodQuery, ctx, query, args)
	return nil, nil
}

func (g *gatedExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	g.hold(methodQueryRow, ctx, query, args)
	return &sql.Row{}
}

// awaitCall fails the test instead of hanging when a call never reaches the
// executor, for example because it is blocked on the statement's lock.
func (g *gatedExecutor) awaitCall(t *testing.T) executorCall {
	t.Helper()
	select {
	case call := <-g.entered:
		return call
	case <-time.After(10 * time.Second):
		require.FailNow(t, "call did not reach the executor")
		return executorCall{}
	}
}

func TestExecutionHelpersOverlappingCallsUseTheirOwnValues(t *testing.T) {
	const calls = 6
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newGatedExecutor(t, calls)
			ps := mustPrepare(t, "SELECT @d, @o, @d, @o")

			var wg sync.WaitGroup
			errs := make([]error, calls)
			got := make([]executorCall, calls)
			for i := range calls {
				// The defaults change while the earlier calls are still held
				// inside the executor.
				require.NoError(t, ps.BindParameterValue("d", fmt.Sprintf("d%d", i)))
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[i] = r.run(context.Background(), db, ps, BindParameterValue("o", i))
				}()
				got[i] = db.awaitCall(t)
			}

			// Every call is inside the executor now. Changing the defaults must
			// not reach the arguments they hold.
			ps.ResetParametersValues()
			require.NoError(t, ps.BindParameterValue("o", "direct"))
			db.releaseAll()
			wg.Wait()

			for i := range got {
				require.NoError(t, errs[i])
				assert.Equal(t, r.method, got[i].method)
				d := fmt.Sprintf("d%d", i)
				assert.Equal(t, []any{d, i, d, i}, got[i].args, "call %d", i)
			}
			assert.Equal(t, BoundParameterValues{nil, "direct", nil, "direct"}, ps.BoundParameterValues(),
				"overrides never become defaults")
		})
	}
}

// Calls on every path, direct binds, reads and resets all run at the same
// time. Run with -race to check the synchronization; the assertions hold for
// every interleaving.
func TestExecutionHelpersConcurrentCallsAndDirectUse(t *testing.T) {
	const callsPerRunner, writers, rounds = 10, 4, 50
	all := runners()
	db := newGatedExecutor(t, len(all)*callsPerRunner)
	db.releaseAll()
	ps := mustPrepare(t, "SELECT @d, @o")
	require.NoError(t, ps.BindParameterValue("d", "default"))

	start := make(chan struct{})
	errs := make(chan error, len(all)*callsPerRunner+writers)
	var wg sync.WaitGroup
	for _, r := range all {
		for i := range callsPerRunner {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- r.run(context.Background(), db, ps, BindParameterValue("o", fmt.Sprintf("%s/%d", r.name, i)))
			}()
		}
	}
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range rounds {
				ps.ResetParametersValues()
				if err := ps.BindParameterValues(BindParameterValue("d", "default")); err != nil {
					errs <- err
					return
				}
				if v := ps.BoundParameterValues(); v[1] != nil {
					errs <- fmt.Errorf("override %v became a default", v[1])
					return
				}
			}
			errs <- nil
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(db.entered)

	for err := range errs {
		require.NoError(t, err)
	}
	seen := map[any]int{}
	for call := range db.entered {
		require.Len(t, call.args, 2)
		assert.Contains(t, []any{"default", nil}, call.args[0], "a default that existed at some point")
		seen[call.args[1]]++
	}
	assert.Len(t, seen, len(all)*callsPerRunner, "every call sent its own override")
	for override, n := range seen {
		assert.Equal(t, 1, n, "override %v sent once", override)
	}
}

func TestExecutionHelpersRunBindersOutsideTheStatementLock(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "SELECT @d, @o")
			require.NoError(t, ps.BindParameterValue("d", 1))

			var inBinder BoundParameterValues
			done := make(chan error, 1)
			go func() {
				done <- r.run(context.Background(), db, ps, func(p PreparedStatement) error {
					// Each of these would deadlock if the binder ran with the
					// statement's lock held.
					if err := ps.BindParameterValue("d", 2); err != nil {
						return err
					}
					inBinder = ps.BoundParameterValues()
					ps.ResetParametersValues()
					if err := ps.BindParameterValue("d", 3); err != nil {
						return err
					}
					return p.BindParameterValue("o", "x")
				})
			}()

			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				require.FailNow(t, "binder blocked on the statement's lock")
			}
			assert.Equal(t, BoundParameterValues{2, nil}, inBinder)
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{1, "x"}, db.calls[0].args, "the call uses the defaults copied before its binders ran")
			assert.Equal(t, BoundParameterValues{3, nil}, ps.BoundParameterValues(), "direct binds made by a binder are defaults")
		})
	}
}

func TestExecutionHelpersOverridesAreCallPrivate(t *testing.T) {
	boom := errors.New("binder failed")
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "SELECT @a, @b, @a")
			require.NoError(t, ps.BindParameterValue("a", 1))
			require.NoError(t, ps.BindParameterValue("b", 2))

			var seen PreparedStatement
			var seenValues BoundParameterValues
			require.NoError(t, r.run(context.Background(), db, ps,
				BindParameterValue("a", nil),
				func(p PreparedStatement) error {
					seen = p
					seenValues = p.BoundParameterValues()
					return nil
				},
			))
			require.NoError(t, r.run(context.Background(), db, ps, func(p PreparedStatement) error {
				p.ResetParametersValues()
				return p.BindParameterValue("b", 4)
			}))
			err := r.run(context.Background(), db, ps,
				BindParameterValue("b", 9),
				func(PreparedStatement) error { return boom },
			)
			require.ErrorIs(t, err, boom)
			require.NoError(t, seen.BindParameterValue("a", 8), "a kept binder argument stays private")
			require.NoError(t, r.run(context.Background(), db, ps))

			require.Len(t, db.calls, 3, "the failed call sends nothing")
			assert.Equal(t, []any{nil, 2, nil}, db.calls[0].args, "explicit nil overrides the default at every position")
			assert.Equal(t, []any{nil, 4, nil}, db.calls[1].args, "a reset in a binder clears the defaults for that call only")
			assert.Equal(t, []any{1, 2, 1}, db.calls[2].args, "no override from earlier or failed calls is kept")
			assert.Equal(t, BoundParameterValues{1, 2, 1}, ps.BoundParameterValues())

			assert.NotSame(t, ps, seen, "binders receive private execution state")
			assert.Equal(t, BoundParameterValues{nil, 2, nil}, seenValues, "binders see the defaults plus earlier overrides")
			assert.Equal(t, ps.Revised(), seen.Revised())
			assert.Equal(t, ps.UnpreparedStatement(), seen.UnpreparedStatement())
			assert.Equal(t, ps.ParameterPositions(), seen.ParameterPositions())
		})
	}
}

func TestExecutionHelpersKeepDefaultsAfterExecutorFailure(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			ps := mustPrepare(t, "SELECT @a, @b")
			require.NoError(t, ps.BindParameterValue("a", 1))

			// The QueryRow helpers fail here because this executor returns a
			// nil row; the others get the executor's error.
			failing := &recordingExecutor{err: errors.New("driver failure")}
			require.Error(t, r.run(context.Background(), failing, ps, BindParameterValue("b", 2)))
			require.Len(t, failing.calls, 1)
			assert.Equal(t, []any{1, 2}, failing.calls[0].args)
			assert.Equal(t, BoundParameterValues{1, nil}, ps.BoundParameterValues())

			db := newExecutor()
			require.NoError(t, r.run(context.Background(), db, ps))
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{1, nil}, db.calls[0].args)
		})
	}
}

func TestExecutionHelpersExplicitResetClearsDefaults(t *testing.T) {
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			ps := mustPrepare(t, "SELECT @a, @b")
			require.NoError(t, ps.BindParameterValue("a", 1))
			require.NoError(t, ps.BindParameterValue("b", 2))

			require.NoError(t, r.run(context.Background(), db, ps))
			ps.ResetParametersValues()
			require.NoError(t, r.run(context.Background(), db, ps))
			require.NoError(t, r.run(context.Background(), db, ps, BindParameterValue("b", 3)))
			require.NoError(t, ps.BindParameterValues(BindParameterValue("a", 5)))
			require.NoError(t, r.run(context.Background(), db, ps))

			require.Len(t, db.calls, 4)
			assert.Equal(t, []any{1, 2}, db.calls[0].args)
			assert.Equal(t, []any{nil, nil}, db.calls[1].args, "reset defaults are sent as NULL")
			assert.Equal(t, []any{nil, 3}, db.calls[2].args)
			assert.Equal(t, []any{5, nil}, db.calls[3].args, "BindParameterValues on the statement sets defaults")
		})
	}
}

func TestBuiltInStatementDoesNotShareArgumentStorage(t *testing.T) {
	ps := mustPrepare(t, "SELECT @a, @b")
	payload := []byte("x")
	require.NoError(t, ps.BindParameterValue("a", 1))
	require.NoError(t, ps.BindParameterValue("b", payload))

	read := ps.BoundParameterValues()
	read[0] = "changed"
	assert.Equal(t, 1, ps.BoundParameterValues()[0], "BoundParameterValues returns a copy")

	db := newExecutor()
	_, err := ExecContext(context.Background(), db, ps)
	require.NoError(t, err)
	require.Len(t, db.calls, 1)
	sent := db.calls[0].args

	require.NoError(t, ps.BindParameterValue("a", 2))
	assert.Equal(t, 1, sent[0], "later binds do not change arguments already sent")
	sent[0] = "mutated"
	assert.Equal(t, 2, ps.BoundParameterValues()[0], "executor writes do not change the defaults")

	payload[0] = 'y'
	assert.Equal(t, []byte("y"), sent[1], "copies are shallow: the bound []byte itself is shared")
}

const (
	eventBind  = "bind"
	eventRead  = "read"
	eventReset = "reset"
)

// eventStatement is a PreparedStatement implemented outside the rewriter that
// records the order in which the helpers use it.
type eventStatement struct {
	PreparedStatement
	events []string
}

func (e *eventStatement) BindParameterValues(binderFuncs ...BindParameterValueFunc) error {
	e.events = append(e.events, eventBind)
	return e.PreparedStatement.BindParameterValues(binderFuncs...)
}

func (e *eventStatement) BoundParameterValues() BoundParameterValues {
	e.events = append(e.events, eventRead)
	return e.PreparedStatement.BoundParameterValues()
}

func (e *eventStatement) ResetParametersValues() {
	e.events = append(e.events, eventReset)
	e.PreparedStatement.ResetParametersValues()
}

func TestExecutionHelpersKeepForeignStatementBehavior(t *testing.T) {
	boom := errors.New("binder failed")
	for _, r := range runners() {
		t.Run(r.name, func(t *testing.T) {
			db := newExecutor()
			base := mustPrepare(t, "SELECT @a, @b")
			ps := &eventStatement{PreparedStatement: base}
			require.NoError(t, base.BindParameterValue("a", 1))

			require.NoError(t, r.run(context.Background(), db, ps, BindParameterValue("b", 2)))
			assert.Equal(t, []string{eventBind, eventRead, eventReset}, ps.events, "bound in place, read, then reset")
			require.Len(t, db.calls, 1)
			assert.Equal(t, []any{1, 2}, db.calls[0].args)
			assert.Equal(t, BoundParameterValues{nil, nil}, base.BoundParameterValues(), "reset after the call")

			ps.events = nil
			require.NoError(t, r.run(context.Background(), db, ps))
			assert.Equal(t, []string{eventRead, eventReset}, ps.events, "no bind without binders")

			ps.events = nil
			require.NoError(t, base.BindParameterValue("a", 1))
			require.ErrorIs(t, r.run(context.Background(), db, ps, func(PreparedStatement) error { return boom }), boom)
			assert.Equal(t, []string{eventBind, eventReset}, ps.events, "reset after a failed call")
			assert.Equal(t, BoundParameterValues{nil, nil}, base.BoundParameterValues())
		})
	}
}

func TestParameterPositionsReturnsACopy(t *testing.T) {
	db := newExecutor()
	ps := mustPrepare(t, "SELECT @a, @b, @a")
	want := ps.ParameterPositions()
	require.NotNil(t, want)

	*ps.ParameterPositions() = ParameterPositions{}
	require.NoError(t, ps.BindParameterValue("a", 1))
	assert.Equal(t, want, ps.ParameterPositions(), "overwriting the returned struct keeps the statement's positions")

	_, err := ExecContext(context.Background(), db, ps, func(p PreparedStatement) error {
		*p.ParameterPositions() = ParameterPositions{}
		return p.BindParameterValue("b", 2)
	})
	require.NoError(t, err)
	require.Len(t, db.calls, 1)
	assert.Equal(t, []any{1, 2, 1}, db.calls[0].args, "binding still uses every position")
	assert.Equal(t, BoundParameterValues{1, nil, 1}, ps.BoundParameterValues())
}
