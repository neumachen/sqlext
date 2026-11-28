package sqlext

import (
	"sync"

	"github.com/neumachen/sqlext/internal"
)

// ParameterPositions is a struct that represents the positions of parameters in an SQL statement.
// The parameterPositions field is a map that stores the positions of parameters, where the key is the parameter name
// and the value is a slice of integers representing the positions.
// The totalPositions field is an integer representing the total number of parameter positions.
type ParameterPositions struct {
	parameterPositions map[string][]int
	totalPositions     int
}

// getPositions is a method of the NamedParameterPositions struct.
// It takes a parameter name as input and returns a slice of integers representing the positions of the parameter.
func (p ParameterPositions) getPositions(parameter string) []int {
	if internal.IsNilOrZeroValue(p.parameterPositions) {
		return nil
	}

	v, ok := p.parameterPositions[parameter]
	if !ok {
		return nil
	}

	return v
}

// insert is a method of the NamedParameterPositions struct.
// It takes a parameter name and a position as input and inserts the position into the parameterPositions map.
func (p *ParameterPositions) insert(parameter string, position int) {
	if internal.IsNilOrZeroValue(p.parameterPositions) {
		p.parameterPositions = make(map[string][]int)
	}

	v, ok := p.parameterPositions[parameter]
	if !ok {
		v = make([]int, 0, 1)
	}

	p.parameterPositions[parameter] = append(v, position)
	p.totalPositions++
}

// BoundParameterValues is a type alias for a slice of any (an empty interface).
// It represents the positional parameters in an SQL statement.
type BoundParameterValues []any

// PreparedStatement is an interface that represents an SQL query with named parameters.
// It defines several methods for manipulating and executing the query.
//
// For a statement returned by PrepareStatement, values bound directly with
// BindParameterValue or BindParameterValues are defaults: Exec, Query, QueryRow
// and their Context variants send them on every call until
// ResetParametersValues clears them, whether the calls succeed or fail. Binders
// passed to those functions override the defaults for that call only. Such a
// statement may be shared between goroutines: direct binds, reads, resets and
// executions are synchronized, and each execution works on its own copy of the
// defaults. The copies are shallow; see ExecContext.
//
// Other implementations are used as before: binders are applied to the
// statement itself and ResetParametersValues is called after every execution.
// sqlext adds no synchronization for them, so whether they may be shared
// between goroutines depends on the implementation.
type PreparedStatement interface {
	// UnpreparedStatement returns the original SQL statement before preparation.
	UnpreparedStatement() string
	// Revised returns the parsed query with positional parameters.
	Revised() string
	// ResetParametersValues clears all bound values, so unbound parameters
	// are sent as NULL.
	ResetParametersValues()
	// ParameterPositions returns the parameter positions for the SQL statement.
	ParameterPositions() *ParameterPositions
	// BoundParameterValues returns the bound values in placeholder order.
	BoundParameterValues() BoundParameterValues
	// BindParameterValue binds a value to a named parameter in the SQL statement.
	BindParameterValue(bindParameter string, bindValue any) error
	// BindParameterValues sets the values for multiple named parameters using a list of BindNamedParameterValueFunc functions.
	BindParameterValues(binderFuncs ...BindParameterValueFunc) error
}

// preparedStatement is a struct that handles the translation of named parameters to positional parameters for SQL statements.
//
// originalStatement, revisedStatement and namedParamPositions are set by
// PrepareStatement and never written again, so they are read without the
// lock. boundNamedParamValues holds the defaults and is only accessed with mu
// held. Methods use pointer receivers so the mutex is never copied.
type preparedStatement struct {
	mu                    sync.RWMutex
	boundNamedParamValues BoundParameterValues
	namedParamPositions   *ParameterPositions
	revisedStatement      string
	originalStatement     string
}

// getTotalIndices returns the total number of parameter positions in the statement.
func (p *preparedStatement) getTotalIndices() int {
	if internal.IsNilOrZeroValue(p.namedParamPositions) {
		return 0
	}
	return p.namedParamPositions.totalPositions
}

// ResetParametersValues clears the default values. Executions that already
// took their copy of the defaults are not affected.
func (p *preparedStatement) ResetParametersValues() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if count := p.getTotalIndices(); count > 0 {
		p.boundNamedParamValues = make(BoundParameterValues, count)
	}
}

// UnpreparedStatement returns the original SQL statement before preparation.
func (p *preparedStatement) UnpreparedStatement() string {
	return p.originalStatement
}

// Revised returns the parsed query with positional parameters.
func (p *preparedStatement) Revised() string {
	return p.revisedStatement
}

// BoundParameterValues returns a shallow copy of the default values, so that
// callers can read it while other goroutines bind or execute. Writing to the
// returned slice does not change the statement.
func (p *preparedStatement) BoundParameterValues() BoundParameterValues {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return copyBoundValues(p.boundNamedParamValues)
}

// ParameterPositions returns the named parameter positions for the SQL statement.
//
// It returns a pointer to a copy, so a caller that overwrites the returned
// struct cannot change the positions that binding uses or race with it. The
// copy is shallow: the map inside is shared, which is safe because it is
// private and never modified after PrepareStatement.
func (p *preparedStatement) ParameterPositions() *ParameterPositions {
	if internal.IsNilOrZeroValue(p.namedParamPositions) {
		return nil
	}

	if len(p.namedParamPositions.parameterPositions) < 1 {
		return nil
	}

	positions := *p.namedParamPositions
	return &positions
}

// BindParameterValue binds a default value to a named parameter in the SQL
// statement. The value is kept until it is bound again or the values are reset.
func (p *preparedStatement) BindParameterValue(parameterName string, bindValue any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	bindAtPositions(p.boundNamedParamValues, p.namedParamPositions, parameterName, bindValue)
	return nil
}

// newExecution returns the private state of one execution, starting from a
// copy of the current defaults taken under the lock.
func (p *preparedStatement) newExecution() *executionStatement {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return &executionStatement{
		statement: p,
		values:    copyBoundValues(p.boundNamedParamValues),
	}
}

// executionStatement is what the binders of one Exec, Query or QueryRow call
// receive for a statement from PrepareStatement. It writes to values, which
// only this execution sees, so per-call binders never change the shared
// defaults and run without holding the statement's lock. That also lets a
// binder call the shared statement's methods without deadlocking.
type executionStatement struct {
	statement *preparedStatement
	values    BoundParameterValues
}

// UnpreparedStatement returns the original SQL statement before preparation.
func (e *executionStatement) UnpreparedStatement() string {
	return e.statement.UnpreparedStatement()
}

// Revised returns the parsed query with positional parameters.
func (e *executionStatement) Revised() string {
	return e.statement.Revised()
}

// ResetParametersValues clears the values of this execution only, including
// the defaults it started from; the statement's defaults are kept.
func (e *executionStatement) ResetParametersValues() {
	if len(e.values) > 0 {
		e.values = make(BoundParameterValues, len(e.values))
	}
}

// ParameterPositions returns a copy of the named parameter positions for the
// SQL statement; see preparedStatement.ParameterPositions.
func (e *executionStatement) ParameterPositions() *ParameterPositions {
	return e.statement.ParameterPositions()
}

// BoundParameterValues returns a shallow copy of the values of this execution.
func (e *executionStatement) BoundParameterValues() BoundParameterValues {
	return copyBoundValues(e.values)
}

// BindParameterValue binds a value for this execution only.
func (e *executionStatement) BindParameterValue(parameterName string, bindValue any) error {
	bindAtPositions(e.values, e.statement.namedParamPositions, parameterName, bindValue)
	return nil
}

// BindParameterValues applies the binders to this execution only.
func (e *executionStatement) BindParameterValues(binderFuncs ...BindParameterValueFunc) error {
	return applyBinders(e, binderFuncs)
}

// arguments returns the values to send to the executor. They are copied so
// that a binder that kept the executionStatement cannot change them while the
// executor uses them.
func (e *executionStatement) arguments() []any {
	return copyBoundValues(e.values)
}

// bindAtPositions stores bindValue at every position of parameterName.
// Unknown names are ignored.
func bindAtPositions(values BoundParameterValues, positions *ParameterPositions, parameterName string, bindValue any) {
	if internal.IsNilOrZeroValue(positions) {
		return
	}
	for _, i := range positions.getPositions(parameterName) {
		values[i] = bindValue
	}
}

// copyBoundValues returns a shallow copy of values, or nil when there are
// none, matching what BoundParameterValues has always returned for a
// statement without parameters.
func copyBoundValues(values []any) BoundParameterValues {
	if len(values) == 0 {
		return nil
	}
	out := make(BoundParameterValues, len(values))
	copy(out, values)
	return out
}

func applyBinders(p PreparedStatement, binderFuncs []BindParameterValueFunc) error {
	for i := range binderFuncs {
		if internal.IsNilOrZeroValue(binderFuncs[i]) {
			continue
		}
		if err := binderFuncs[i](p); err != nil {
			return err
		}
	}

	return nil
}

// BindParameterValueFunc is a function that sets the value for a named parameter in the query.
type BindParameterValueFunc func(p PreparedStatement) error

// BindParameterValueFuncs is a list of parameter binders, applied in order.
type BindParameterValueFuncs []BindParameterValueFunc

// NewBindParameterValueFuncs returns a copy of the given binders, or nil when
// none are given.
func NewBindParameterValueFuncs(bindParameterValueFuncs ...BindParameterValueFunc) BindParameterValueFuncs {
	count := len(bindParameterValueFuncs)
	if count == 0 {
		return nil
	}
	newBindParameterValueFuncs := make(BindParameterValueFuncs, count)
	copy(newBindParameterValueFuncs, bindParameterValueFuncs)
	return newBindParameterValueFuncs
}

// BindParameterValue creates a BindNamedParameterValueFunc that sets the value for a named parameter.
func BindParameterValue(bindParameter string, bindValue any) BindParameterValueFunc {
	return func(p PreparedStatement) error {
		return p.BindParameterValue(bindParameter, bindValue)
	}
}

// BindParameterValues applies the binders to the statement, so the values they
// bind become defaults. The binders run without the statement's lock and each
// bind is synchronized on its own, so an execution in another goroutine can
// copy the defaults while only some of the binders have run. An error stops
// the remaining binders; values bound before it are kept.
func (p *preparedStatement) BindParameterValues(binderFuncs ...BindParameterValueFunc) error {
	return applyBinders(p, binderFuncs)
}

var (
	_ PreparedStatement = (*preparedStatement)(nil)
	_ PreparedStatement = (*executionStatement)(nil)
)
