//go:build integration

package sqlext

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	// Registers the "postgres" driver for the integration tests only.
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Integration tests run only when built with -tags integration and pointed at
// a PostgreSQL instance created for the test run. Both variables are
// required; a missing or partial configuration fails the tests instead of
// skipping them or falling back to a local default.
const (
	// EnvPostgresDSN is the connection string of the disposable instance.
	// It is never logged.
	EnvPostgresDSN = "SQLEXT_TEST_POSTGRES_DSN"
	// EnvPostgresCluster is the cluster_name the instance was started with.
	EnvPostgresCluster = "SQLEXT_TEST_POSTGRES_CLUSTER"
	// disposableClusterPrefix marks instances created for test runs, as
	// .dev/test-postgres.sh does.
	disposableClusterPrefix = "sqlext-test-"
)

// disposableTables are the public tables the integration tests may find: the
// legacy fixtures from .dev/migrations, the bookkeeping tables of PostGIS and
// golang-migrate, and probe tables the tests create themselves.
var disposableTables = map[string]bool{
	"testing_datatypes": true, "customers": true, "email_addresses": true, "addresses": true,
	"customer_addresses": true, "spatial_ref_sys": true, "schema_migrations": true,
}

const probeTablePrefix = "sqlext_exec_it_"

// OpenDisposablePostgres opens the instance named by the integration
// configuration. It fails the test unless both variables are set, the
// configured cluster name carries the disposable prefix, the server reports
// that cluster_name, and the public schema holds no tables other than the test
// fixtures. These checks guard against pointing the tests at the wrong server;
// they cannot prove that an instance is disposable, so only point the tests at
// instances created for the run. The pool is closed when the test ends.
//
// It is exported so the external test package uses the same checks.
func OpenDisposablePostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn, cluster := os.Getenv(EnvPostgresDSN), os.Getenv(EnvPostgresCluster)
	if dsn == "" || cluster == "" {
		t.Fatalf("integration tests need both %s and %s (DSN set: %t, cluster set: %t); see README.md, \"Integration tests\"",
			EnvPostgresDSN, EnvPostgresCluster, dsn != "", cluster != "")
	}
	if !strings.HasPrefix(cluster, disposableClusterPrefix) {
		t.Fatalf("%s=%q does not start with %q; refusing to treat that server as disposable",
			EnvPostgresCluster, cluster, disposableClusterPrefix)
	}

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err, "open %s", EnvPostgresDSN)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var got string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT current_setting('cluster_name')").Scan(&got),
		"read cluster_name from the configured server")
	require.Equal(t, cluster, got, "refusing to run against a server whose cluster_name does not match")

	rows, err := db.QueryContext(ctx, `
		SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')`)
	require.NoError(t, err)
	var unexpected []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		if !disposableTables[name] && !strings.HasPrefix(name, probeTablePrefix) {
			unexpected = append(unexpected, name)
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Empty(t, unexpected, "the public schema holds tables the tests do not own; refusing to treat the database as disposable")
	return db
}
