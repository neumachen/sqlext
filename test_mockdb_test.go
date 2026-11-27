package sqlext

import (
	"context"
	"database/sql"
	"errors"
)

// mockDB mocks out sqlx.m for the purpose of testing.
type mockDB struct {
	PingOk     bool
	ExecOk     bool
	PrepareOk  bool
	QueryOk    bool
	QueryRowOk bool
}

// func (m *mockDB) ExecContext(ctx context.Context)

// Prepare returns an empty statement when PrepareOk is set and a mock error
// otherwise.
func (m *mockDB) Prepare(_ string) (*sql.Stmt, error) {
	if m.PrepareOk {
		return &sql.Stmt{}, nil
	}
	return nil, errors.New("mock error")
}

func (m *mockDB) PrepareContext(_ context.Context, _ string) (*sql.Stmt, error) {
	if m.PrepareOk {
		return &sql.Stmt{}, nil
	}
	return nil, errors.New("mock error")
}

// Exec returns a mock result when ExecOk is set and a mock error otherwise.
func (m *mockDB) Exec(_ string, _ ...any) (sql.Result, error) {
	if m.ExecOk {
		return &Result{}, nil
	}
	return nil, errors.New("mock error")
}

func (m *mockDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	if m.ExecOk {
		return &Result{}, nil
	}
	return nil, errors.New("mock error")
}

// Query returns no rows and no error when QueryOk is set, and a mock error
// otherwise.
func (m *mockDB) Query(_ string, _ ...any) (*sql.Rows, error) {
	if m.QueryOk {
		return nil, nil
	}
	return nil, errors.New("mock error")
}

func (m *mockDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	if m.QueryOk {
		return nil, nil
	}
	return nil, errors.New("mock error")
}

// QueryRow returns an empty row when QueryRowOk is set and nil otherwise.
func (m *mockDB) QueryRow(_ string, _ ...any) *sql.Row {
	if m.QueryRowOk {
		return &sql.Row{}
	}
	return nil
}

func (m *mockDB) QueryRowContext(_ context.Context, _ string, _ ...any) *sql.Row {
	if m.QueryRowOk {
		return &sql.Row{}
	}
	return nil
}

// Ping reports a mock error unless PingOk is set.
func (m *mockDB) Ping() error {
	if m.PingOk {
		return nil
	}
	return errors.New("mock Ping error")
}

// Close always succeeds.
func (*mockDB) Close() error {
	return nil
}

// Result is a mock of sql.Result.
type Result struct {
	LastInsertIDOk bool
}

// LastInsertId always reports 1.
func (*Result) LastInsertId() (int64, error) {
	return 1, nil
}

// RowsAffected always reports 1.
func (*Result) RowsAffected() (int64, error) {
	return 1, nil
}
