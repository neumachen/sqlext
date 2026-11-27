package sqlext

import (
	"database/sql"
	"fmt"

	"github.com/neumachen/sqlext/internal"
)

// RowScanner maps one result set to typed callbacks. Call rows.Next before Scan.
// The caller owns iteration, rows.Err, and rows.Close. Construct another scanner
// after NextResultSet. A scanner and its destinations must not be used concurrently.
type RowScanner struct {
	rows         *sql.Rows
	destinations []any
}

// NewRowScanner matches every result column to exactly one binding by name.
// Duplicate, missing, and unbound columns are errors.
// Bindings must satisfy ColumnScanner.
func NewRowScanner(rows *sql.Rows, bindings ColumnBinders) (*RowScanner, error) {
	if rows == nil {
		return nil, fmt.Errorf("nil rows: %w", ErrColumnMapping)
	}
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read result columns: %w", err)
	}
	byName := make(map[Column]ColumnScanner, len(bindings))
	for _, binding := range bindings {
		if internal.IsNil(binding) {
			return nil, fmt.Errorf("nil binding: %w", ErrColumnMapping)
		}
		scanner, ok := binding.(ColumnScanner)
		if !ok {
			return nil, fmt.Errorf("column %q lacks sql.Scanner: %w", binding.Column(), ErrColumnMapping)
		}
		if validator, ok := scanner.(interface{ validate() error }); ok {
			if err := validator.validate(); err != nil {
				return nil, err
			}
		}
		name := binding.Column()
		if name == "" {
			return nil, fmt.Errorf("empty column name: %w", ErrColumnMapping)
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("duplicate binding %q: %w", name, ErrColumnMapping)
		}
		byName[name] = scanner
	}
	if len(columns) != len(byName) {
		return nil, fmt.Errorf("%d result columns, %d bindings: %w", len(columns), len(byName), ErrColumnMapping)
	}
	destinations := make([]any, len(columns))
	for i, name := range columns {
		scanner, exists := byName[Column(name)]
		if !exists {
			return nil, fmt.Errorf("unbound or duplicate result column %q: %w", name, ErrColumnMapping)
		}
		destinations[i] = scanner
		delete(byName, Column(name))
	}
	return &RowScanner{rows: rows, destinations: destinations}, nil
}

// Scan assigns the current row in result-column order. It does not advance or
// close rows. On error, earlier callbacks may already have changed the record.
func (s *RowScanner) Scan() error {
	if s == nil || s.rows == nil {
		return fmt.Errorf("uninitialized row scanner: %w", ErrColumnMapping)
	}
	if err := s.rows.Scan(s.destinations...); err != nil {
		return fmt.Errorf("scan row: %w", err)
	}
	return nil
}
