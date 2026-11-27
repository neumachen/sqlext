package sqlext_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type scanFixture struct {
	columns      []string
	values       [][]driver.Value
	iterationErr error
	nextCalls    int
	closeCalls   int
}

type (
	scanConnector  struct{ fixture *scanFixture }
	scanDriver     struct{ fixture *scanFixture }
	scanConnection struct{ fixture *scanFixture }
	scanRows       struct {
		fixture *scanFixture
		index   int
		buffers map[int][]byte
	}
)

var errFixtureUnsupported = errors.New("fixture supports QueryContext only")

// Column names and values shared by the typed scanning tests.
const (
	idColumn    = "id"
	nameColumn  = "name"
	otherColumn = "other"
	adaName     = "Ada"
)

func (c scanConnector) Connect(context.Context) (driver.Conn, error) { return scanConnection(c), nil }
func (c scanConnector) Driver() driver.Driver                        { return scanDriver(c) }
func (d scanDriver) Open(string) (driver.Conn, error)                { return scanConnection(d), nil }

func (scanConnection) Prepare(string) (driver.Stmt, error) { return nil, errFixtureUnsupported }

func (scanConnection) Begin() (driver.Tx, error) { return nil, errFixtureUnsupported }
func (scanConnection) Close() error              { return nil }

func (c scanConnection) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &scanRows{fixture: c.fixture, buffers: make(map[int][]byte)}, nil
}

func (r *scanRows) Columns() []string { return r.fixture.columns }
func (r *scanRows) Close() error      { r.fixture.closeCalls++; return nil }

func (r *scanRows) Next(dest []driver.Value) error {
	r.fixture.nextCalls++
	if r.index == len(r.fixture.values) {
		if r.fixture.iterationErr != nil {
			return r.fixture.iterationErr
		}
		return io.EOF
	}
	copy(dest, r.fixture.values[r.index])
	for i, value := range dest {
		if raw, ok := value.([]byte); ok && raw != nil {
			r.buffers[i] = append(r.buffers[i][:0], raw...)
			if r.buffers[i] == nil {
				r.buffers[i] = []byte{}
			}
			dest[i] = r.buffers[i]
		}
	}
	r.index++
	return nil
}

func openScanRows(t *testing.T, fixture *scanFixture) *sql.Rows {
	t.Helper()
	db := sql.OpenDB(scanConnector{fixture: fixture})
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	rows, err := db.QueryContext(context.Background(), "fixture")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rows.Close()) })
	return rows
}
