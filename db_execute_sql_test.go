package dbsql_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/neumachen/dbsql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errFakeExecution = errors.New("fake driver: execution failed")

// execDriver is an in-memory driver that implements only the required
// interfaces, without QueryerContext or ExecerContext, so database/sql has to
// create and close a driver statement for every direct execution. It tracks
// which statements are open, which rows are open, and every event, so tests can
// assert that nothing outlives the result or rows and that no statement is
// closed while its rows are still being read.
type execDriver struct {
	mu                  sync.Mutex
	events              []string
	openStmts           int
	openRows            int
	stmtClosedUnderRows bool
	executed            []string
	args                [][]driver.Value
}

func (d *execDriver) log(event string) { d.events = append(d.events, event) }

func (d *execDriver) executedQueries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.executed...)
}

func (d *execDriver) argsAt(i int) []driver.Value {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.args[i]
}

func (d *execDriver) snapshot() (openStmts, openRows int, closedUnderRows bool, events []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.openStmts, d.openRows, d.stmtClosedUnderRows, append([]string(nil), d.events...)
}

func (d *execDriver) Connect(context.Context) (driver.Conn, error) { return &execConn{d: d}, nil }
func (d *execDriver) Driver() driver.Driver                        { return execDriverOpener{d: d} }

type execDriverOpener struct{ d *execDriver }

func (o execDriverOpener) Open(string) (driver.Conn, error) { return &execConn{d: o.d}, nil }

type execConn struct{ d *execDriver }

func (c *execConn) Prepare(query string) (driver.Stmt, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.openStmts++
	c.d.log("prepare")
	return &execStmt{d: c.d, query: query}, nil
}

func (*execConn) Close() error { return nil }

func (c *execConn) Begin() (driver.Tx, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	c.d.log("begin")
	return execTx{d: c.d}, nil
}

type execTx struct{ d *execDriver }

func (t execTx) Commit() error   { return t.end("commit") }
func (t execTx) Rollback() error { return t.end("rollback") }

func (t execTx) end(event string) error {
	t.d.mu.Lock()
	defer t.d.mu.Unlock()
	t.d.log(event)
	return nil
}

type execStmt struct {
	d     *execDriver
	query string
}

func (s *execStmt) Close() error {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.d.openRows > 0 {
		s.d.stmtClosedUnderRows = true
	}
	s.d.openStmts--
	s.d.log("close stmt")
	return nil
}

func (*execStmt) NumInput() int { return -1 }

func (s *execStmt) recordExecution(args []driver.Value) error {
	s.d.executed = append(s.d.executed, s.query)
	s.d.args = append(s.d.args, args)
	if strings.Contains(s.query, "fail") {
		return errFakeExecution
	}
	return nil
}

func (s *execStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.log("exec")
	if err := s.recordExecution(args); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (s *execStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.log("query")
	if err := s.recordExecution(args); err != nil {
		return nil, err
	}
	s.d.openRows++
	return &execRows{d: s.d, n: 3}, nil
}

type execRows struct {
	d      *execDriver
	i, n   int
	closed bool
}

func (*execRows) Columns() []string { return []string{"n"} }

func (r *execRows) Close() error {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.d.openRows--
		r.d.log("close rows")
	}
	return nil
}

func (r *execRows) Next(dest []driver.Value) error {
	if r.i == r.n {
		return io.EOF
	}
	r.i++
	dest[0] = int64(r.i)
	return nil
}

func openExecDB(t *testing.T) (*sql.DB, *execDriver) {
	t.Helper()
	d := &execDriver{}
	db := sql.OpenDB(d)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, d
}

func prepareStatement(t *testing.T, query string) dbsql.PreparedStatement {
	t.Helper()
	ps, err := dbsql.PrepareStatement(query)
	require.NoError(t, err)
	return ps
}

func readAll(t *testing.T, rows *sql.Rows) []int64 {
	t.Helper()
	var got []int64
	for rows.Next() {
		var v int64
		require.NoError(t, rows.Scan(&v))
		got = append(got, v)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	return got
}

func assertNothingOpen(t *testing.T, d *execDriver) {
	t.Helper()
	stmts, rows, closedUnderRows, events := d.snapshot()
	assert.Zero(t, stmts, "open driver statements; events: %v", events)
	assert.Zero(t, rows, "open driver rows; events: %v", events)
	assert.False(t, closedUnderRows, "a statement was closed while its rows were open; events: %v", events)
}

func TestExecutionThroughDatabaseSQL(t *testing.T) {
	ctx := context.Background()
	query := prepareStatement(t, "SELECT n FROM numbers WHERE n > @min AND tag = @tag")

	t.Run("DB query read to the end", func(t *testing.T) {
		db, d := openExecDB(t)
		for range 3 {
			rows, err := dbsql.QueryContext(ctx, db, query, dbsql.BindParameterValue("min", 0))
			require.NoError(t, err)
			assert.Equal(t, []int64{1, 2, 3}, readAll(t, rows))
		}
		assertNothingOpen(t, d)
		assert.Equal(t, []driver.Value{int64(0), nil}, d.argsAt(0), "missing name sent as NULL")
	})

	t.Run("DB query closed early", func(t *testing.T) {
		db, d := openExecDB(t)
		rows, err := dbsql.QueryContext(ctx, db, query)
		require.NoError(t, err)
		require.True(t, rows.Next())
		require.NoError(t, rows.Close())
		assertNothingOpen(t, d)
	})

	t.Run("DB exec and query row", func(t *testing.T) {
		db, d := openExecDB(t)
		_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, "UPDATE numbers SET n = @n"), dbsql.BindParameterValue("n", 1))
		require.NoError(t, err)
		row, err := dbsql.QueryRowContext(ctx, db, query)
		require.NoError(t, err)
		var v int64
		require.NoError(t, row.Scan(&v))
		assert.Equal(t, int64(1), v)
		assertNothingOpen(t, d)
	})

	for _, finish := range []string{"read to the end", "closed early"} {
		t.Run("Tx query "+finish+" then commit", func(t *testing.T) {
			db, d := openExecDB(t)
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)

			_, err = dbsql.ExecContext(ctx, tx, prepareStatement(t, "INSERT INTO numbers VALUES (@n)"), dbsql.BindParameterValue("n", 4))
			require.NoError(t, err)
			rows, err := dbsql.QueryContext(ctx, tx, query)
			require.NoError(t, err)
			if finish == "read to the end" {
				assert.Len(t, readAll(t, rows), 3)
			} else {
				require.True(t, rows.Next())
				require.NoError(t, rows.Close())
			}
			row, err := dbsql.QueryRowContext(ctx, tx, query)
			require.NoError(t, err)
			var v int64
			require.NoError(t, row.Scan(&v))

			require.NoError(t, tx.Commit())
			assertNothingOpen(t, d)
			_, _, _, events := d.snapshot()
			assert.Equal(t, "commit", events[len(events)-1], "commit happens after every statement and rows are closed")
		})
	}

	t.Run("Tx rollback", func(t *testing.T) {
		db, d := openExecDB(t)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		rows, err := dbsql.QueryContext(ctx, tx, query)
		require.NoError(t, err)
		readAll(t, rows)
		require.NoError(t, tx.Rollback())
		assertNothingOpen(t, d)
		_, _, _, events := d.snapshot()
		assert.Equal(t, "rollback", events[len(events)-1])
	})

	t.Run("QueryRow execution error surfaces at Scan", func(t *testing.T) {
		db, d := openExecDB(t)
		row, err := dbsql.QueryRowContext(ctx, db, prepareStatement(t, "SELECT fail WHERE x = @x"), dbsql.BindParameterValue("x", 1))
		require.NoError(t, err, "execution errors are not reported by QueryRowContext")
		var v int64
		require.ErrorIs(t, row.Scan(&v), errFakeExecution)
		assertNothingOpen(t, d)
	})

	t.Run("Exec and Query execution errors are returned", func(t *testing.T) {
		db, d := openExecDB(t)
		_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, "UPDATE fail"))
		require.ErrorIs(t, err, errFakeExecution)
		rows, err := dbsql.QueryContext(ctx, db, prepareStatement(t, "SELECT fail"))
		require.ErrorIs(t, err, errFakeExecution)
		assert.Nil(t, rows)
		assertNothingOpen(t, d)
	})

	t.Run("canceled context stops execution", func(t *testing.T) {
		db, d := openExecDB(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		rows, err := dbsql.QueryContext(canceled, db, query)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, rows)
		row, err := dbsql.QueryRowContext(canceled, db, query)
		require.NoError(t, err)
		var v int64
		require.ErrorIs(t, row.Scan(&v), context.Canceled)
		assert.Empty(t, d.executedQueries())
	})

	t.Run("multiple statements never reach the driver", func(t *testing.T) {
		db, d := openExecDB(t)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		for _, q := range []string{"DELETE FROM numbers; DELETE FROM other", "DELETE FROM numbers WHERE n = @n; DROP TABLE numbers"} {
			_, err := dbsql.ExecContext(ctx, tx, prepareStatement(t, q), dbsql.BindParameterValue("n", 1))
			require.ErrorIs(t, err, dbsql.ErrMultipleStatements)
		}
		require.NoError(t, tx.Commit())
		assert.Empty(t, d.executedQueries())
		_, _, _, events := d.snapshot()
		assert.Equal(t, []string{"begin", "commit"}, events)
	})
}
