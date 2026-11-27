package dbsql_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/neumachen/dbsql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The PostgreSQL execution tests run only against an explicitly configured,
// disposable server. Both variables are required, and the server's
// cluster_name must equal DBSQL_TEST_POSTGRES_CLUSTER, so that a DSN pointing
// at a shared database is refused instead of written to. The DSN is never
// logged.
const (
	envPostgresDSN     = "DBSQL_TEST_POSTGRES_DSN"
	envPostgresCluster = "DBSQL_TEST_POSTGRES_CLUSTER"
)

func openDisposablePostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cluster := os.Getenv(envPostgresDSN), os.Getenv(envPostgresCluster)
	if dsn == "" || cluster == "" {
		t.Skipf("set %s and %s to run PostgreSQL execution tests", envPostgresDSN, envPostgresCluster)
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)

	var got string
	require.NoError(t, db.QueryRowContext(testContext(t), "SELECT current_setting('cluster_name')").Scan(&got))
	require.Equal(t, cluster, got, "refusing to run against a server whose cluster_name does not match")
	return db
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func createProbeTable(t *testing.T, db *sql.DB) string {
	t.Helper()
	table := fmt.Sprintf("dbsql_exec_it_%d", time.Now().UnixNano())
	_, err := db.ExecContext(testContext(t), "CREATE TABLE "+table+" (n int NOT NULL, tag text NOT NULL)")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), "DROP TABLE "+table)
		assert.NoError(t, err)
	})
	return table
}

func countTag(t *testing.T, db *sql.DB, table, tag string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(testContext(t), "SELECT count(*) FROM "+table+" WHERE tag = $1", tag).Scan(&n))
	return n
}

func preparedStatementCount(t *testing.T, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) int {
	t.Helper()
	var n int
	require.NoError(t, q.QueryRowContext(testContext(t), "SELECT count(*) FROM pg_prepared_statements").Scan(&n))
	return n
}

func TestPostgresTransactionOwnership(t *testing.T) {
	db := openDisposablePostgres(t)
	table := createProbeTable(t, db)
	insert := prepareStatement(t, "INSERT INTO "+table+" (n, tag) SELECT g, @tag FROM generate_series(1, 5) AS g")
	selectRows := prepareStatement(t, "SELECT n FROM "+table+" WHERE tag = @tag ORDER BY n;")
	count := prepareStatement(t, "SELECT count(*) FROM "+table+" WHERE tag = @tag")

	for _, tc := range []struct {
		name    string
		finish  func(*testing.T, *sql.Rows)
		commit  bool
		wantRow int
	}{
		{name: "read all rows then commit", finish: func(t *testing.T, rows *sql.Rows) {
			var got []int
			for rows.Next() {
				var n int
				require.NoError(t, rows.Scan(&n))
				got = append(got, n)
			}
			require.NoError(t, rows.Err())
			assert.Equal(t, []int{1, 2, 3, 4, 5}, got)
			require.NoError(t, rows.Close())
		}, commit: true, wantRow: 5},
		{name: "close rows early then commit", finish: func(t *testing.T, rows *sql.Rows) {
			require.True(t, rows.Next())
			require.NoError(t, rows.Close())
		}, commit: true, wantRow: 5},
		{name: "read all rows then roll back", finish: func(t *testing.T, rows *sql.Rows) {
			read := 0
			for rows.Next() {
				read++
			}
			require.NoError(t, rows.Err())
			assert.Equal(t, 5, read)
			require.NoError(t, rows.Close())
		}, commit: false, wantRow: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testContext(t)
			tag := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)

			result, err := dbsql.ExecContext(ctx, tx, insert, dbsql.BindParameterValue("tag", tag))
			require.NoError(t, err)
			affected, err := result.RowsAffected()
			require.NoError(t, err)
			assert.Equal(t, int64(5), affected)

			rows, err := dbsql.QueryContext(ctx, tx, selectRows, dbsql.BindParameterValue("tag", tag))
			require.NoError(t, err)
			tc.finish(t, rows)

			row, err := dbsql.QueryRowContext(ctx, tx, count, dbsql.BindParameterValue("tag", tag))
			require.NoError(t, err)
			var inTx int
			require.NoError(t, row.Scan(&inTx))
			assert.Equal(t, 5, inTx)
			assert.Zero(t, preparedStatementCount(t, tx), "no server-side prepared statements inside the transaction")

			if tc.commit {
				require.NoError(t, tx.Commit())
			} else {
				require.NoError(t, tx.Rollback())
			}
			assert.Equal(t, tc.wantRow, countTag(t, db, table, tag))
		})
	}
}

func TestPostgresNoStatementLeak(t *testing.T) {
	db := openDisposablePostgres(t)
	ctx := testContext(t)
	withArgs := prepareStatement(t, "SELECT n FROM generate_series(1, 3) AS n WHERE n >= @min")
	noArgs := prepareStatement(t, "SELECT ';' AS a, $$;$$ AS b; -- trailing comment")
	before := preparedStatementCount(t, db)

	for range 5 {
		_, err := dbsql.ExecContext(ctx, db, withArgs, dbsql.BindParameterValue("min", 1))
		require.NoError(t, err)
		_, err = dbsql.ExecContext(ctx, db, noArgs)
		require.NoError(t, err)

		rows, err := dbsql.QueryContext(ctx, db, withArgs, dbsql.BindParameterValue("min", 2))
		require.NoError(t, err)
		var got []int
		for rows.Next() {
			var n int
			require.NoError(t, rows.Scan(&n))
			got = append(got, n)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		assert.Equal(t, []int{2, 3}, got)

		row, err := dbsql.QueryRowContext(ctx, db, noArgs)
		require.NoError(t, err)
		var a, b string
		require.NoError(t, row.Scan(&a, &b))
		assert.Equal(t, ";", a)
		assert.Equal(t, ";", b)
	}

	assert.Equal(t, before, preparedStatementCount(t, db), "the single pooled session keeps no extra prepared statements")
}

func TestPostgresSingleStatementPolicy(t *testing.T) {
	db := openDisposablePostgres(t)
	table := createProbeTable(t, db)
	ctx := testContext(t)

	for name, query := range map[string]string{
		"without parameters": "INSERT INTO " + table + " VALUES (1, 'multi'); INSERT INTO " + table + " VALUES (2, 'multi')",
		"with parameters":    "INSERT INTO " + table + " VALUES (@n, 'multi'); INSERT INTO " + table + " VALUES (2, 'multi')",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, query), dbsql.BindParameterValue("n", 1))
			require.ErrorIs(t, err, dbsql.ErrMultipleStatements)
			assert.Zero(t, countTag(t, db, table, "multi"), "nothing was executed")
		})
	}

	t.Run("single statements with and without parameters", func(t *testing.T) {
		_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, "INSERT INTO "+table+" VALUES (1, 'one;'); "))
		require.NoError(t, err)
		_, err = dbsql.ExecContext(ctx, db, prepareStatement(t, "INSERT INTO "+table+" VALUES (@n, 'one;') /* ; */;"),
			dbsql.BindParameterValue("n", 2))
		require.NoError(t, err)
		assert.Equal(t, 2, countTag(t, db, table, "one;"))
	})
}

func TestPostgresErrors(t *testing.T) {
	db := openDisposablePostgres(t)
	ctx := testContext(t)
	divide := prepareStatement(t, "SELECT 1 / (@x::int * 0)")

	row, err := dbsql.QueryRowContext(ctx, db, divide, dbsql.BindParameterValue("x", 1))
	require.NoError(t, err, "database errors are reported by Scan")
	var v int
	scanErr := row.Scan(&v)
	var pqErr *pq.Error
	require.ErrorAs(t, scanErr, &pqErr)
	assert.Equal(t, pq.ErrorCode("22012"), pqErr.Code)

	_, err = dbsql.ExecContext(ctx, db, divide, dbsql.BindParameterValue("x", 1))
	require.ErrorAs(t, err, &pqErr)
	assert.Equal(t, pq.ErrorCode("22012"), pqErr.Code)

	var n int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&n), "connection still usable")
}

// Values of the standard_conforming_strings setting.
const (
	stringsOn  = "on"
	stringsOff = "off"
)

func requireStringSetting(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var got string
	require.NoError(t, db.QueryRowContext(testContext(t), "SHOW standard_conforming_strings").Scan(&got))
	require.Equal(t, want, got, "session standard_conforming_strings")
}

func countRows(t *testing.T, db *sql.DB, table, where string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(testContext(t), "SELECT count(*) FROM "+table+" WHERE "+where).Scan(&n))
	return n
}

// TestPostgresSingleStatementUnderStringSettings checks the one-statement rule
// against each standard_conforming_strings setting of the session. The server
// baseline shows that PostgreSQL itself runs a second statement hidden in the
// SQL when the setting is off; the helpers must reject that SQL before sending
// it, whatever the setting.
func TestPostgresSingleStatementUnderStringSettings(t *testing.T) {
	db := openDisposablePostgres(t)
	table := createProbeTable(t, db)
	insert := "INSERT INTO " + table + " (n, tag) VALUES "
	// One statement when the setting is on, two when it is off.
	hidden := insert + `(1, 'a\' || '); ` + insert + `(2, $$hidden$$); --')`
	hiddenWithParameter := insert + `(@n, 'a\' || '); ` + insert + `(2, $$hidden$$); --')`
	// One statement under either setting: the second INSERT is text inside
	// the escape string.
	escapeHiding := insert + `(3, E'x\' ); ` + insert + `(4, $$hidden$$); --')`
	escapeQuote := insert + `(5, E'it\'s; fine')`
	// Valid only when the setting is on; when it is off the server cannot
	// lex it and runs nothing.
	trailingBackslash := insert + `(6, 'C:\temp\')`

	for _, setting := range []string{"default", stringsOn, stringsOff} {
		t.Run(setting, func(t *testing.T) {
			ctx := testContext(t)
			want := setting
			if setting == "default" {
				want = stringsOn
			} else {
				_, err := db.ExecContext(ctx, "SET standard_conforming_strings = "+setting)
				require.NoError(t, err)
				t.Cleanup(func() {
					_, err := db.ExecContext(context.Background(), "RESET standard_conforming_strings")
					assert.NoError(t, err)
				})
			}
			requireStringSetting(t, db, want)
			truncate := func(t *testing.T) {
				t.Helper()
				_, err := db.ExecContext(ctx, "TRUNCATE "+table)
				require.NoError(t, err)
			}

			t.Run("helpers reject the hidden statement before sending", func(t *testing.T) {
				truncate(t)
				_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, "/* scs-helper */ "+hidden))
				require.ErrorIs(t, err, dbsql.ErrMultipleStatements)
				_, err = dbsql.ExecContext(ctx, db, prepareStatement(t, "/* scs-helper */ "+hiddenWithParameter),
					dbsql.BindParameterValue("n", 1))
				require.ErrorIs(t, err, dbsql.ErrMultipleStatements)
				rows, err := dbsql.QueryContext(ctx, db, prepareStatement(t, "/* scs-helper */ "+hidden))
				require.ErrorIs(t, err, dbsql.ErrMultipleStatements)
				assert.Nil(t, rows)
				assert.Zero(t, countRows(t, db, table, "true"), "nothing reached the server")
				requireStringSetting(t, db, want)
			})

			t.Run("server baseline", func(t *testing.T) {
				truncate(t)
				_, err := db.ExecContext(ctx, "/* scs-direct */ "+hidden)
				require.NoError(t, err)
				wantHidden := 0
				if want == stringsOff {
					wantHidden = 1
				}
				assert.Equal(t, wantHidden, countRows(t, db, table, "tag = 'hidden'"),
					"PostgreSQL runs the hidden statement only when standard_conforming_strings is off")
				assert.Equal(t, 1, countRows(t, db, table, "n = 1"))
			})

			t.Run("escape strings are accepted and hide nothing", func(t *testing.T) {
				truncate(t)
				_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, "/* scs-accepted */ "+escapeHiding))
				require.NoError(t, err)
				_, err = dbsql.ExecContext(ctx, db, prepareStatement(t, "/* scs-accepted */ "+escapeQuote))
				require.NoError(t, err)
				assert.Equal(t, 1, countRows(t, db, table, "n = 3"))
				assert.Zero(t, countRows(t, db, table, "n = 4"), "text inside the escape string was not executed")
				assert.Equal(t, 1, countRows(t, db, table, "n = 5 AND tag = 'it''s; fine'"))
			})

			t.Run("plain literal with trailing backslash is not rejected by the helpers", func(t *testing.T) {
				truncate(t)
				_, err := dbsql.ExecContext(ctx, db, prepareStatement(t, "/* scs-accepted */ "+trailingBackslash))
				if want == stringsOn {
					require.NoError(t, err)
					assert.Equal(t, 1, countRows(t, db, table, `n = 6 AND tag = 'C:\temp\'`))
					return
				}
				require.NotErrorIs(t, err, dbsql.ErrMultipleStatements)
				var pqErr *pq.Error
				require.ErrorAs(t, err, &pqErr, "the server, not the helper, rejects it")
				assert.Equal(t, pq.ErrorCode("42601"), pqErr.Code)
				assert.Zero(t, countRows(t, db, table, "true"))
			})
		})
	}
}
